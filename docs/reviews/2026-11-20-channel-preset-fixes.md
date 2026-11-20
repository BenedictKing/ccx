# 一键预设兼容性修复

## 行为修复

- DeepSeek 的 Messages → Chat 转换保留 base64、URL 图片和工具结果图片，保持图文顺序与工具调用 ID。
- Web 与桌面端按客户端入口选择源类别；Responses 入口始终使用 astra、codex-auto-review、luna、sol、terra，即使上游使用 Claude 协议。
- Kimi/GLM 共享预设使用 reasoning_effort；medium 转 high、xhigh 转 max、none 转 low，GLM 旧的 disabled 思考请求转为 enabled + low。
- GLM-5.3 的三个渠道预设明确禁用视觉；默认接入走通用 Chat Completion API，Coding Plan 的 Messages/Responses 通过网关转换。
- Kimi K3.1 Pro 注册表移除不支持的 medium；共享源经生成器同步 Web、桌面前端与 Go 后端。
- 未指定 temperature 时使用上游默认值；未启用参数兼容样式的 Chat 同协议请求继续保持透传。
- 原则：源模型类别与上游协议分开；图片格式转换复用现有转换器；模型能力以共享注册表为准。

## 验证

- Web：289 项测试、类型检查与生产构建通过；桌面前端：114 项测试、类型检查与生产构建通过。
- Go 后端与桌面端各自 go test ./... 通过；改动文件 ESLint、gofmt 与 git diff --check 通过。
- 上一轮失败的三个后端复现用例已转绿；两端 composable 的源类别、最新目标 ID 和视觉限制矩阵通过。
- 后端全栈 make build 通过。
- 本次未调用付费上游 API；没有停止或重启已有服务。Go 测试使用 /tmp 缓存规避默认缓存目录权限限制。

## 修改文件

- [backend-go/internal/config/config_utils.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/backend-go/internal/config/config_utils.go)
- [backend-go/internal/config/generated_model_registry.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/backend-go/internal/config/generated_model_registry.go)
- [backend-go/internal/converters/responses_items.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/backend-go/internal/converters/responses_items.go)
- [backend-go/internal/handlers/chat/request_build.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/backend-go/internal/handlers/chat/request_build.go)
- [backend-go/internal/handlers/chat/preset_reasoning_test.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/backend-go/internal/handlers/chat/preset_reasoning_test.go)
- [backend-go/internal/providers/openai.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/backend-go/internal/providers/openai.go)
- [backend-go/internal/providers/responses.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/backend-go/internal/providers/responses.go)
- [backend-go/internal/providers/preset_compat_test.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/backend-go/internal/providers/preset_compat_test.go)
- [backend-go/internal/types/types.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/backend-go/internal/types/types.go)
- [frontend/src/composables/useEditChannelPresets.ts](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/frontend/src/composables/useEditChannelPresets.ts)
- [frontend/src/composables/useEditChannelPresets.test.ts](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/frontend/src/composables/useEditChannelPresets.test.ts)
- [frontend/src/generated/claudeMessagesPresets.ts](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/frontend/src/generated/claudeMessagesPresets.ts)
- [frontend/src/generated/codexResponsesPresets.ts](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/frontend/src/generated/codexResponsesPresets.ts)
- [frontend/src/generated/openaiChatPresets.ts](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/frontend/src/generated/openaiChatPresets.ts)
- [frontend/src/generated/modelRegistry.ts](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/frontend/src/generated/modelRegistry.ts)
- [desktop/frontend/src/composables/useChannelEditPresets.ts](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/desktop/frontend/src/composables/useChannelEditPresets.ts)
- [desktop/frontend/src/composables/useChannelEditPresets.test.ts](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/desktop/frontend/src/composables/useChannelEditPresets.test.ts)
- [desktop/frontend/src/generated/claude-messages-presets.ts](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/desktop/frontend/src/generated/claude-messages-presets.ts)
- [desktop/frontend/src/generated/codex-responses-presets.ts](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/desktop/frontend/src/generated/codex-responses-presets.ts)
- [desktop/frontend/src/generated/openai-chat-presets.ts](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/desktop/frontend/src/generated/openai-chat-presets.ts)
- [desktop/frontend/src/generated/model-registry.ts](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/desktop/frontend/src/generated/model-registry.ts)
- [desktop/internal/channelpreset/preset.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/desktop/internal/channelpreset/preset.go)
- [desktop/internal/channelpreset/preset_test.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/desktop/internal/channelpreset/preset_test.go)
- [desktop/internal/channelpreset/glm_compat_test.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/desktop/internal/channelpreset/glm_compat_test.go)
- [desktop/internal/channelpreset/generated_claude_messages_presets.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/desktop/internal/channelpreset/generated_claude_messages_presets.go)
- [desktop/internal/channelpreset/generated_codex_responses_presets.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/desktop/internal/channelpreset/generated_codex_responses_presets.go)
- [desktop/internal/channelpreset/generated_openai_chat_presets.go](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/desktop/internal/channelpreset/generated_openai_chat_presets.go)
- [shared/channel-presets/claude-messages.json](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/shared/channel-presets/claude-messages.json)
- [shared/channel-presets/codex-responses.json](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/shared/channel-presets/codex-responses.json)
- [shared/channel-presets/openai-chat.json](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/shared/channel-presets/openai-chat.json)
- [shared/model-registry/ccx_model_registry.json](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/shared/model-registry/ccx_model_registry.json)
- [docs/reviews/2026-11-20-channel-preset-fixes.md](/Users/petaflops/works/api-gateways/llm-routing/ccx-v3-lean/docs/reviews/2026-11-20-channel-preset-fixes.md)
