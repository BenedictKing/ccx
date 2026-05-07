// Package handlers 不直接定义任何东西；这里 adapters 子包提供 4 个 handler
// 之间共享的 inbound/outbound adapter 公共契约。
//
// PR1 阶段：所有 adapter 仅作为"接口实现"存在，不被 handler.go 调用；
// PR2/PR3 切流量时，handler 通过 pipeline.Factory.Pipeline(inbound, outbound)
// 接入它们。
package adapters

import (
	"errors"
	"net/http"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/gin-gonic/gin"
)

// llm.Request.Metadata 上 4 个共享键。
//
// 4 个 handler 的 adapter 通过这些键在 inbound/outbound 之间传递必要的运行时
// 上下文。理由：pipeline.Outbound.TransformRequest 是纯函数（仅持 ctx +
// llm.Request），但 ccx 现有 buildProviderRequest / providers.ConvertToProviderRequest
// 需要 *gin.Context / *config.UpstreamConfig / apiKey / baseURL，所以必须通过
// Metadata 携带。
//
// 注意：
//   - 这些值是 attempt 级状态，PR2 跨 channel 切换时由 outbound 在 NextChannel
//     之后写入新的 upstreamConfig / apiKey / baseURL。
//   - inbound 不修改 Metadata 上的 outbound 键，仅写入 ginContext / rawHTTPRequest。
const (
	// MetaGinContext 是入站 gin.Context 句柄。inbound 写入；outbound 读取以
	// 调用现有 forwarding.Build / provider.ConvertToProviderRequest。
	MetaGinContext = "adapter:ginContext"

	// MetaUpstreamConfig 是当前 attempt 选中的 *config.UpstreamConfig。
	// outbound 在 TransformRequest 之前由调度器/loadbalancer 写入。
	MetaUpstreamConfig = "adapter:upstreamConfig"

	// MetaAPIKey 是当前 attempt 选中的上游 API key（明文）。outbound 写入。
	MetaAPIKey = "adapter:apiKey"

	// MetaBaseURL 是当前 attempt 选中的上游 baseURL。outbound 写入。
	MetaBaseURL = "adapter:baseURL"
)

// 这些错误统一标识 adapter 因 metadata 缺失或类型错误而失败的场景。
//
// outbound 在 TransformRequest 阶段返回这些错误意味着调用方（pipeline 或
// loadbalancer）忘了写入对应键，应作为编程错误而非可重试错误处理。
var (
	ErrMissingGinContext    = errors.New("adapter: gin.Context missing in llm.Request.Metadata")
	ErrMissingUpstreamConfig = errors.New("adapter: upstream config missing in llm.Request.Metadata")
	ErrMissingAPIKey        = errors.New("adapter: API key missing in llm.Request.Metadata")
	ErrMissingBaseURL       = errors.New("adapter: base URL missing in llm.Request.Metadata")
)

// SetGinContext 在 llm.Request.Metadata 上写入 gin.Context；inbound 调用。
//
// 若 Metadata 为 nil 会自动初始化。
func SetGinContext(req *llm.Request, c *gin.Context) {
	if req == nil {
		return
	}
	if req.Metadata == nil {
		req.Metadata = make(map[string]any, 4)
	}
	req.Metadata[MetaGinContext] = c
}

// SetUpstreamBinding 在 llm.Request.Metadata 上一次性写入 upstream/apiKey/baseURL。
// outbound 在确定本次 attempt 使用的 channel/key 之后调用。
func SetUpstreamBinding(req *llm.Request, upstream *config.UpstreamConfig, apiKey, baseURL string) {
	if req == nil {
		return
	}
	if req.Metadata == nil {
		req.Metadata = make(map[string]any, 4)
	}
	req.Metadata[MetaUpstreamConfig] = upstream
	req.Metadata[MetaAPIKey] = apiKey
	req.Metadata[MetaBaseURL] = baseURL
}

// GinContext 从 Metadata 取出 gin.Context；缺失或类型错误返回 ErrMissingGinContext。
func GinContext(req *llm.Request) (*gin.Context, error) {
	if req == nil || req.Metadata == nil {
		return nil, ErrMissingGinContext
	}
	v, ok := req.Metadata[MetaGinContext]
	if !ok {
		return nil, ErrMissingGinContext
	}
	c, ok := v.(*gin.Context)
	if !ok || c == nil {
		return nil, ErrMissingGinContext
	}
	return c, nil
}

// UpstreamBinding 从 Metadata 取出 upstream/apiKey/baseURL；任一缺失即返回错误。
func UpstreamBinding(req *llm.Request) (*config.UpstreamConfig, string, string, error) {
	if req == nil || req.Metadata == nil {
		return nil, "", "", ErrMissingUpstreamConfig
	}
	uv, ok := req.Metadata[MetaUpstreamConfig]
	if !ok {
		return nil, "", "", ErrMissingUpstreamConfig
	}
	upstream, ok := uv.(*config.UpstreamConfig)
	if !ok || upstream == nil {
		return nil, "", "", ErrMissingUpstreamConfig
	}
	kv, ok := req.Metadata[MetaAPIKey]
	if !ok {
		return upstream, "", "", ErrMissingAPIKey
	}
	apiKey, ok := kv.(string)
	if !ok || apiKey == "" {
		return upstream, "", "", ErrMissingAPIKey
	}
	bv, ok := req.Metadata[MetaBaseURL]
	if !ok {
		return upstream, apiKey, "", ErrMissingBaseURL
	}
	baseURL, ok := bv.(string)
	if !ok || baseURL == "" {
		return upstream, apiKey, "", ErrMissingBaseURL
	}
	return upstream, apiKey, baseURL, nil
}

// CopyResponse 把上游 *http.Response 拷贝为 llm.Response。Body 已读完并保存
// 在 llm.Response.Body 中；调用方可关闭原 resp.Body。
//
// 该 helper 仅用于"non-stream 响应 → llm.Response"路径，stream 响应应由
// outbound.TransformStream 单独处理。
//
// usageParser 由各 adapter 提供，用于在 body 解析后填充 llm.Response.Usage。
func CopyResponse(format llm.Format, resp *http.Response, body []byte, usageParser func([]byte) *llm.Usage) *llm.Response {
	header := resp.Header.Clone()
	r := &llm.Response{
		Format:     format,
		StatusCode: resp.StatusCode,
		Header:     header,
		Body:       body,
	}
	if usageParser != nil {
		r.Usage = usageParser(body)
	}
	return r
}
