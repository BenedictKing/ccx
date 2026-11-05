package siteaccess

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/httpclient"
)

// doMinimalInferenceRequest 用渠道现有 Key 发一次最小真实推理请求。
// 仅用于用户主动触发的验收验证；不在后台自动执行，不消耗未授权的推理额度。
func doMinimalInferenceRequest(ctx context.Context, channel config.UpstreamConfig, protocol, model string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(channel.BaseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("渠道缺少 baseUrl")
	}
	apiKey := channel.APIKeys[0]

	var method, path string
	var body []byte
	var headers map[string]string
	switch protocol {
	case "messages":
		method, path = http.MethodPost, "/v1/messages"
		body = []byte(fmt.Sprintf(`{"model":%q,"max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`, model))
		headers = map[string]string{"x-api-key": apiKey, "anthropic-version": "2023-06-01"}
	case "chat":
		method, path = http.MethodPost, "/v1/chat/completions"
		body = []byte(fmt.Sprintf(`{"model":%q,"max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`, model))
		headers = map[string]string{"Authorization": "Bearer " + apiKey}
	case "responses":
		method, path = http.MethodPost, "/v1/responses"
		body = []byte(fmt.Sprintf(`{"model":%q,"input":"ping","max_output_tokens":16}`, model))
		headers = map[string]string{"Authorization": "Bearer " + apiKey}
	case "gemini":
		method, path = http.MethodPost, "/v1beta/models/"+model+":generateContent"
		body = []byte(`{"contents":[{"parts":[{"text":"ping"}]}]}`)
		headers = map[string]string{"x-goog-api-key": apiKey}
	default:
		return fmt.Errorf("不支持的协议: %s", protocol)
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := httpclient.GetManager().GetStandardClient(60*time.Second, channel.InsecureSkipVerify)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("真实请求发送失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("真实请求读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("真实请求 HTTP %d: %s", resp.StatusCode, truncateForError(respBody))
	}
	return nil
}
