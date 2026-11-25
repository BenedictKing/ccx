# 协议网关与转换

> 状态：现行规格
> 代码基线：`53b79829`

## 1. 目标与范围

CCX 在同一监听端口提供六类代理入口，将客户端协议适配到所选渠道的上游协议，并保留状态码、响应头、流式事件和错误语义。

- 入口类型为 `messages`、`chat`、`responses`、`gemini`、`images`、`vectors`，六类配置、调度指标和日志相互隔离。
- 原生同协议默认透明转发；模型映射、显式参数覆盖、兼容开关、必要的数据修复或跨协议转换才允许改写请求。
- 大多数代理端点及 `/health` 同时支持 `/:routePrefix/...` 形式，路由前缀只参与候选过滤，不改变对外协议。
- 本文定义线上请求契约；渠道选择、重试预算、会话保存和观测口径分别见[调度规格](scheduling-and-resilience.md)、[会话规格](sessions-and-subagents.md)和[观测规格](observability-and-reporting.md)。

非目标：不把 Images/Vectors 模拟成文本聊天，不推断上游内部行为，不保证任意两个协议间所有扩展字段均可无损转换。

## 2. 协议与能力边界

| 对外入口  | 当前端点与行为                                                                                                                                                  |
| --------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Messages  | `POST /v1/messages`；支持 Claude 原生及跨协议流式/非流式请求。`POST /v1/messages/count_tokens` 在本地估算并返回 `input_tokens`，不调用上游。                    |
| Chat      | `POST /v1/chat/completions`；支持 OpenAI Chat 原生及与其他文本协议的转换。                                                                                      |
| Responses | `POST /v1/responses`；`GET` 同路径仅接受 WebSocket Upgrade。`POST /v1/responses/compact` 使用上游原生 compact，或在非 Responses 上游/不支持场景走本地压缩流程。 |
| Gemini    | `POST /v1beta/models/*modelAction`；按路径中的模型和 action 处理 `generateContent`、`streamGenerateContent` 等当前 handler 支持的原生操作。                     |
| Images    | `POST /v1/images/generations \| edits \| variations`；保留 JSON 或 multipart 语义，操作类型记录为 `generations`、`edits`、`variations`。                        |
| Vectors   | `POST /v1/embeddings`；要求非空 `model` 和合法 `input`，`dimensions` 若存在必须为正整数；只接受当前 OpenAI 兼容 serviceType。                                   |
| Models    | `GET /v1/models`、`GET /v1/models/:model`；按渠道聚合或查询模型信息，模型能力可包含上下文与输出上限。                                                           |

- 文本协议之间的转换由 provider/converter 执行；原生响应中的未知扩展字段和大整数不得因无条件 JSON 往返而丢失。
- Responses WebSocket 每个文本消息是独立的 `response.create` 逻辑请求；非文本或非法消息明确关闭。原生 WS、HTTP/SSE bridge 的选择取决于实际上游模型端点能力，不能把连接握手计为成功推理。
- Images 的 generations 必须含 `model`、`prompt`；edits/variations 缺省模型使用当前实现默认值。`convertImageUrlToB64Json` 仅在客户端请求 `b64_json` 且显式开启时下载并转换 URL。
- Vectors 校验响应为包含 `data[].embedding` 数组的 JSON；不同向量空间、维度或归一化语义不得因 fallback 混用。
- 所有代理入口使用代理访问密钥鉴权；`x-api-key`、`Authorization: Bearer` 和当前兼容的 query `key` 按中间件规则处理。

## 3. 请求与响应运行逻辑

1. 中间件完成请求日志、CORS、压缩和 Web 访问控制；代理 handler 再执行代理密钥认证。
2. handler 在配置的上限内读取原始请求体，解析模型、流式标记、会话和 Agent 上下文；请求体解析失败在进入调度前返回 400。
3. 调度器按当前配置快照选择渠道；每次渠道、URL、Key 尝试独立构造出站请求，不能复用上一次已改写的正文。
4. 原生路径默认使用原始字节。需要模型映射或兼容处理时使用请求副本；JSON 重写保留大整数精度，查询参数保留重复值和编码，CCX 的 `key` 鉴权参数不转发。
5. 上游请求清除 CCX 专用及转发头，设置上游认证，再应用渠道自定义头。Base URL 末尾 `#` 表示跳过自动 `/v1` 拼接的当前约定，具体端点构造由各 provider 负责。
6. 原生非流式响应保留上游状态码、Content-Type 和正文；原生 SSE 字节透传，解析仅用于预检、指标、会话及观测，不默认合成终态或补 usage。
7. 跨协议响应经转换器生成目标协议事件与最终响应；首字节提交后持续监控流活动并记录真实终态。非原生的 Responses 非流式响应在 usage 缺失或明显偏低时，按 Responses item 类型本地估算输出 token，覆盖推理摘要和函数调用参数。

- 客户端明确发送单一 `Accept-Encoding: identity` 时，HTTP 上游请求保留该禁用响应压缩意图；其他编码协商头仍移除，非流式由 Go transport 协商和解压 gzip，流式保持 transport 禁用自动压缩的原有行为。渠道自定义头继续按显式配置覆盖，客户端请求头不被原地修改。
- 转换后的 Responses 客户端出口 `input_tokens` 包含缓存，`total_tokens = input_tokens + output_tokens`；内部指标仍使用不含缓存的输入计数。低质量模式不覆盖有缓存信号的输入计数，本地估算不等同于上游模型 tokenizer 或 llama.cpp 的 `n_tokens`。
- 原生 OpenAI Responses 的低质量 usage 修补保留包含缓存的输入口径，重新计算 `total_tokens` 时不得重复加缓存；转换路径在完成内部修补后仅在出口加回一次缓存。

Responses 特殊流程：

- `previous_response_id` 在原生路径保留；需要转换时使用会话快照重建必要历史，不能把其他尝试的工具上下文混入。
- `responsesReasoningFold` 默认 false，只有显式开启才允许推理续轮折叠触发额外上游请求。开启时正文与工具调用缓冲到确定不再续接后下发；未开启时，即使因 Codex 工具兼容/原生透传进入同一处理流程，正文也必须实时转发，仅需还原 Codex 形态的工具调用缓冲到该项完成。
- 本地 compact 从会话历史构造压缩输入，有长度截断与最近 reasoning 保留策略；`compactModel` 是实际上游模型且不再经过普通 `modelMapping`。
- Copilot 按映射后的实际模型及其 `supported_endpoints` 选择 HTTP 或 WS；缺元数据时只保留带诊断的历史默认，不声称已验证。

## 4. 状态、异常与重试边界

| 场景                                             | 必须行为                                                               |
| ------------------------------------------------ | ---------------------------------------------------------------------- |
| 鉴权失败、请求过大、JSON/multipart 非法          | 在选渠前终止，不产生上游尝试或渠道熔断样本。                           |
| 无合格渠道、无可用 Key                           | 返回明确的无可用上游错误；候选过滤不伪造一次上游调用。                 |
| 连接失败、可重试状态码、空响应、可识别的畸形响应 | 按故障分类在剩余 URL/Key/渠道和请求预算内 failover；保留每次尝试诊断。 |
| 鉴权/权限/余额/模型缺失                          | 按 Key、Key×模型或渠道粒度处理，不能把所有 4xx 都当作可重试过载。      |
| 流式首内容或活动超时                             | 响应尚未提交时可换尝试；已经向客户端提交后不得透明重放另一上游流。     |
| 客户端取消或写回失败                             | 终止当前请求及可取消的上游读取，记录取消而非成功，不继续无界重试。     |
| 所有候选失败                                     | 返回最后有效错误及适当状态码；不得用空 200 掩盖失败。                  |

- 一次请求共享总尝试次数和重试时间预算；同渠换 Key/URL 与跨渠道都受约束。已经交付正常长流后，预算不强制截断客户端响应。
- 渠道发现、能力测试、ping 和 compact 辅助调用具有独立 `requestSource`/调用角色，不进入默认正式代理分布。
- Images URL 转 base64 失败属于响应处理失败，不得返回半转换结果；下载限制为 30 秒、25 MiB，且校验 HTTP 状态和图片内容。

## 5. 安全、兼容与持久化约束

- CCX 代理密钥、管理密钥和上游 Key 是三种独立凭证；上游请求不得转发 CCX 的代理鉴权参数或 `x-proxy-key`。
- 自定义头可以按渠道覆盖上游请求头，属于显式受信配置；管理 API、日志和观测响应不得因此泄露完整 Key 或 Authorization。
- 请求正文不写入观测记录；开发环境的详细请求/响应日志可能包含敏感内容，生产环境默认保持响应日志关闭。
- `insecureSkipVerify`、图片远程下载和自定义 Base URL 都是显式网络边界；图片下载只允许 HTTP/HTTPS 或受支持的 data URL，并限制大小、超时、状态和内容类型。
- 代理转发本身不持久化提示词和响应。会话、thinking、路由观测和聚合指标分别使用各自最小数据集与保留期。
- 新增协议字段时，原生路径默认保留；转换路径必须补字段级矩阵和流式/非流式测试，不能以“未知字段被丢弃”作为隐式兼容策略。
- 配置热重载不得改变已固定请求快照的重试语义；协议升级不得把现有显式兼容开关自动改成开启。

## 6. 验收标准

| 场景                      | 必须结果                                                                                 |
| ------------------------- | ---------------------------------------------------------------------------------------- |
| 六类原生 HTTP 请求        | 未启用改写时上游收到原始正文；响应状态、Content-Type 和扩展字段保留。                    |
| 原生 SSE 与跨协议流       | 原生字节不被规范化；转换流事件顺序、工具 ID、终态和错误符合目标协议。                    |
| 大整数与未知字段          | 需要 JSON 重写时不损失整数精度；原生未知字段不丢弃。                                     |
| Responses HTTP/WS/compact | 每轮独立关联请求与终态；续轮字段保留；本地/上游 compact 分支及辅助调用口径正确。         |
| 首内容前失败与提交后断流  | 前者可按预算 failover，后者不拼接其他上游响应；取消和失败终态准确。                      |
| Images JSON/multipart     | 操作路径、模型映射、二进制 part 和 Content-Type 保留；URL 转 base64 遵守大小与安全限制。 |
| Vectors 请求与响应        | 非法 model/input/dimensions 返回 400；合法 fallback 不混向量空间，非法响应不当作成功。   |
| 鉴权与查询参数            | CCX key 不泄露到上游，重复查询参数保留；非法代理认证在选渠前失败。                       |
| 禁用响应压缩              | Messages HTTP/SSE 的假上游实际收到 `identity`；默认 HTTP 协商 gzip、SSE 不自动协商，客户端收到可读响应。 |
| Chat 转 Responses 缓存用量 | 多轮 HTTP/SSE 的客户端用量包含缓存；原生 Responses 不重复加缓存，内部统计口径保持一致。 |

验收使用本地假上游覆盖 HTTP、SSE、WS 和 multipart，至少核对上游实际收到的字节/模型与客户端实际收到的事件；不只验证 handler 返回 200。

## 7. 实现与测试索引

- 路由注册：`backend-go/main.go`。
- 公共重试与流处理：`backend-go/internal/handlers/common/`。
- 原生字节契约：`messages/native_passthrough_test.go`、`chat/native_passthrough_test.go`、`responses/native_passthrough_test.go`、`gemini/native_passthrough_test.go`。
- Responses WS 与 compact：`websocket.go`、`compact.go`、`compact_local.go`及同目录测试。
- Images/Vectors：`images/handler.go`、`vectors/handler.go`及各自 `native_passthrough_test.go`、`handler_test.go`。
- 响应与错误矩阵：`messages`、`chat`、`responses`、`gemini` 四个目录的 `handler_response_matrix_test.go`，公共 `upstream_rejected_response_test.go`、`stream_data_error_test.go`、`client_error_test.go`。
- 压缩协商：`utils/headers.go`、`utils/headers_test.go`、`messages/native_passthrough_test.go`；用量口径：`responses/stream.go`、`responses/usage.go`、`responses/usage_context_regression_test.go`。后者覆盖本地假上游的三轮高缓存 HTTP/SSE、低质量开关及重复 developer 消息；#263 的 llama.cpp 请求样本与 Codex App 实测仍待验证。
