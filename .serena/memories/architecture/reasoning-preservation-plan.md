# Reasoning 状态保留与 Continuation 实施计划

## 三阶段概览

### 阶段一：让 reasoning 状态活下来（零行为变更基石）
- 流式路径 session 回写
- reasoning EncryptedContent 保留在 session
- post-commit stall 时补写 response.incomplete

### 阶段二：ThinkingCache 扩展 + Responses 流式 reasoning 收集
- thinkingcache 扩展支持 EncryptedContent
- Responses 流式路径 reasoning item 收集
- converter 路径保留 EncryptedContent

### 阶段三：分层保真 compact
- writeCompactedSession 分层：保留最近 K 条 reasoning item
- truncateTranscriptWithLogTag 改为 item 边界感知
- function_call_output 不再完全丢弃