package llm

import "net/http"

// Response 是 ccx 内部统一的 LLM 非流式响应中间表示。
//
// 设计与 Request 对称：
//   - Format：响应所属协议（由 Outbound 设置）。
//   - StatusCode / Header / Body：原始 HTTP 响应（用于 same-format 透传）。
//   - Usage：解析后的 token 统计（cross-format 必填，same-format 可选）。
//   - Metadata：transformer 之间的元信息槽位。
//
// Inbound 在 TransformResponse 阶段将本结构按入站格式重新序列化为字节流写回客户端。
type Response struct {
	Format     Format
	StatusCode int
	Header     http.Header
	Body       []byte
	Usage      *Usage
	Metadata   map[string]any
}
