package llm

// StreamEvent 表示上游 SSE 流的一个事件。
//
// 设计与 axonhub/llm/httpclient/StreamEvent 对齐：保留原始 event/data 字段，
// 不在抽象层做 JSON 解析；具体协议解析由 Outbound transformer 在
// TransformStream 阶段完成。
//
// 字段：
//   - Type：SSE event 名（OpenAI 系一般为空，Claude 系会有 message_start 等）。
//   - Data：data: 行后面的原始字节（可能是 JSON、可能是 [DONE]）。
//   - LastEventID：SSE id 字段，多数上游不使用。
//   - Retry：SSE retry 字段，多数上游不使用。
//
// 当流结束时，生产端应关闭 chan 而不是发送一个特殊 sentinel；消费端通过
// Stream.Next 返回 false + Err 区分正常结束与异常。
type StreamEvent struct {
	Type        string
	Data        []byte
	LastEventID string
	Retry       int
}
