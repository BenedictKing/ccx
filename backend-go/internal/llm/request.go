package llm

import (
	"maps"
	"net/http"
)

// Request 是 ccx 内部统一的 LLM 请求中间表示。
//
// 设计目标：尽可能保留入站请求的完整信息，让 Outbound transformer 能够：
//   1. 还原出与上游协议相符的 *http.Request；
//   2. 支持 same-format raw passthrough（直接复用 RawBody / RawRequest）；
//   3. 支持 cross-format 转换（用 Format + Model + Stream 等字段重建）。
//
// 字段：
//   - Format：入站请求所属协议（驱动 cross-format 转换决策）。
//   - Model：请求中的模型名（已经过 inbound 解析，可能尚未做 model redirect）。
//   - Stream：是否为流式请求（nil 表示协议中未指定）。
//   - RawBody：入站请求体的原始字节（同格式透传时使用）。
//   - RawRequest：入站 *http.Request 的句柄，仅用于读取 header / context；
//     transformer 不应直接转发该对象给上游。
//   - Metadata：transformer 之间共享的轻量元信息（如已解析的 messages / tools），
//     避免重复反序列化；具体 schema 由 adapter 内部约定。
type Request struct {
	Format     Format
	Model      string
	Stream     *bool
	RawBody    []byte
	RawRequest *http.Request
	Metadata   map[string]any
}

// IsStream 返回是否为流式请求；当 Stream 为 nil 时返回 false。
func (r *Request) IsStream() bool {
	if r == nil || r.Stream == nil {
		return false
	}
	return *r.Stream
}

// Clone 返回 Request 的浅拷贝，RawBody / Metadata 共享底层对象。
//
// pipeline retry 阶段需要在多个 attempt 中复用 Request；Outbound 在
// 修改 Request 时应使用 Clone 确保不污染原始对象。
func (r *Request) Clone() *Request {
	if r == nil {
		return nil
	}
	cp := *r
	if r.Metadata != nil {
		md := make(map[string]any, len(r.Metadata))
		maps.Copy(md, r.Metadata)
		cp.Metadata = md
	}
	return &cp
}
