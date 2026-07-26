package autopilot

// ── 请求画像（设计 §3.5）──

// RequestProfile 是每次请求在进入调度器前生成的画像，不持久化。
// 聚合了请求特征（ClassifierInput）和分类结果（TaskClass/TaskDomain）。
type RequestProfile struct {
	// ── 来自请求（脱敏，对应 ClassifierInput）──
	Model       string         // 请求的目标模型
	ChannelKind string         // messages | chat | responses | gemini | images | vectors
	Operation   string         // completion | count_tokens | image_generation | image_edit | image_variation | embedding
	AgentRole   string         // "main" | "subagent" | ""
	AgentType   string         // "codex_subagent" | "claude_code_subagent" | ""
	HasImage    bool           // 是否包含图片
	EstTokens   int            // 估算输入 token 数（基于字符估算的保守上界）
	Complexity  TaskComplexity // 不含正文的任务难度信号

	// ── 路由能力下界 ──
	QualityNeed        QualityTier // 该模型对应的质量需求
	QualityTarget      QualityTier // 结合任务难度后的首选质量档
	ContextNeed        int         // 估算输入 token 数；输出上限由 scheduler 独立校验
	VisionNeed         bool        // 是否需要识图
	ImageGenNeed       bool        // 是否需要原生生图端点
	EmbeddingNeed      bool        // 是否需要原生 embedding 端点
	ToolUseNeed        bool        // 是否需要工具调用
	ReasoningNeed      bool        // 是否需要推理
	EmbeddingDimension int         // vectors handler 的硬约束；未知时为 0
	ClientEffort       EffortLevel // 客户端显式声明的思考等级；空=未声明
	ClientEffortExplicit bool      // 客户端是否显式设置了思考等级（区分"显式无"和"未声明"）

	// ── 任务分类结果 ──
	TaskClass  TaskClass  // 分类结果：supervisor | worker | lightweight | vision | long_context | image_generation | embedding
	TaskDomain TaskDomain // 域推导结果（由 InferTaskDomain 填充）

	// ── 人工意图匹配扩展（由 handler/main.go 层注入）──
	SessionID  string // 统一会话标识，用于 session_pin 匹配
	PromptHash string // prompt SHA256 前 16 位，用于确定性流量分配

	// ── AFP 路由扩展（可选）──
	AFPProfile *AFPRequestProfile
}

// ClassifierInput 是脱敏的请求特征集合，不含消息正文，用于确定性任务分类。
// 同一 ClassifierInput 永远产生同一 TaskClass。
type ClassifierInput struct {
	// ── 请求元数据 ──
	Model       string // 请求的目标模型名
	ChannelKind string // messages | chat | responses | gemini | images | vectors
	Operation   string // completion | count_tokens | image_generation | image_edit | image_variation | embedding
	AgentRole   string // "main" | "subagent" | ""
	AgentType   string // "codex_subagent" | "claude_code_subagent" | ""

	// ── 请求特征（脱敏）──
	HasImage   bool           // 是否包含图片内容
	EstTokens  int            // 估算输入 token 数（字符级估算，非精确计费）
	Complexity TaskComplexity // 请求入口提取的任务难度，不含正文

	// ── 路由能力下界 ──
	ContextNeed   int  // 估算输入 token 数（0 = 未知），与 scheduler 的输入窗口过滤语义一致
	VisionNeed    bool // 模型需要识图能力
	ImageGenNeed  bool // 需要原生生图端点
	EmbeddingNeed bool // 需要原生 embedding 端点
	ToolUseNeed   bool // 需要工具调用能力
	ReasoningNeed bool // 需要推理能力

	// ── 域推导输入（透传给 InferTaskDomain）──
	DomainHints DomainHints
}
