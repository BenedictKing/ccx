# 首批流式修复实施记录

## 范围

- C1：Responses content_part 文本、Chat refusal/audio 语义识别。
- C2：preflight remainder 仅清空，不重复加入缓冲。
- C3：EOF 不重发尾行正文，只补 SSE 终止符。
- C4：Chat/Gemini 补齐尾事件，reader 监听取消信号；同步 Images 调用。
- C5：Responses 未知上游错误进入 failover，保留黑名单和确定性请求拒绝。
- C6：显式 `#` Base URL 保留完整路径，不被控制台清洗或错误去重。
- 未引入 main 的 3.0 架构；未操作 3688 端口进程。

## 验证与边界

- 前端全量 229 项测试、类型检查、生产构建通过；构建仍有大分块提示。
- 新增回归用例已复现原行为中的语义漏检、缓冲双写、尾行重发与缺少终止空行。
- 后端验证包括全量测试、受影响五包 race 检查和本地构建。
- 独立审查发现的请求拒绝终态、无空格 data 字段、Gemini 尾随注释边界一并处理。
- reader 取消用于解除阻塞发送；阻塞 Read 仍依赖关闭 body 或上游请求上下文取消。
- 单次 failover 的 reader 最迟在整个请求取消时收敛，未重构每次尝试的生命周期。
- 未进行真实生产上游请求测试；不宣称生产实例已加载新代码。

## 修改文件（相对仓库根目录）

- `backend-go/internal/handlers/common/stream_processor.go`
- `backend-go/internal/handlers/common/stream_test.go`
- `backend-go/internal/handlers/common/stream_chunk_reader.go`
- `backend-go/internal/handlers/common/stream_chunk_reader_test.go`
- `backend-go/internal/handlers/chat/stream.go`
- `backend-go/internal/handlers/chat/stream_tools.go`
- `backend-go/internal/handlers/chat/handler_stream_preflight_test.go`
- `backend-go/internal/handlers/gemini/stream.go`
- `backend-go/internal/handlers/gemini/stream_tail_test.go`
- `backend-go/internal/handlers/images/handler.go`
- `backend-go/internal/handlers/responses/helpers.go`
- `backend-go/internal/handlers/responses/stream.go`
- `backend-go/internal/handlers/responses/fold.go`
- `backend-go/internal/handlers/responses/handler_session_test.go`
- `backend-go/internal/handlers/responses/handler_stream_preflight_test.go`
- `frontend/src/utils/baseUrlSemantics.ts`
- `frontend/src/utils/base-url-semantics.test.ts`
