package guardrails

import (
	"regexp"
	"strings"
	"sync"
)

// CredentialMasker 实现凭据掩码 guardrail。
// 永远 block=false，只掩不拦；命中已知密钥格式即替换为 [MASKED:type]。
//
// 扫描字节上限 maxScanBytes（默认 256KB），超出只扫头部。
type CredentialMasker struct {
	enabled      bool
	priority     int
	maxScanBytes int

	mu        sync.RWMutex
	staticKeys []string // 网关自身密钥完整值（ProxyAccessKey/AdminAccessKey/ExtraKeys）
	keyPrefixes []string // 渠道 key 前缀（前 8 字符），用于指纹匹配
}

// NewCredentialMasker 创建默认配置的 credential-masker。
// priority 默认 95（与 OmniRoute 一致，低优先级即后执行、先看到完整内容）。
func NewCredentialMasker() *CredentialMasker {
	return &CredentialMasker{
		enabled:      true,
		priority:     95,
		maxScanBytes: 256 * 1024, // 256KB
	}
}

// SetEnabled 设置是否启用。
func (m *CredentialMasker) SetEnabled(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = v
}

// SetMaxScanBytes 设置单次扫描最大字节数。
func (m *CredentialMasker) SetMaxScanBytes(n int) {
	if n <= 0 {
		n = 256 * 1024
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxScanBytes = n
}

// SetStaticKeys 设置网关自身密钥列表（完整值）。
// 包括 ProxyAccessKey、AdminAccessKey、ExtraProxyAccessKeys。
func (m *CredentialMasker) SetStaticKeys(keys []string) {
	filtered := make([]string, 0, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" && len(k) >= 8 {
			filtered = append(filtered, k)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.staticKeys = filtered
}

// SetChannelKeyPrefixes 设置渠道 key 前缀指纹（前 8 字符）。
// 只取前缀避免把完整密钥存进 guardrail 内存，指纹命中后按同长度掩码。
func (m *CredentialMasker) SetChannelKeyPrefixes(prefixes []string) {
	filtered := make([]string, 0, len(prefixes))
	seen := make(map[string]struct{})
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if len(p) < 4 {
			continue
		}
		if len(p) > 12 {
			p = p[:8]
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		filtered = append(filtered, p)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keyPrefixes = filtered
}

func (m *CredentialMasker) Name() string     { return "credential-masker" }
func (m *CredentialMasker) Priority() int   { return m.priority }
func (m *CredentialMasker) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// PreCall 扫描请求体中的凭据并掩码。
func (m *CredentialMasker) PreCall(payload []byte, _ *Context) (*Result, error) {
	return m.mask(payload, false)
}

// PostCall 扫描响应/错误体中的凭据并掩码。
func (m *CredentialMasker) PostCall(response []byte, _ *Context) (*Result, error) {
	return m.mask(response, true)
}

func (m *CredentialMasker) mask(data []byte, isResponse bool) (*Result, error) {
	if len(data) == 0 {
		return nil, nil
	}

	m.mu.RLock()
	maxBytes := m.maxScanBytes
	trimmed := false
	if len(data) > maxBytes {
		data = data[:maxBytes]
		trimmed = true
	}
	staticKeys := m.staticKeys
	keyPrefixes := m.keyPrefixes
	m.mu.RUnlock()

	text := string(data)
	detections := make(map[string]int)
	result := text

	// 1. 精确匹配网关自身密钥
	for _, key := range staticKeys {
		if strings.Contains(result, key) {
			count := strings.Count(result, key)
			result = strings.ReplaceAll(result, key, "[MASKED:gateway_key]")
			detections["gateway_static"] += count
		}
	}

	// 2. 渠道 key 前缀指纹匹配
	//    匹配规则：前缀 + 至少 12 个非空白字符，整体掩码
	for _, prefix := range keyPrefixes {
		// 构造正则：前缀 + 连续非空白字符（至少 len(prefix) + 12）
		re := regexp.MustCompile(regexp.QuoteMeta(prefix) + `\S{12,}`)
		matches := re.FindAllString(result, -1)
		if len(matches) > 0 {
			for _, match := range matches {
				result = strings.ReplaceAll(result, match, "[MASKED:channel_key]")
			}
			detections["channel_prefix"] += len(matches)
		}
	}

	// 3. 通用密钥模式
	for _, pat := range genericPatterns {
		matches := pat.re.FindAllString(result, -1)
		if len(matches) > 0 {
			result = pat.re.ReplaceAllString(result, pat.replacement)
			detections[pat.name] += len(matches)
		}
	}

	if len(detections) == 0 {
		return nil, nil
	}

	totalCount := 0
	for _, n := range detections {
		totalCount += n
	}

	meta := map[string]any{
		"detections": detections,
		"count":      totalCount,
		"trimmed":    trimmed,
		"stage":      "pre",
	}
	if isResponse {
		meta["stage"] = "post"
	}

	return &Result{
		Blocked:  false,
		Modified: true,
		Message:  "credential-masker applied",
		Payload:  []byte(result),
		Response: []byte(result),
		Meta:     meta,
	}, nil
}

// credentialPattern 描述一个通用凭据匹配规则。
type credentialPattern struct {
	name        string
	re          *regexp.Regexp
	replacement string
}

// genericPatterns 是预定义的通用密钥/令牌模式。
// 优先匹配更具体的前缀，避免 sk- 太宽泛误杀。
var genericPatterns = []credentialPattern{
	// Anthropic / Claude
	{name: "anthropic", re: regexp.MustCompile(`sk-ant-api\d?-[\w-]{20,}`), replacement: "[MASKED:anthropic]"},
	{name: "anthropic_alt", re: regexp.MustCompile(`sk-ant-[\w-]{20,}`), replacement: "[MASKED:anthropic]"},
	// OpenAI
	{name: "openai_proj", re: regexp.MustCompile(`sk-proj-[\w-]{20,}`), replacement: "[MASKED:openai]"},
	{name: "openai", re: regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`), replacement: "[MASKED:openai]"},
	// Google
	{name: "google", re: regexp.MustCompile(`AIza[\w-]{35}`), replacement: "[MASKED:google]"},
	// GitHub
	{name: "github", re: regexp.MustCompile(`gh[pousr]_[\w-]{36,}`), replacement: "[MASKED:github]"},
	// Slack
	{name: "slack", re: regexp.MustCompile(`xox[bpoa]-[\w-]{10,}`), replacement: "[MASKED:slack]"},
	// Stripe
	{name: "stripe", re: regexp.MustCompile(`(?:sk|rk)_(?:live|test)_\w{24,}`), replacement: "[MASKED:stripe]"},
	// AWS
	{name: "aws_access_key", re: regexp.MustCompile(`AKIA[0-9A-Z]{16}`), replacement: "[MASKED:aws]"},
	// JWT
	{name: "jwt", re: regexp.MustCompile(`\beyJ[\w-]{10,}\.[\w-]{10,}\.[\w-]{10,}\b`), replacement: "[MASKED:jwt]"},
	// Bearer token 形态（跟在 Authorization: Bearer 后面的长 token）
	{
		name: "bearer_token",
		re:   regexp.MustCompile(`((?:Bearer|bearer)\s+)[A-Za-z0-9._~+/=-]{20,}`),
		replacement: "$1[MASKED:bearer_token]",
	},
	// 连接字符串
	{
		name: "connection_string",
		re:   regexp.MustCompile(`(?:mongodb(?:\+srv)?|postgres(?:ql)?|mysql|redis|amqp):\/\/[^\s:@"']+:[^\s:@"']+@`),
		replacement: "[MASKED:connection_string]",
	},
	// Private key PEM
	{
		name: "private_key",
		re:   regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`),
		replacement: "[MASKED:private_key]",
	},
}
