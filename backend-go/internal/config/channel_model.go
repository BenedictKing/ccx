package config

// channel_model.go 定义"渠道 → key → (baseURL,协议) endpoint → 模型"粒度的只读视图模型。
//
// 背景：CCX 权威存储仍是六个 Upstream* 数组（每个 UpstreamConfig 只承载一种协议）。
// 本文件提供在其之上合成的读模型（ChannelView 家族），把同一物理站点/账号的多协议渠道
// 收敛为单一渠道视图，并把"协议+模型认知"抽象为可跨账号共享的 EndpointCapability。
//
// 设计边界（见 docs/specs/channel-data-model-v2.md）：
//   - 账号边界：AccountUID / AccessToken / 余额 / key 归属，绝不跨账号共享。
//   - 能力边界：(SiteIdentity, GroupIdentity, IdentityBaseURL, Protocol)，同站点同分组共享。
//
// 本文件是纯读模型 + 身份工具，不改 JSON schema，不参与持久化。

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/BenedictKing/ccx/internal/utils"
)

// ChannelSchemaVersion 是 Channels 镜像结构的 schema 版本。
const ChannelSchemaVersion = 1

// ChannelView 是一个物理站点/账号的聚合视图，收敛同账号/同逻辑渠道的多协议物理渠道。
type ChannelView struct {
	ChannelUID   string                `json:"channelUid"`             // 聚合主键：优先 LogicalChannelUID，否则首个物理 ChannelUID
	AccountUID   string                `json:"accountUid,omitempty"`   // 账号身份（可空）
	ProviderID   string                `json:"providerId,omitempty"`   // provider 模板来源（可空）
	Name         string                `json:"name,omitempty"`         // 用户可见名（取逻辑名或首个非派生物理名）
	Remark       string                `json:"remark,omitempty"`       // 旧派生规则写下的历史名，新规则下不再使用；只读展示用
	SiteIdentity string                `json:"siteIdentity,omitempty"` // 归一化站点身份
	BaseURLs     []string              `json:"baseUrls,omitempty"`     // 站点级 baseURL 并集
	Status       string                `json:"status,omitempty"`       // 聚合状态：active / partial / suspended / disabled
	Keys         []ChannelKeyView      `json:"keys,omitempty"`         // 该渠道下所有凭证
	Protocols    []ProtocolFacadeView  `json:"protocols,omitempty"`    // CCX 入口协议 facade
	Account      *NewApiAccountView    `json:"account,omitempty"`      // 仅 new-api 渠道非空
	MemberRoutes []ChannelRouteRefView `json:"memberRoutes,omitempty"` // 组成该视图的物理路由引用
}

// ChannelRouteRefView 标识一个物理渠道在六数组中的位置（读模型，不可变）。
type ChannelRouteRefView struct {
	Kind       string `json:"kind"`       // messages | chat | responses | gemini | images | vectors
	Index      int    `json:"index"`      // 在对应 Upstream* 数组中的下标
	ChannelUID string `json:"channelUid"` // 物理 UpstreamConfig.ChannelUID
}

// ChannelKeyView 是渠道下的一个凭证视图。凭证状态（auth/健康/配额）按 key 隔离，
// 但每个 endpoint binding 引用可跨账号共享的 EndpointCapability。
type ChannelKeyView struct {
	KeyUID                 string                   `json:"keyUid,omitempty"`
	CredentialUID          string                   `json:"credentialUid,omitempty"`
	KeyMask                string                   `json:"keyMask,omitempty"`
	KeyHash                string                   `json:"keyHash,omitempty"`
	AccountUID             string                   `json:"accountUid,omitempty"`
	QuotaGroup             string                   `json:"quotaGroup,omitempty"`
	GroupIdentity          string                   `json:"groupIdentity,omitempty"`
	Enabled                bool                     `json:"enabled"`
	Weight                 int                      `json:"weight,omitempty"`
	RateLimitRPM           int                      `json:"rateLimitRpm,omitempty"`
	RateLimitMaxConcurrent int                      `json:"rateLimitMaxConcurrent,omitempty"`
	Endpoints              []KeyEndpointBindingView `json:"endpoints,omitempty"`
}

// KeyEndpointBindingView 把一个 key 绑定到一份共享能力，不复制模型清单。
type KeyEndpointBindingView struct {
	CapabilityUID   string `json:"capabilityUid"`
	Protocol        string `json:"protocol,omitempty"`
	IdentityBaseURL string `json:"identityBaseUrl,omitempty"`
	Enabled         bool   `json:"enabled"`
}

// ProtocolFacadeView 是 CCX 入口协议 facade（一个 kind 对应一个物理渠道）。
type ProtocolFacadeView struct {
	Kind        string `json:"kind"`
	ServiceType string `json:"serviceType,omitempty"`
	ChannelUID  string `json:"channelUid,omitempty"`
	Index       int    `json:"index"`
	Enabled     bool   `json:"enabled"`
	Status      string `json:"status,omitempty"`
	Priority    int    `json:"priority,omitempty"`
	RoutePrefix string `json:"routePrefix,omitempty"`
}

// NewApiAccountView 是 new-api 渠道的账号侧信息（凭证敏感字段不回显）。
type NewApiAccountView struct {
	AccountUID      string   `json:"accountUid,omitempty"`
	SubscriptionUID string   `json:"subscriptionUid,omitempty"`
	UserID          string   `json:"userId,omitempty"`
	DisplayName     string   `json:"displayName,omitempty"`
	Status          string   `json:"status,omitempty"`
	KeyUIDs         []string `json:"keyUids,omitempty"`
}

// EndpointCapability 表示同站点同分组共享的协议+模型认知。跨账号可共享。
type EndpointCapability struct {
	CapabilityUID   string   `json:"capabilityUid"`
	SiteIdentity    string   `json:"siteIdentity,omitempty"`
	GroupIdentity   string   `json:"groupIdentity,omitempty"`
	GroupName       string   `json:"groupName,omitempty"`
	BaseURL         string   `json:"baseUrl,omitempty"`
	IdentityBaseURL string   `json:"identityBaseUrl,omitempty"`
	Protocol        string   `json:"protocol,omitempty"`
	ServiceType     string   `json:"serviceType,omitempty"`
	Models          []string `json:"models,omitempty"`
	Source          string   `json:"source,omitempty"`
}

// NormalizeGroupIdentity 把分组名归一化为跨账号可比的能力身份：trim + 小写。
// 已确认设计假设：同站点同名分组视为同能力。空分组归一化为空串（表示未分组，按默认能力）。
func NormalizeGroupIdentity(groupName string) string {
	return strings.ToLower(strings.TrimSpace(groupName))
}

// SiteIdentityForBaseURL 返回 baseURL 的归一化站点身份（跨协议稳定）。
// 复用 utils.BaseURLSiteIdentities 的首个身份，与逻辑渠道归组口径一致。
func SiteIdentityForBaseURL(baseURL string) string {
	ids := utils.BaseURLSiteIdentities(baseURL)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// GenerateCapabilityUID 生成同站点同分组能力的稳定标识。
// 键 = (siteIdentity, groupIdentity, identityBaseURL, protocol)，跨账号共享。
func GenerateCapabilityUID(siteIdentity, groupIdentity, identityBaseURL, protocol string) string {
	seed := strings.Join([]string{
		strings.TrimSpace(siteIdentity),
		NormalizeGroupIdentity(groupIdentity),
		strings.TrimSpace(identityBaseURL),
		strings.ToLower(strings.TrimSpace(protocol)),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "cap_" + hex.EncodeToString(sum[:])[:16]
}

// ChannelKeyHash 返回明文 key 的单向哈希（前 16 位十六进制），用作凭证状态归并键。
// 与 autopilot.KeyHashFromAPIKey / metrics.ModelCircuitKeyHash 同算法，保证跨模块一致。
func ChannelKeyHash(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])[:16]
}

// ProtocolForChannelKind 把六数组的 channelKind 映射为 endpoint 协议名（当前一一对应）。
func ProtocolForChannelKind(kind string) string {
	return strings.ToLower(strings.TrimSpace(kind))
}

// ServiceTypeForChannelKind 返回该 kind 下 UpstreamConfig.ServiceType 缺失时的兜底 serviceType。
func ServiceTypeForChannelKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "messages":
		return "claude"
	case "responses":
		return "responses"
	case "gemini":
		return "gemini"
	default: // chat / images / vectors
		return "openai"
	}
}
