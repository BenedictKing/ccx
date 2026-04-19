package config

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
	"golang.org/x/net/proxy"
)

const (
	modelsHealthCheckReason      = "models_health_check_non_200"
	modelsHealthCheckBodyMaxSize = 1024
)

var modelsHealthCheckVersionPattern = regexp.MustCompile(`/v\d+[a-z]*$`)

type modelsHealthCheckTarget struct {
	apiType            string
	channelIndex       int
	channelName        string
	serviceType        string
	baseURLs           []string
	apiKeys            []string
	insecureSkipVerify bool
	proxyURL           string
	interval           time.Duration
}

func (cm *ConfigManager) modelsHealthCheckLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	lastRunAt := make(map[string]time.Time)
	cm.runModelsHealthCheckOnce(lastRunAt, time.Now())

	for {
		select {
		case <-cm.stopChan:
			return
		case now := <-ticker.C:
			cm.runModelsHealthCheckOnce(lastRunAt, now)
		}
	}
}

func (cm *ConfigManager) runModelsHealthCheckOnce(lastRunAt map[string]time.Time, now time.Time) {
	targets := cm.collectModelsHealthCheckTargets(now, lastRunAt)
	for _, target := range targets {
		cm.runModelsHealthCheckForTarget(target)
	}
}

func (cm *ConfigManager) collectModelsHealthCheckTargets(now time.Time, lastRunAt map[string]time.Time) []modelsHealthCheckTarget {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	type upstreamGroup struct {
		apiType   string
		upstreams []UpstreamConfig
	}

	groups := []upstreamGroup{
		{apiType: "Messages", upstreams: cm.config.Upstream},
		{apiType: "Responses", upstreams: cm.config.ResponsesUpstream},
		{apiType: "Chat", upstreams: cm.config.ChatUpstream},
		{apiType: "Gemini", upstreams: cm.config.GeminiUpstream},
	}

	targets := make([]modelsHealthCheckTarget, 0)
	activeScheduleKeys := make(map[string]struct{})

	for _, group := range groups {
		for idx := range group.upstreams {
			upstream := group.upstreams[idx]

			if !upstream.IsModelsHealthCheckEnabled() {
				continue
			}
			if len(upstream.APIKeys) == 0 {
				continue
			}

			status := strings.ToLower(strings.TrimSpace(upstream.Status))
			if status != "" && status != "active" {
				continue
			}

			allBaseURLs := upstream.GetAllBaseURLs()
			if len(allBaseURLs) == 0 {
				continue
			}

			interval := time.Duration(upstream.GetModelsHealthCheckIntervalMinutes()) * time.Minute
			if interval < time.Minute {
				interval = time.Minute
			}

			scheduleKey := modelsHealthCheckScheduleKey(group.apiType, idx)
			activeScheduleKeys[scheduleKey] = struct{}{}
			if last, exists := lastRunAt[scheduleKey]; exists && now.Sub(last) < interval {
				continue
			}
			lastRunAt[scheduleKey] = now

			target := modelsHealthCheckTarget{
				apiType:            group.apiType,
				channelIndex:       idx,
				channelName:        upstream.Name,
				serviceType:        upstream.ServiceType,
				baseURLs:           append([]string(nil), allBaseURLs...),
				apiKeys:            append([]string(nil), upstream.APIKeys...),
				insecureSkipVerify: upstream.InsecureSkipVerify,
				proxyURL:           upstream.ProxyURL,
				interval:           interval,
			}
			targets = append(targets, target)
		}
	}

	for scheduleKey := range lastRunAt {
		if _, exists := activeScheduleKeys[scheduleKey]; !exists {
			delete(lastRunAt, scheduleKey)
		}
	}

	return targets
}

func (cm *ConfigManager) runModelsHealthCheckForTarget(target modelsHealthCheckTarget) {
	client := newModelsHealthCheckClient(target.insecureSkipVerify, target.proxyURL)
	for _, apiKey := range target.apiKeys {
		statusCode, bodyPreview, gotResponse, err := probeKeyModelsAcrossBaseURLs(client, target, apiKey)
		if err != nil {
			log.Printf("[%s-ModelsHealth] 巡检失败(已跳过): channel=%s, key=%s, error=%v", target.apiType, target.channelName, utils.MaskAPIKey(apiKey), err)
			continue
		}
		if !gotResponse || statusCode == http.StatusOK {
			continue
		}

		message := fmt.Sprintf("models health check non-200 status=%d", statusCode)
		if bodyPreview != "" {
			message += ", body=" + bodyPreview
		}

		if err := cm.BlacklistKey(target.apiType, target.channelIndex, apiKey, modelsHealthCheckReason, message); err != nil {
			log.Printf("[%s-ModelsHealth] 拉黑失败: channel=%s, key=%s, status=%d, error=%v", target.apiType, target.channelName, utils.MaskAPIKey(apiKey), statusCode, err)
			continue
		}

		log.Printf("[%s-ModelsHealth] 已拉黑 key: channel=%s, key=%s, status=%d, interval=%s", target.apiType, target.channelName, utils.MaskAPIKey(apiKey), statusCode, target.interval)
	}
}

func probeKeyModelsAcrossBaseURLs(client *http.Client, target modelsHealthCheckTarget, apiKey string) (statusCode int, bodyPreview string, gotResponse bool, err error) {
	var firstNon200Status int
	var firstNon200Body string
	var lastErr error

	for _, baseURL := range target.baseURLs {
		checkStatus, body, probeErr := probeModelsEndpoint(client, target.serviceType, baseURL, apiKey)
		if probeErr != nil {
			lastErr = probeErr
			continue
		}

		gotResponse = true
		if checkStatus == http.StatusOK {
			return http.StatusOK, "", true, nil
		}
		if firstNon200Status == 0 {
			firstNon200Status = checkStatus
			firstNon200Body = body
		}
	}

	if gotResponse {
		return firstNon200Status, firstNon200Body, true, nil
	}
	if lastErr != nil {
		return 0, "", false, lastErr
	}
	return 0, "", false, nil
}

func probeModelsEndpoint(client *http.Client, serviceType, baseURL, apiKey string) (statusCode int, bodyPreview string, err error) {
	modelsURL := buildModelsHealthCheckURL(baseURL, serviceType)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return 0, "", err
	}
	utils.SetAuthenticationHeader(req.Header, apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, modelsHealthCheckBodyMaxSize))
	return resp.StatusCode, normalizeBodyPreview(body), nil
}

func buildModelsHealthCheckURL(baseURL, serviceType string) string {
	versionPrefix := "/v1"
	if strings.EqualFold(serviceType, "gemini") {
		versionPrefix = "/v1beta"
	}

	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	if skipVersionPrefix {
		baseURL = strings.TrimSuffix(baseURL, "#")
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	hasVersionSuffix := modelsHealthCheckVersionPattern.MatchString(baseURL)
	if !hasVersionSuffix && !skipVersionPrefix {
		baseURL += versionPrefix
	}

	return baseURL + "/models"
}

func modelsHealthCheckScheduleKey(apiType string, channelIndex int) string {
	return fmt.Sprintf("%s:%d", apiType, channelIndex)
}

func normalizeBodyPreview(body []byte) string {
	preview := strings.TrimSpace(string(body))
	if len(preview) <= 300 {
		return preview
	}
	return preview[:300] + "..."
}

func newModelsHealthCheckClient(insecureSkipVerify bool, proxyURL string) *http.Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   8,
		ForceAttemptHTTP2:     true,
	}
	if insecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	applyModelsHealthCheckProxy(transport, proxyURL)

	return &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
	}
}

func applyModelsHealthCheckProxy(transport *http.Transport, proxyAddr string) {
	if strings.TrimSpace(proxyAddr) == "" {
		return
	}

	u, err := url.Parse(proxyAddr)
	if err != nil {
		log.Printf("[ModelsHealth] 代理地址解析失败: %s, error=%v", utils.RedactURLCredentials(proxyAddr), err)
		return
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https":
		transport.Proxy = http.ProxyURL(u)
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if u.User != nil {
			password, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: password}
		}
		dialer, dialErr := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if dialErr != nil {
			log.Printf("[ModelsHealth] SOCKS5 代理创建失败: %s, error=%v", utils.RedactURLCredentials(proxyAddr), dialErr)
			return
		}
		if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
			transport.DialContext = contextDialer.DialContext
		} else {
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		}
		transport.ForceAttemptHTTP2 = false
	default:
		log.Printf("[ModelsHealth] 不支持的代理协议: %s", scheme)
	}
}
