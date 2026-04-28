[P1] backend-go/internal/handlers/chat/channels.go, backend-go/internal/handlers/responses/channels.go, backend-go/internal/handlers/gemini/channels.go - 移动 API Key 的路由参数 `id` 解析错误被忽略 - 非法请求可能误操作 0 号渠道而不是返回 400 - 统一补齐 `id` 参数校验并与 messages 渠道实现对齐
[P2] backend-go/internal/handlers/messages/handler.go, backend-go/internal/handlers/responses/handler.go - 请求体 JSON 解析失败后仍继续进入后续处理链路 - 客户端参数错误会被包装成 failover 或 500，误导排障和监控 - 在 HTTP 入口直接返回稳定的 400 错误并停止后续处理
[P2] frontend/src/components/AddChannelModal.vue - 模型探测缓存未随 baseUrl 或 serviceType 变化失效 - 弹窗内切换目标上游后可能继续显示旧模型列表和旧状态码 - 将模型探测缓存键扩展为包含上游上下文，或在相关字段变化时主动清空缓存

修复顺序
1. 先修复后端 `id` 参数校验问题，避免错误请求直接改动错误渠道
2. 再修复后端请求体解析边界，确保坏请求在入口稳定失败
3. 最后修复前端模型探测缓存失效逻辑，消除陈旧展示
