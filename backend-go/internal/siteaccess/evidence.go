package siteaccess

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// 渠道验收的证据等级：配置已保存 → 管理面信息已验证 → 真实请求成功。
// 等级只升不降：后续探针失败只记录诊断，不抹掉已有的更强证据；
// 任何验证失败都不会据此禁用 Key（禁用只发生在对账确认远端删除时）。
const (
	EvidenceConfigured   = "configured"
	EvidenceInfoVerified = "info_verified"
	EvidenceRealRequest  = "real_request"
)

// ChannelEvidence 记录单个协议+分组渠道的验收证据。
type ChannelEvidence struct {
	Protocol string `json:"protocol"`
	Group    string `json:"group"`
	Level    string `json:"level"`

	ConfiguredAt   *time.Time `json:"configuredAt,omitempty"`
	InfoVerifiedAt *time.Time `json:"infoVerifiedAt,omitempty"`
	RealRequestAt  *time.Time `json:"realRequestAt,omitempty"`

	// LastDiagnostic 是最近一次验证失败的摘要（定位凭证/分组/模型/协议问题），
	// 仅用于展示诊断，不代表渠道不可用。
	LastDiagnostic string `json:"lastDiagnostic,omitempty"`
}

func evidenceLevelRank(level string) int {
	switch level {
	case EvidenceRealRequest:
		return 3
	case EvidenceInfoVerified:
		return 2
	case EvidenceConfigured:
		return 1
	default:
		return 0
	}
}

// evidenceFor 找到或新建指定协议+分组的证据记录。
func (a *SiteAccount) evidenceFor(protocol, group string) *ChannelEvidence {
	for i := range a.Evidence {
		if a.Evidence[i].Protocol == protocol && a.Evidence[i].Group == group {
			return &a.Evidence[i]
		}
	}
	a.Evidence = append(a.Evidence, ChannelEvidence{Protocol: protocol, Group: group})
	return &a.Evidence[len(a.Evidence)-1]
}

// RecordConfigured 记录「配置已保存」证据（provision 成功时调用）。
func RecordConfigured(store *AccountStore, accountUID, protocol, group string) {
	mutateAccountEvidence(store, accountUID, protocol, group, func(e *ChannelEvidence) {
		now := time.Now()
		e.ConfiguredAt = &now
		if evidenceLevelRank(e.Level) < 1 {
			e.Level = EvidenceConfigured
		}
	})
}

// RecordInfoVerified 管理面信息验证结果：成功升级证据等级；
// 失败只写诊断，不降级、不禁用（探针失败 ≠ 渠道不可用）。
func RecordInfoVerified(store *AccountStore, accountUID, protocol, group string, verifyErr error) {
	mutateAccountEvidence(store, accountUID, protocol, group, func(e *ChannelEvidence) {
		if verifyErr != nil {
			e.LastDiagnostic = verifyErr.Error()
			return
		}
		now := time.Now()
		e.InfoVerifiedAt = &now
		e.LastDiagnostic = ""
		if evidenceLevelRank(e.Level) < 2 {
			e.Level = EvidenceInfoVerified
		}
	})
}

// RecordRealRequest 真实推理结果：成功升级为最强证据；失败只写诊断。
// 真实业务流量与主动验证共用此入口。
func RecordRealRequest(store *AccountStore, accountUID, protocol, group string, success bool, diagnostic string) {
	mutateAccountEvidence(store, accountUID, protocol, group, func(e *ChannelEvidence) {
		if !success {
			e.LastDiagnostic = diagnostic
			return
		}
		now := time.Now()
		e.RealRequestAt = &now
		e.LastDiagnostic = ""
		e.Level = EvidenceRealRequest
	})
}

func mutateAccountEvidence(store *AccountStore, accountUID, protocol, group string, fn func(*ChannelEvidence)) {
	account, found, err := store.Get(accountUID)
	if err != nil || !found {
		return
	}
	fn(account.evidenceFor(protocol, group))
	_, _ = store.Upsert(account)
}

var defaultStore *AccountStore

// SetDefaultStore 设置真实流量证据回填使用的默认账号存储（启动时注入一次）。
func SetDefaultStore(store *AccountStore) {
	defaultStore = store
}

// RecordRealTrafficByChannelName 从真实业务流量回填证据：按托管渠道命名约定
// 定位账号与协议+分组。非托管渠道（不匹配任何账号）时不做任何事。
// 真实流量的成功/失败是渠道可用性的最强证据；失败只写诊断，不驱动禁用。
func RecordRealTrafficByChannelName(channelName string, success bool, diagnostic string) {
	store := defaultStore
	if store == nil || !strings.HasPrefix(channelName, "ccx: ") {
		return
	}
	accounts, err := store.List()
	if err != nil {
		return
	}
	for _, account := range accounts {
		for _, meta := range account.ProvisionedKeys {
			if ProvisionedChannelName(account.BaseURL, meta.Protocol, meta.Group) == channelName {
				RecordRealRequest(store, account.UID, meta.Protocol, meta.Group, success, diagnostic)
				return
			}
		}
	}
}

// VerifyAccountInfo 用户主动触发的管理面信息验证：校验令牌 + 逐渠道读取分组模型。
// 任何失败只写诊断；成功时升级对应渠道证据到 info_verified。
func VerifyAccountInfo(ctx context.Context, store *AccountStore, adapter *NewApiAdapter, accountUID string) error {
	account, found, err := store.Get(accountUID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("[SiteAccess-Verify] 账号不存在: %s", accountUID)
	}
	accessToken, err := account.plaintextAccessToken()
	if err != nil {
		return err
	}

	self, derivedUserID, err := adapter.VerifyWithFallback(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode)
	if err != nil {
		for i := range account.Evidence {
			account.Evidence[i].LastDiagnostic = fmt.Sprintf("令牌校验失败: %v", err)
		}
		_, _ = store.Upsert(account)
		return err
	}

	// fork 回退派生出的 userID 写回账号，后续 provision/对账复用
	if account.UserID != derivedUserID {
		account.UserID = derivedUserID
		_, _ = store.Upsert(account)
	}
	_ = self

	for _, meta := range account.ProvisionedKeys {
		// 分组被撤销时 new-api 会返回空模型或错误：仅记录诊断，不自动禁用
		_, err := adapter.FetchGroupModels(ctx, account.BaseURL, accessToken, derivedUserID, account.AuthTokenMode, meta.Group)
		RecordInfoVerified(store, accountUID, meta.Protocol, meta.Group, err)
	}
	return nil
}

// VerifyRealRequest 用户主动触发的真实推理验证：用渠道现有 Key 发一次最小请求。
// 成功/失败只影响证据与诊断，不修改渠道状态；失败由用户决定是否继续 failover。
func VerifyRealRequest(ctx context.Context, store *AccountStore, cfgManager *config.ConfigManager, accountUID, protocol, group, model string) error {
	account, found, err := store.Get(accountUID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("[SiteAccess-Verify] 账号不存在: %s", accountUID)
	}
	apiType, _, err := provisionProtocolInfo(protocol)
	if err != nil {
		return err
	}
	channelName := ProvisionedChannelName(account.BaseURL, strings.ToLower(protocol), group)
	channels := protocolChannels(cfgManager, apiType)
	var channel *config.UpstreamConfig
	for i := range channels {
		if channels[i].Name == channelName {
			channel = &channels[i]
			break
		}
	}
	if channel == nil {
		err := fmt.Errorf("渠道不存在: %s", channelName)
		RecordRealRequest(store, accountUID, strings.ToLower(protocol), group, false, err.Error())
		return err
	}
	if len(channel.APIKeys) == 0 {
		err := fmt.Errorf("渠道无可用 Key（可能已被对账停用，可重新接入恢复）")
		RecordRealRequest(store, accountUID, strings.ToLower(protocol), group, false, err.Error())
		return err
	}
	if strings.TrimSpace(model) == "" {
		err := fmt.Errorf("真实请求验证必须显式指定模型")
		return err
	}

	verifyErr := doMinimalInferenceRequest(ctx, *channel, strings.ToLower(protocol), model)
	RecordRealRequest(store, accountUID, strings.ToLower(protocol), group, verifyErr == nil, diagnosticOf(verifyErr))
	return verifyErr
}

func diagnosticOf(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
