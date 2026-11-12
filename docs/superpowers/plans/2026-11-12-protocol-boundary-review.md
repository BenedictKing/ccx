# 协议边界复核与修复

基线为 `6d7cbf15`，保留该提交的 Codex 模型可见字节估算，以及已移除的能力测试用户入口。

## 本批范围

用户补充的首要约束：同协议在无额外配置时默认直接透传，跨协议才转换。

1. 原生 Responses 默认保留 additional_tools 原位；转换协议或显式兼容开关才提升声明。
2. namespace/custom 工具保留完整身份、空输入、ID、strict 等字段，修复同名历史回放与流式还原。
3. previous_response_id 续轮从本地历史重建增量声明；failover 每次独立构造工具上下文。
4. 媒体预算识别工具结果中的媒体、带参数 PCM 与文本附件，保持图片和 Codex 字节估算不变。
5. Kimi Code 订阅模型别名与 Kimi API 按量价格分离，禁止把同能力视作同计费产品。

## 约束与验证

- 只修改已复现的缺陷；真实配置、运行进程与已有未提交改动保持不变。
- 前端依赖已核验；lean 与 main 有意分化，不同步 main。
- 每项先构造失败用例，覆盖透传、非流式、流式和历史回放；未知能力保守保留。
- 定向测试通过后运行全量 Go 测试、race、go vet、Web/Desktop 构建与前端测试。
- 主题分别本地提交；回退采用反向补丁，不重写历史、不推送。

## 资料与结果

- [OpenAI 会话前缀](https://developers.openai.com/api/docs/guides/preserved-reasoning)：增量工具需保留原始位置。
- [工具 schema](https://developers.openai.com/api/reference/resources/responses/methods/create)：custom 调用也有 namespace；function 的 strict 可显式设置。
- [Kimi API 计费](https://platform.kimi.com/docs/pricing/chat) 与 [Kimi Code 模型](https://www.kimi.com/code/docs/kimi-code/models.html) 是不同计费产品。
- 并发任务提交的 `a77f4ab8`（工具 ID 与请求错误分类）已保留。

## 默认透传规则

- Messages → Claude、Chat → OpenAI 兼容接口、Responses → Responses、Gemini → Gemini，以及 Images/Vectors 原生入口，默认保留请求正文原始字节。
- 模型映射、参数覆盖和兼容开关明确启用时，才执行对应修改；改写 JSON 时保留大整数精度。
- `normalizeMetadataUserId` 后端与 Web/Desktop 默认均为 false；已有显式 true 配置继续生效。
- Gemini Chat 兼容入口仅在 `injectDummyThoughtSignature` 为 true 时注入 dummy signature。
- 原生非流式响应保留上游字段、usage、状态码与 Content-Type；转换后的响应继续使用目标协议的 JSON 类型。
- Messages/Responses 原生流解析仅供预检、指标和会话保存，保留原始 SSE 字节，不默认补 usage、规范化事件或合成 completed。
- 查询参数保留重复值和编码；路由必需参数优先，网关鉴权 query `key` 不转发。
- Responses 推理续轮折叠必须显式设置渠道 `responsesReasoningFold: true`；工具兼容开关本身不会自动开启额外请求。

## 转换边界与数据修复

- additional_tools 默认原位透传；跨协议与显式工具兼容路径才提升声明。
- 同名 custom 工具按 namespace 精确回放，非流式、added/done/completed 保留 namespace；合法空 input 不省略。
- function 的显式 strict=false 在 Responses → Chat 转换中保留。
- previous_response_id 转换使用会话快照拼接历史，不修改持久历史，不把前一次 failover 的工具上下文带入原生重试。
- 媒体预算只遍历协议内容和工具结果区域，不扫描工具参数；文本附件按解码正文计数。
- [Gemini 音频](https://ai.google.dev/gemini-api/docs/audio)明确每秒 32 tokens；[Live API](https://ai.google.dev/gemini-api/docs/live-guide)明确 `audio/pcm;rate=16000` 为 16-bit PCM，预算据采样率和声道数计算。
- Kimi Code 的 k3/k3.1 系列与 Kimi API 的 kimi-k3 系列拆开匹配；会员别名不附加 API 单价，也不继承未在 Code 文档中公布的最大输出限制。
- 注册表只编辑源 JSON，再由生成脚本同步 Go、Web 与 Desktop。

## 验证结果

- 六协议请求透传回归、Messages/Responses 原始 SSE 与非流式 HTTP 回归通过；显式续轮开关的关闭/开启分别只发送 1/2 个上游请求。
- `go test ./...`、`go test -race ./...`、`go vet ./...` 和 Go 格式检查通过。
- 根目录 `make build`、Web/Desktop 类型检查与生产构建通过。
- Web 229 项、Desktop 97 项测试通过；本次 Web 修改文件 ESLint 无错误。
- Web 全量 ESLint 仍有 10 个未修改文件中的既有错误（图表类型、浏览器全局和多余空行），未计为本批验证通过。
- 验证使用隔离 HTTP 上游与测试路由，未停止或重启用户运行实例；运行实例需用户手动重启后加载新代码。
