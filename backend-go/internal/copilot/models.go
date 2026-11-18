package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/httpclient"
)

// ModelDetails 保留 Copilot 的模型级协议信息，缺失字段不代表已验证支持。
type ModelDetails struct {
	ID                 string   `json:"id"`
	SupportedEndpoints []string `json:"supported_endpoints,omitempty"`
}

type RouteOptions struct {
	BaseURL            string
	ProxyURL           string
	InsecureSkipVerify bool
	Model              string
	PreferredProtocol  string
	WebSocket          bool
}

type ModelRoute struct {
	BaseURL      string
	Path         string
	ServiceType  string
	RuntimeToken string
	Source       string
}

var ErrUnsupportedWebSocket = errors.New("模型未声明 Copilot Responses WebSocket 支持")

type modelsSnapshot struct {
	models    []ModelDetails
	expiresAt time.Time
}

// ModelResolver 的缓存按短期凭证、API 地址和代理隔离，刷新失败不沿用过期能力。
type ModelResolver struct {
	client       *http.Client
	resolveToken func(context.Context, string, string) (string, string, error)
	now          func() time.Time
	mu           sync.Mutex
	cache        map[string]modelsSnapshot
	inflight     map[string]chan struct{}
}

func NewModelResolver(client *http.Client, resolveToken func(context.Context, string, string) (string, string, error)) *ModelResolver {
	if resolveToken == nil {
		resolveToken = ResolveTokenWithProxy
	}
	return &ModelResolver{client: client, resolveToken: resolveToken, now: time.Now, cache: make(map[string]modelsSnapshot), inflight: make(map[string]chan struct{})}
}

var defaultModelResolver = NewModelResolver(nil, nil)

type modelResolverContextKey struct{}

// WithModelResolver 为隔离实例注入能力缓存及 token 解析器，避免共享账号状态。
func WithModelResolver(ctx context.Context, resolver *ModelResolver) context.Context {
	return context.WithValue(ctx, modelResolverContextKey{}, resolver)
}

func modelResolverForContext(ctx context.Context) *ModelResolver {
	if resolver, ok := ctx.Value(modelResolverContextKey{}).(*ModelResolver); ok && resolver != nil {
		return resolver
	}
	return defaultModelResolver
}

func ResolveModelRoute(ctx context.Context, oauthToken string, opts RouteOptions) (*ModelRoute, error) {
	return modelResolverForContext(ctx).Resolve(ctx, oauthToken, opts)
}

func DiscoverModels(ctx context.Context, oauthToken string, opts RouteOptions) ([]ModelDetails, string, error) {
	resolver := modelResolverForContext(ctx)
	token, baseURL, err := resolver.resolveCredentials(ctx, oauthToken, opts)
	if err != nil {
		return nil, "", err
	}
	models, err := resolver.Models(ctx, token, baseURL, opts.ProxyURL, opts.InsecureSkipVerify)
	return models, baseURL, err
}

func (m *ModelResolver) resolveCredentials(ctx context.Context, oauthToken string, opts RouteOptions) (string, string, error) {
	token, discoveredBaseURL, err := m.resolveToken(ctx, oauthToken, opts.ProxyURL)
	if err != nil {
		return "", "", err
	}
	baseURL := strings.TrimRight(strings.TrimSuffix(opts.BaseURL, "#"), "/")
	if baseURL == "" || baseURL == DefaultAPIBaseURL {
		baseURL = strings.TrimRight(discoveredBaseURL, "/")
	}
	if baseURL == "" {
		baseURL = DefaultAPIBaseURL
	}
	return token, baseURL, nil
}

func (m *ModelResolver) Resolve(ctx context.Context, oauthToken string, opts RouteOptions) (*ModelRoute, error) {
	token, baseURL, err := m.resolveCredentials(ctx, oauthToken, opts)
	if err != nil {
		return nil, err
	}
	models, err := m.Models(ctx, token, baseURL, opts.ProxyURL, opts.InsecureSkipVerify)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var statusErr *ModelsError
		if !errors.As(err, &statusErr) || (statusErr.Status != http.StatusNotFound && statusErr.Status != http.StatusMethodNotAllowed) {
			return nil, err
		}
		// 仅旧账号/中转明确没有 /models 时保留历史默认；临时失败不能猜测协议。
		if opts.WebSocket {
			return nil, ErrUnsupportedWebSocket
		}
		return &ModelRoute{baseURL, "/responses", "responses", token, "legacy-default"}, nil
	}
	for _, model := range models {
		if model.ID != opts.Model {
			continue
		}
		path, serviceType, source, err := selectModelEndpoint(model, opts)
		if err != nil {
			return nil, err
		}
		return &ModelRoute{baseURL, path, serviceType, token, source}, nil
	}
	return nil, fmt.Errorf("模型 %q 未列于 Copilot /models", opts.Model)
}

func selectModelEndpoint(model ModelDetails, opts RouteOptions) (string, string, string, error) {
	if opts.WebSocket {
		for _, endpoint := range model.SupportedEndpoints {
			if endpoint == "ws:/responses" {
				return "/responses", "responses", "models-endpoint", nil
			}
		}
		return "", "", "", ErrUnsupportedWebSocket
	}
	if len(model.SupportedEndpoints) == 0 {
		return "/responses", "responses", "legacy-default", nil
	}
	paths := map[string]string{"responses": "/responses", "chat": "/chat/completions", "messages": "/v1/messages"}
	serviceTypes := map[string]string{"responses": "responses", "chat": "openai", "messages": "claude"}
	for _, protocol := range []string{opts.PreferredProtocol, "responses", "messages", "chat"} {
		path := paths[protocol]
		if path == "" {
			continue
		}
		for _, endpoint := range model.SupportedEndpoints {
			if endpoint == path {
				return path, serviceTypes[protocol], "models-endpoint", nil
			}
		}
	}
	return "", "", "", fmt.Errorf("模型 %q 没有可支持的 Copilot HTTP endpoint", model.ID)
}

type ModelsError struct{ Status int }

func (e *ModelsError) Error() string { return fmt.Sprintf("Copilot /models 返回 HTTP %d", e.Status) }

// Models 使用 runtime token 请求能力信息；返回副本，调用方不能修改共享缓存。
func (m *ModelResolver) Models(ctx context.Context, token, baseURL, proxyURL string, insecure bool) ([]ModelDetails, error) {
	key := tokenCacheKey(fmt.Sprintf("%s\n%s\n%s\n%t", token, baseURL, proxyURL, insecure))
	m.mu.Lock()
	cached, ok := m.cache[key]
	if ok && m.now().Before(cached.expiresAt) {
		m.mu.Unlock()
		return cloneModels(cached.models), nil
	}
	if pending := m.inflight[key]; pending != nil {
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending:
			return m.Models(ctx, token, baseURL, proxyURL, insecure)
		}
	}
	pending := make(chan struct{})
	m.inflight[key] = pending
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.inflight, key)
		close(pending)
		m.mu.Unlock()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	ApplyRuntimeHeaders(req.Header, token)
	client := m.client
	if client == nil {
		client = httpclient.GetManager().NewStandardClient(10*time.Second, insecure, proxyURL)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &ModelsError{resp.StatusCode}
	}
	var payload struct {
		Data []ModelDetails `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析 Copilot /models 失败")
	}
	if len(payload.Data) == 0 {
		return nil, errors.New("模型列表未从 Copilot /models 返回")
	}
	m.mu.Lock()
	if len(m.cache) >= 128 {
		for existing := range m.cache {
			delete(m.cache, existing)
			break
		}
	}
	m.cache[key] = modelsSnapshot{payload.Data, m.now().Add(5 * time.Minute)}
	m.mu.Unlock()
	return cloneModels(payload.Data), nil
}

func cloneModels(models []ModelDetails) []ModelDetails {
	cloned := append([]ModelDetails(nil), models...)
	for i := range cloned {
		cloned[i].SupportedEndpoints = append([]string(nil), models[i].SupportedEndpoints...)
	}
	return cloned
}
