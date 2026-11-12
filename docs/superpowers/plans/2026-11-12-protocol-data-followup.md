# 协议与模型数据缺口修复

基线：`3c8a1024`，分支 `codex/v3-lean-from-2.9.37`。

## 范围与顺序

1. 核验供应商定价和模型限制，修复 GLM 缓存价币种混用，重生成三端注册表。
2. 修复 Responses 工具历史、schema、密文参数的 token 估算与结构化透传。
3. 按媒体类型估算音频和附件，保护已有图片剥离与计数行为。
4. 归一化非标准 reasoning 形态，覆盖预检、fold、逐行中继、非流式和 compact。
5. 提升 additional_tools，支持 namespace/custom 工具转换与响应、历史还原。
6. compact 旁路统计携带真实模型，补持久化及归属回归。

## 实施约束

- 六协议渠道和显式模型映射维持 lean 语义，按行为移植，不整笔 cherry-pick。
- 保留已有工作区改动；不停止、重启任何运行实例，不写真实配置和密钥。
- 以已获取的供应商官方资料核验数值；资料不足时保留现值并记录待定项。
- 价格对象按币种整体处理，不跨币种补字段；历史估算仅作预算，不冒充计费精度。
- 原生 Responses 保留可用密文，转换到无对应字段的协议时显式采用兼容行为。
- 前端依赖已确认可用；分支与 main 有意分化，不做 ff-only 同步。

## 验证与交付

- 先添加最小失败用例，再修改实现；测试包含输入/输出往返和边界。
- 定价与模式：币种、精确模型匹配、生成一致性、Web/Desktop 类型检查与构建。
- token：长工具参数和输出、工具 schema、未知 item、媒体格式与畸形输入、避免重复计数。
- 流：标准流不改写、归一化幂等、thinking-only 预检、compact 不泄漏 reasoning。
- 工具：请求声明提升、非流式与流式响应 remap、多轮历史 replay、无工具请求保持兼容。
- 统计：模型随旁路成功/失败记录进入历史与持久化，渠道/币种维度不变。
- 最终运行后端全量测试、全量 race、go vet、make build 及两端前端检查。
- 按独立主题本地提交；回退使用对应提交的反向补丁，不重写历史。

## 官方资料核验（2026-11-12）

- [智谱 API 定价](https://docs.bigmodel.cn/cn/guide/start/pricing)：GLM-5.3 输入/缓存/输出为 CNY 8/2/28；Flash 为 0.8/0.23/2.8；FlashX/Legal 为 2/0.57/7，拆分匹配模式。
- [GPT-5.4](https://developers.openai.com/api/docs/models/gpt-5.4)、[GPT-5.5](https://developers.openai.com/api/docs/models/gpt-5.5)：总上下文 1,050,000；下游 agent 的保守窗口是独立配置语义。
- [Amazon Bedrock](https://developers.openai.com/api/docs/guides/amazon-bedrock)：GPT-5.6 系列总上下文 1,050,000。
- [Qwen3.8 Max](https://help.aliyun.com/zh/model-studio/qwen3-8-max)：总上下文 1,000,000；991,808 是最大输入长度。
- [Kimi API](https://platform.kimi.com/docs/api/responses)、[K3.1 Pro](https://platform.kimi.com/docs/guide/kimi-k3-1-pro-quickstart)：K3 系列默认输出 131,072，最大可设 1,048,576；两者分别注册。
- [Kimi 价格](https://platform.kimi.com/docs/pricing/chat)：动态页面由 TinyFish 只读提取，K3 的输入/缓存/输出为 CNY 20/2/100，K3.1 Pro 为 13/1.3/68，高速版为 26/1.3/136。
- [Kimi Code 模型](https://www.kimi.com/code/docs/kimi-code/models.html)：kimi-for-coding 当前为 K2.8 Preview，最高 1,048,576 上下文；移除误继承的 K2.7 API token 价格。
- [DeepSeek 价格](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/)：Flash/Pro 空闲费率分别为 CNY 1/0.02/4 和 4.5/0.15/13.5，高峰费率翻倍。
- [Step 定价](https://platform.stepfun.com/docs/zh/guides/pricing/details)：Step 3.7 Flash 输入/缓存/输出为 CNY 1.35/0.27/8.1。
- [Google token 文档](https://ai.google.dev/gemini-api/docs/tokens)：音频 32 tokens/s；[OpenAI 音频成本](https://developers.openai.com/api/docs/guides/voice-latency-cost)：输入音频约 10 tokens/s。本地路由估算采用 32 tokens/s。
- [Responses 输入项](https://developers.openai.com/api/reference/resources/responses/methods/create)：additional_tools 是有效输入类型，需处理已有 tools 与增量声明并存。
- [Responses 事件](https://developers.openai.com/api/reference/resources/responses/streaming-events)：reasoning_text 与 reasoning_summary_text 均是事件类型；summary 归一化属于客户端兼容处理。

## 验证结果与边界

- 初始工具历史复现由 8 tokens 修正为约 200,047 tokens；密文参数解析后保留。
- 四类上游的非流式/流式 HTTP 工具往返、namespace 扩展、历史重放和终帧还原已覆盖。
- 音频支持 WAV/FLAC/MP3/M4A/WebM 容器头解析，解析失败按格式码率近似；附件采用预算占位，不视为精确计费或严格上界。
- 密文无法跨协议解密，转换到 Chat/Claude/Gemini 仅以空 JSON 参数兼容历史；原生 Responses 保留密文。
- DeepSeek 分时费率和 Kimi TTL 缓存写入费用超出现有静态价格字段表达能力；本次注册标准输入/缓存读取/输出价格，并在 DeepSeek 描述保留空闲基准说明。
- 最终通过 make test、全量 go test -race ./...、go vet、两端构建、Web 229 与 Desktop 95 个前端测试，以及生成结果逐字节重现、两端 TS 数据一致性、格式与 ESLint 检查。
- 收尾修复旧时区测试使用过期固定样本造成的随机失败，改为保留期内的日期，10 次重复测试与 metrics race 均通过。
- 交付为四个主题提交：媒体/工具 token 与密文保留、工具与 reasoning 协议、compact 模型统计、供应商数据核验与记录。
