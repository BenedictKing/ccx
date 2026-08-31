package autopilot

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
)

const (
	volcengineManagementHost = "ark.cn-beijing.volcengineapi.com"
	volcengineOpenAPIHost    = "open.volcengineapi.com"
	volcengineRegion         = "cn-beijing"
	volcengineAPIVersion     = "2024-01-01"
	volcengineContentType    = "application/json; charset=UTF-8"
	// 火山方舟管控面示例要求只对这三个请求头签名。Content-Type 仍会发送，
	// 但不能列入 SignedHeaders，否则套餐模型列表接口会拒绝签名。
	volcengineSignedHeaders   = "host;x-content-sha256;x-date"
	volcenginePlanAgent       = "agent_plan"
	volcenginePlanCoding      = "coding_plan"
	volcengineEditionPersonal = "personal"
	volcengineEditionTeam     = "team"
	// GetSeatInfo 的 Scene 参数：Agent Plan 企业版必须用 agent_plan_enterprise，
	// Coding Plan 企业版用空字符串（早期误用 agent_plan/coding_plan 会返空 SeatID 不报错）。
	volcengineAgentPlanEnterpriseScene = "agent_plan_enterprise"
)

type volcenginePlanInfo struct {
	Plan   string
	Tier   string
	Status string
}

type volcenginePlanClient struct {
	Endpoint   string
	HTTPClient *http.Client
	Now        func() time.Time
}

type volcengineResponse struct {
	ResponseMetadata struct {
		Error *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error,omitempty"`
	} `json:"ResponseMetadata"`
	Result struct {
		PlanType string `json:"PlanType"`
		Status   string `json:"Status"`
		Datas    []struct {
			ModelID string `json:"ModelID"`
		} `json:"Datas"`
		// Agent Plan GetAFPUsage 用量窗口。
		AFPFiveHour *volcengineAFPWindow `json:"AFPFiveHour,omitempty"`
		AFPDaily    *volcengineAFPWindow `json:"AFPDaily,omitempty"`
		AFPWeekly   *volcengineAFPWindow `json:"AFPWeekly,omitempty"`
		AFPMonthly  *volcengineAFPWindow `json:"AFPMonthly,omitempty"`
		// Coding Plan GetCodingPlanUsage 用量窗口。
		QuotaUsage []volcengineCodingPlanQuota `json:"QuotaUsage,omitempty"`
	} `json:"Result"`
}

// volcengineAFPWindow 是 Agent Plan AFP 单窗口用量。
type volcengineAFPWindow struct {
	Quota     float64 `json:"Quota"`
	Used      float64 `json:"Used"`
	ResetTime int64   `json:"ResetTime"`
}

// volcengineCodingPlanQuota 是 Coding Plan 单个用量窗口（仅返回已用百分比）。
type volcengineCodingPlanQuota struct {
	Level          string  `json:"Level"`
	Percent        float64 `json:"Percent"`
	ResetTimestamp int64   `json:"ResetTimestamp"`
}

type volcengineAPIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *volcengineAPIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("火山管控面错误 %s（HTTP %d）: %s", e.Code, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("火山管控面返回 HTTP %d: %s", e.StatusCode, e.Message)
}

// DetectPlan 探测套餐并返回主桶信息（DetectPlans 的兼容包装，存量调用方与行为不变）。
func (c *volcenginePlanClient) DetectPlan(ctx context.Context, pair *config.VolcengineAccessKeyPair, hint string) (volcenginePlanInfo, error) {
	_, primary, err := c.DetectPlans(ctx, pair, hint)
	return primary, err
}

// DetectPlans 探测该 AK/SK 身份下的全部套餐桶：personal×2 走 GetPersonalPlan，
// team×2 走 GetSeatInfo 席位绑定（企业版不在 GetPersonalPlan 暴露）。
// 个人版保持既有语义——鉴权/网络错误上抛、未订阅(404)静默；团队版探测失败只记桶级
// Error 不阻断其它桶；席位未绑定时静默不出桶。返回全部桶与选定的主桶
// （hint 匹配 > personal 优先 > 唯一 Running > 唯一可用）。
func (c *volcenginePlanClient) DetectPlans(ctx context.Context, pair *config.VolcengineAccessKeyPair, hint string) ([]config.VolcenginePlanBucket, volcenginePlanInfo, error) {
	buckets := make([]config.VolcenginePlanBucket, 0, 4)
	for _, plan := range []string{volcenginePlanAgent, volcenginePlanCoding} {
		info, err := c.getPersonalPlan(ctx, pair, plan)
		if err != nil {
			if apiErr, ok := err.(*volcengineAPIError); ok && apiErr.StatusCode == http.StatusNotFound {
				continue
			}
			return nil, volcenginePlanInfo{}, err
		}
		buckets = append(buckets, config.VolcenginePlanBucket{Product: plan, Edition: volcengineEditionPersonal, Tier: info.Tier, Status: info.Status})
	}
	for _, plan := range []string{volcenginePlanAgent, volcenginePlanCoding} {
		scene := ""
		if plan == volcenginePlanAgent {
			scene = volcengineAgentPlanEnterpriseScene
		}
		bucket := config.VolcenginePlanBucket{Product: plan, Edition: volcengineEditionTeam}
		seatID, err := c.getTeamSeat(ctx, pair, scene)
		switch {
		case err == nil && seatID != "":
			// 席位绑定即视为生效订阅（GetSeatInfo 不区分未订与未分配席位）。
			bucket.SeatID = seatID
			bucket.Status = "Running"
		case err == nil:
			continue
		default:
			bucket.Error = err.Error()
		}
		buckets = append(buckets, bucket)
	}
	primary, err := pickVolcenginePrimaryBucket(buckets, hint)
	if err != nil {
		return buckets, volcenginePlanInfo{}, err
	}
	return buckets, primary, nil
}

// pickVolcenginePrimaryBucket 从套餐桶中选主桶（写入 Plan/Usage 兼容字段，供
// 模型清单、稀疏 L2 预算、恢复等既有消费链使用）。个人版优先于团队版，
// 同 product 双 edition 并存时选个人版，保持存量行为。
func pickVolcenginePrimaryBucket(buckets []config.VolcenginePlanBucket, hint string) (volcenginePlanInfo, error) {
	var usable []config.VolcenginePlanBucket
	for _, bucket := range buckets {
		if bucket.Error == "" {
			usable = append(usable, bucket)
		}
	}
	if len(usable) == 0 {
		return volcenginePlanInfo{}, fmt.Errorf("access Key 所属账号未查询到 Agent Plan 或 Coding Plan，请确认 AK/SK 与推理 Key 属于同一账号")
	}
	pick := func(list []config.VolcenginePlanBucket) (volcenginePlanInfo, bool) {
		switch len(list) {
		case 0:
			return volcenginePlanInfo{}, false
		case 1:
			return volcenginePlanInfo{Plan: list[0].Product, Tier: list[0].Tier, Status: list[0].Status}, true
		}
		running := 0
		var only volcenginePlanInfo
		for _, bucket := range list {
			if strings.EqualFold(bucket.Status, "Running") {
				running++
				only = volcenginePlanInfo{Plan: bucket.Product, Tier: bucket.Tier, Status: bucket.Status}
			}
		}
		if running == 1 {
			return only, true
		}
		return volcenginePlanInfo{}, false
	}
	var personal, team []config.VolcenginePlanBucket
	for _, bucket := range usable {
		if bucket.Edition == volcengineEditionTeam {
			team = append(team, bucket)
		} else {
			personal = append(personal, bucket)
		}
	}
	if hint = normalizeVolcenginePlan(hint); hint != "" {
		for _, group := range [][]config.VolcenginePlanBucket{personal, team} {
			for _, bucket := range group {
				if bucket.Product == hint {
					return volcenginePlanInfo{Plan: bucket.Product, Tier: bucket.Tier, Status: bucket.Status}, nil
				}
			}
		}
	}
	if info, ok := pick(personal); ok {
		return info, nil
	}
	if info, ok := pick(team); ok {
		return info, nil
	}
	if len(personal) > 1 {
		return volcenginePlanInfo{}, fmt.Errorf("该账号同时存在 Agent Plan 与 Coding Plan，且无法从推理 Key 的数据面地址消歧")
	}
	first := usable[0]
	return volcenginePlanInfo{Plan: first.Product, Tier: first.Tier, Status: first.Status}, nil
}

// primaryUsageFromBuckets 返回主套餐（product 匹配，personal 优先）的用量快照。
func primaryUsageFromBuckets(buckets []config.VolcenginePlanBucket, plan string) *config.VolcenginePlanUsage {
	plan = normalizeVolcenginePlan(plan)
	for _, edition := range []string{volcengineEditionPersonal, volcengineEditionTeam} {
		for _, bucket := range buckets {
			if bucket.Product == plan && bucket.Edition == edition && bucket.Usage != nil {
				return bucket.Usage
			}
		}
	}
	return nil
}

func (c *volcenginePlanClient) getPersonalPlan(ctx context.Context, pair *config.VolcengineAccessKeyPair, plan string) (volcenginePlanInfo, error) {
	apiPlan := "AgentPlan"
	if normalizeVolcenginePlan(plan) == volcenginePlanCoding {
		apiPlan = "CodingPlan"
		plan = volcenginePlanCoding
	} else {
		plan = volcenginePlanAgent
	}
	var decoded volcengineResponse
	if err := c.doAction(ctx, pair, "GetPersonalPlan", "ark", map[string]string{"Plan": apiPlan}, &decoded); err != nil {
		return volcenginePlanInfo{}, err
	}
	return volcenginePlanInfo{Plan: plan, Tier: strings.TrimSpace(decoded.Result.PlanType), Status: strings.TrimSpace(decoded.Result.Status)}, nil
}

func (c *volcenginePlanClient) FetchModels(ctx context.Context, pair *config.VolcengineAccessKeyPair, plan string) ([]string, error) {
	action := "ListArkAgentPlanModel"
	plan = normalizeVolcenginePlan(plan)
	if plan == volcenginePlanCoding {
		action = "ListArkCodingPlanModel"
	} else if plan != volcenginePlanAgent {
		return nil, fmt.Errorf("未知的火山套餐类型: %s", plan)
	}
	var decoded volcengineResponse
	if err := c.doAction(ctx, pair, action, "ark", struct{}{}, &decoded); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	models := make([]string, 0, len(decoded.Result.Datas))
	for _, item := range decoded.Result.Datas {
		modelID := strings.TrimSpace(item.ModelID)
		if modelID != "" && !seen[modelID] {
			seen[modelID] = true
			models = append(models, modelID)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("火山 %s 模型列表为空", displayVolcenginePlan(plan))
	}
	sort.Strings(models)
	return models, nil
}

// FetchUsage 查询火山套餐用量快照。
// Agent Plan 走 GetAFPUsage，返回含额度的四窗口；
// Coding Plan 走 GetCodingPlanUsage，返回 session/weekly/monthly 已用百分比。
func (c *volcenginePlanClient) FetchUsage(ctx context.Context, pair *config.VolcengineAccessKeyPair, plan string) (*config.VolcenginePlanUsage, error) {
	plan = normalizeVolcenginePlan(plan)
	usage := &config.VolcenginePlanUsage{FetchedAt: c.now()}
	switch plan {
	case volcenginePlanAgent:
		var decoded volcengineResponse
		if err := c.doAction(ctx, pair, "GetAFPUsage", "ark", nil, &decoded); err != nil {
			return nil, err
		}
		usage.FiveHour = afpWindow(decoded.Result.AFPFiveHour)
		usage.Daily = afpWindow(decoded.Result.AFPDaily)
		usage.Weekly = afpWindow(decoded.Result.AFPWeekly)
		usage.Monthly = afpWindow(decoded.Result.AFPMonthly)
		return usage, nil
	case volcenginePlanCoding:
		var decoded volcengineResponse
		if err := c.doAction(ctx, pair, "GetCodingPlanUsage", "ark", nil, &decoded); err != nil {
			return nil, err
		}
		usage, err := codingUsageFromQuotas(decoded.Result.QuotaUsage)
		if err != nil {
			return nil, err
		}
		usage.FetchedAt = c.now()
		return usage, nil
	default:
		return nil, fmt.Errorf("未知的火山套餐类型: %s", plan)
	}
}

// codingUsageFromQuotas 将 CodingPlan 家族的百分比用量窗口（GetCodingPlanUsage 与
// 团队版 GetSeatInfoUsage 同构）转换为通用用量快照。
func codingUsageFromQuotas(quotas []volcengineCodingPlanQuota) (*config.VolcenginePlanUsage, error) {
	usage := &config.VolcenginePlanUsage{}
	for _, quota := range quotas {
		resetTime := quota.ResetTimestamp
		if resetTime <= 0 {
			resetTime = 0
		} else if resetTime < 1_000_000_000_000 {
			resetTime *= 1000
		}
		usedPercent := quota.Percent
		window := &config.VolcenginePlanUsageWindow{UsedPercent: &usedPercent, ResetTime: resetTime}
		switch strings.ToLower(strings.TrimSpace(quota.Level)) {
		case "session", "5h", "5-hour", "fivehour", "five_hour", "rolling_5h":
			usage.FiveHour = window
		case "weekly", "week", "7d":
			usage.Weekly = window
		case "monthly", "month":
			usage.Monthly = window
		}
	}
	if usage.FiveHour == nil && usage.Weekly == nil && usage.Monthly == nil {
		return nil, fmt.Errorf("火山 Coding Plan 未返回套餐用量")
	}
	return usage, nil
}

// FetchBucketUsage 查询单个套餐桶的用量快照：personal 走既有 GetAFPUsage /
// GetCodingPlanUsage；team 走 GetSeatAFPUsage(SeatIDs) / GetSeatInfoUsage(SeatID, Scene)。
func (c *volcenginePlanClient) FetchBucketUsage(ctx context.Context, pair *config.VolcengineAccessKeyPair, bucket config.VolcenginePlanBucket) (*config.VolcenginePlanUsage, error) {
	plan := normalizeVolcenginePlan(bucket.Product)
	switch {
	case bucket.Edition != volcengineEditionTeam:
		return c.FetchUsage(ctx, pair, plan)
	case plan == volcenginePlanAgent:
		if bucket.SeatID == "" {
			return nil, fmt.Errorf("团队版 Agent Plan 缺少席位 ID，无法查询用量")
		}
		return c.fetchSeatAFPUsage(ctx, pair, bucket.SeatID)
	case plan == volcenginePlanCoding:
		if bucket.SeatID == "" {
			return nil, fmt.Errorf("团队版 Coding Plan 缺少席位 ID，无法查询用量")
		}
		return c.fetchSeatInfoUsage(ctx, pair, bucket.SeatID)
	default:
		return nil, fmt.Errorf("未知的火山套餐类型: %s", bucket.Product)
	}
}

// FetchBucketsUsage 逐桶刷新用量；单桶失败记入该桶 Usage.Error，不阻断其它桶。
// 探测阶段已失败（Error 非空）的桶不再发起用量查询。
func (c *volcenginePlanClient) FetchBucketsUsage(ctx context.Context, pair *config.VolcengineAccessKeyPair, buckets []config.VolcenginePlanBucket) []config.VolcenginePlanBucket {
	out := make([]config.VolcenginePlanBucket, len(buckets))
	copy(out, buckets)
	for i := range out {
		if out[i].Error != "" {
			continue
		}
		usage, err := c.FetchBucketUsage(ctx, pair, out[i])
		if err != nil {
			usage = &config.VolcenginePlanUsage{FetchedAt: c.now(), Error: err.Error()}
		}
		out[i].Usage = usage
	}
	return out
}

// volcengineSeatInfo 是 GetSeatInfo / GetSeatInfoUsage 的宽松解析目标。
// 团队版响应契约未经真机验证（ark-cli 未开源实现），按文档语义多字段路径兼容，
// 拿到任一非空 SeatID 即认为席位绑定。
type volcengineSeatInfo struct {
	Result struct {
		SeatID string `json:"SeatID"`
		SeatId string `json:"SeatId"`
		Seat   *struct {
			SeatID string `json:"SeatID"`
			ID     string `json:"ID"`
		} `json:"Seat"`
		Data *struct {
			List []struct {
				SeatID string `json:"SeatID"`
				SeatId string `json:"SeatId"`
			} `json:"List"`
		} `json:"Data"`
		QuotaUsage []volcengineCodingPlanQuota `json:"QuotaUsage"`
		Usage      []volcengineCodingPlanQuota `json:"Usage"`
	} `json:"Result"`
}

func (r *volcengineSeatInfo) seatID() string {
	if v := strings.TrimSpace(r.Result.SeatID); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Result.SeatId); v != "" {
		return v
	}
	if r.Result.Seat != nil {
		if v := strings.TrimSpace(r.Result.Seat.SeatID); v != "" {
			return v
		}
		if v := strings.TrimSpace(r.Result.Seat.ID); v != "" {
			return v
		}
	}
	if r.Result.Data != nil {
		for _, item := range r.Result.Data.List {
			if v := strings.TrimSpace(item.SeatID); v != "" {
				return v
			}
			if v := strings.TrimSpace(item.SeatId); v != "" {
				return v
			}
		}
	}
	return ""
}

// volcengineSeatAFPUsage 是 GetSeatAFPUsage 的宽松解析目标：窗口可能在 Result
// 顶层（单席位）或 Seats/Datas 数组（多席位，按 SeatID 匹配）。
type volcengineSeatAFPUsage struct {
	Result struct {
		AFPFiveHour *volcengineAFPWindow     `json:"AFPFiveHour,omitempty"`
		AFPDaily    *volcengineAFPWindow     `json:"AFPDaily,omitempty"`
		AFPWeekly   *volcengineAFPWindow     `json:"AFPWeekly,omitempty"`
		AFPMonthly  *volcengineAFPWindow     `json:"AFPMonthly,omitempty"`
		Seats       []volcengineSeatAFPEntry `json:"Seats,omitempty"`
		Datas       []volcengineSeatAFPEntry `json:"Datas,omitempty"`
	} `json:"Result"`
}

type volcengineSeatAFPEntry struct {
	SeatID      string               `json:"SeatID"`
	SeatId      string               `json:"SeatId"`
	AFPFiveHour *volcengineAFPWindow `json:"AFPFiveHour,omitempty"`
	AFPDaily    *volcengineAFPWindow `json:"AFPDaily,omitempty"`
	AFPWeekly   *volcengineAFPWindow `json:"AFPWeekly,omitempty"`
	AFPMonthly  *volcengineAFPWindow `json:"AFPMonthly,omitempty"`
}

func (e *volcengineSeatAFPEntry) seatID() string {
	if v := strings.TrimSpace(e.SeatID); v != "" {
		return v
	}
	return strings.TrimSpace(e.SeatId)
}

func (e *volcengineSeatAFPEntry) empty() bool {
	return e.AFPFiveHour == nil && e.AFPDaily == nil && e.AFPWeekly == nil && e.AFPMonthly == nil
}

// getTeamSeat 探测团队版席位绑定；空 SeatID 表示该身份未绑席位（未订阅或未分配），
// 不视为错误。scene 取值见 volcengineAgentPlanEnterpriseScene 注释。
func (c *volcenginePlanClient) getTeamSeat(ctx context.Context, pair *config.VolcengineAccessKeyPair, scene string) (string, error) {
	raw, err := c.doActionRaw(ctx, pair, "GetSeatInfo", "ark", map[string]string{"Scene": scene})
	if err != nil {
		return "", err
	}
	var decoded volcengineSeatInfo
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("解析火山 GetSeatInfo 响应失败: %w", err)
	}
	return decoded.seatID(), nil
}

// fetchSeatAFPUsage 查询团队版 Agent Plan 席位用量（GetSeatAFPUsage）。
func (c *volcenginePlanClient) fetchSeatAFPUsage(ctx context.Context, pair *config.VolcengineAccessKeyPair, seatID string) (*config.VolcenginePlanUsage, error) {
	raw, err := c.doActionRaw(ctx, pair, "GetSeatAFPUsage", "ark", map[string][]string{"SeatIDs": {seatID}})
	if err != nil {
		return nil, err
	}
	var decoded volcengineSeatAFPUsage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("解析火山 GetSeatAFPUsage 响应失败: %w", err)
	}
	windows := struct {
		AFPFiveHour, AFPDaily, AFPWeekly, AFPMonthly *volcengineAFPWindow
	}{decoded.Result.AFPFiveHour, decoded.Result.AFPDaily, decoded.Result.AFPWeekly, decoded.Result.AFPMonthly}
	if windows.AFPFiveHour == nil && windows.AFPDaily == nil && windows.AFPWeekly == nil && windows.AFPMonthly == nil {
		for _, entry := range append(append([]volcengineSeatAFPEntry(nil), decoded.Result.Seats...), decoded.Result.Datas...) {
			if entry.empty() {
				continue
			}
			if id := entry.seatID(); id != "" && id != seatID {
				continue
			}
			windows.AFPFiveHour, windows.AFPDaily = entry.AFPFiveHour, entry.AFPDaily
			windows.AFPWeekly, windows.AFPMonthly = entry.AFPWeekly, entry.AFPMonthly
			break
		}
	}
	usage := &config.VolcenginePlanUsage{FetchedAt: c.now()}
	usage.FiveHour = afpWindow(windows.AFPFiveHour)
	usage.Daily = afpWindow(windows.AFPDaily)
	usage.Weekly = afpWindow(windows.AFPWeekly)
	usage.Monthly = afpWindow(windows.AFPMonthly)
	if usage.FiveHour == nil && usage.Daily == nil && usage.Weekly == nil && usage.Monthly == nil {
		return nil, fmt.Errorf("火山团队版 Agent Plan 未返回席位用量")
	}
	return usage, nil
}

// fetchSeatInfoUsage 查询团队版 Coding Plan 席位用量（GetSeatInfoUsage，
// CodingPlan 家族百分比窗口，字段名兼容 QuotaUsage/Usage）。
func (c *volcenginePlanClient) fetchSeatInfoUsage(ctx context.Context, pair *config.VolcengineAccessKeyPair, seatID string) (*config.VolcenginePlanUsage, error) {
	raw, err := c.doActionRaw(ctx, pair, "GetSeatInfoUsage", "ark", map[string]string{"SeatID": seatID, "Scene": ""})
	if err != nil {
		return nil, err
	}
	var decoded volcengineSeatInfo
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("解析火山 GetSeatInfoUsage 响应失败: %w", err)
	}
	quotas := decoded.Result.QuotaUsage
	if len(quotas) == 0 {
		quotas = decoded.Result.Usage
	}
	usage, err := codingUsageFromQuotas(quotas)
	if err != nil {
		return nil, fmt.Errorf("火山团队版 Coding Plan 未返回席位用量")
	}
	usage.FetchedAt = c.now()
	return usage, nil
}

// afpWindow 将火山 AFP 窗口转换为通用用量窗口；nil 输入返回 nil。
func afpWindow(w *volcengineAFPWindow) *config.VolcenginePlanUsageWindow {
	if w == nil {
		return nil
	}
	return &config.VolcenginePlanUsageWindow{Quota: w.Quota, Used: w.Used, ResetTime: w.ResetTime}
}

// now 返回客户端时钟（便于测试注入）。
func (c *volcenginePlanClient) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *volcenginePlanClient) doAction(ctx context.Context, pair *config.VolcengineAccessKeyPair, action, service string, payload any, target *volcengineResponse) error {
	raw, err := c.doActionRaw(ctx, pair, action, service, payload)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("解析火山管控面响应失败: %w", err)
	}
	return nil
}

// doActionRaw 发起管控面签名请求并校验错误，成功时返回原始响应体，
// 供使用独立响应结构（团队版席位类接口）的调用方自行解码。
func (c *volcenginePlanClient) doActionRaw(ctx context.Context, pair *config.VolcengineAccessKeyPair, action, service string, payload any) (json.RawMessage, error) {
	if pair == nil || strings.TrimSpace(pair.AccessKeyID) == "" || strings.TrimSpace(pair.SecretAccessKey) == "" {
		return nil, fmt.Errorf("火山套餐识别、模型发现和用量查询需要绑定 Access Key ID 与 Secret Access Key")
	}
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("编码火山管控面请求失败: %w", err)
		}
	}
	endpoint := c.endpointFor(action, service)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造火山管控面请求失败: %w", err)
	}
	query := req.URL.Query()
	query.Set("Action", action)
	query.Set("Version", volcengineAPIVersion)
	req.URL.RawQuery = query.Encode()
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	applyVolcengineSignature(req, body, pair.AccessKeyID, pair.SecretAccessKey, service, now)
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求火山管控面失败: %w", err)
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, fmt.Errorf("读取火山管控面响应失败: %w", err)
	}
	var decoded volcengineResponse
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, fmt.Errorf("解析火山管控面响应失败: %w", err)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || decoded.ResponseMetadata.Error != nil {
		apiErr := &volcengineAPIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
		if decoded.ResponseMetadata.Error != nil {
			apiErr.Code = decoded.ResponseMetadata.Error.Code
			apiErr.Message = decoded.ResponseMetadata.Error.Message
		}
		return nil, apiErr
	}
	return json.RawMessage(raw), nil
}

func (c *volcenginePlanClient) endpointFor(action, service string) string {
	if endpoint := strings.TrimSpace(c.Endpoint); endpoint != "" {
		return endpoint
	}
	switch action {
	case "GetAFPUsage", "GetCodingPlanUsage", "GetSeatInfo", "GetSeatAFPUsage", "GetSeatInfoUsage":
		// 套餐用量与席位类接口走公网 OpenAPI 入口（ark-cli 调用面对齐）。
		return "https://" + volcengineOpenAPIHost + "/"
	}
	return "https://" + volcengineManagementHost + "/"
}

func applyVolcengineSignature(req *http.Request, body []byte, accessKeyID, secretAccessKey, service string, now time.Time) {
	payloadHash := sha256Hex(body)
	xDate := now.UTC().Format("20060102T150405Z")
	shortDate := xDate[:8]
	host := req.URL.Host
	canonicalHeaders := "host:" + host + "\n" +
		"x-content-sha256:" + payloadHash + "\n" +
		"x-date:" + xDate + "\n"
	canonicalRequest := req.Method + "\n" + canonicalURI(req.URL) + "\n" + canonicalQuery(req.URL) + "\n" + canonicalHeaders + "\n" + volcengineSignedHeaders + "\n" + payloadHash
	credentialScope := shortDate + "/" + volcengineRegion + "/" + service + "/request"
	stringToSign := "HMAC-SHA256\n" + xDate + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))
	kDate := hmacSHA256([]byte(secretAccessKey), shortDate)
	kRegion := hmacSHA256(kDate, volcengineRegion)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))
	req.Host = host
	req.Header.Set("Content-Type", volcengineContentType)
	req.Header.Set("X-Date", xDate)
	req.Header.Set("X-Content-Sha256", payloadHash)
	req.Header.Set("Authorization", "HMAC-SHA256 Credential="+accessKeyID+"/"+credentialScope+", SignedHeaders="+volcengineSignedHeaders+", Signature="+signature)
}

func canonicalURI(value *url.URL) string {
	if value == nil || value.EscapedPath() == "" {
		return "/"
	}
	return value.EscapedPath()
}

func canonicalQuery(value *url.URL) string {
	if value == nil {
		return ""
	}
	return value.Query().Encode()
}

func hmacSHA256(secret []byte, value string) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func normalizeVolcenginePlan(plan string) string {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "agentplan", "agent_plan", "agent":
		return volcenginePlanAgent
	case "codingplan", "coding_plan", "coding":
		return volcenginePlanCoding
	default:
		return ""
	}
}

func volcenginePlanFromBaseURL(baseURL string) string {
	lower := strings.ToLower(strings.TrimSpace(baseURL))
	if strings.Contains(lower, "ark.cn-beijing.volces.com/api/plan") {
		return volcenginePlanAgent
	}
	if strings.Contains(lower, "ark.cn-beijing.volces.com/api/coding") {
		return volcenginePlanCoding
	}
	return ""
}

func displayVolcenginePlan(plan string) string {
	if normalizeVolcenginePlan(plan) == volcenginePlanAgent {
		return "Agent Plan"
	}
	return "Coding Plan"
}

// FetchVolcenginePlanModelsForChannel 供管理端"拉取模型列表"入口使用。
// 火山套餐渠道的数据面 /models 不反映套餐真实清单：/api/coding 返回账号可见的
// 全量模型目录（含按量付费模型），/api/plan 无此接口直接 404，因此改走管控面
// 套餐模型接口（ListArkCodingPlanModel / ListArkAgentPlanModel）。
// channel 可为 nil（编辑对话框带临时 baseUrl 时后端无渠道上下文），此时按
// API Key 在托管账号中定位凭证。
// 返回 (模型列表, 是否命中火山套餐端点, 错误)：
//   - baseURL 非火山套餐端点：未命中，调用方继续原数据面路径；
//   - 命中且推理 Key 绑定了 Access Key：实时调用管控面返回真实套餐清单；
//   - 命中但未绑定 Access Key（含新增渠道的临时 baseUrl 场景）：返回内置兜底
//     清单，与自动发现的"未绑定回退内置清单"口径一致。
func FetchVolcenginePlanModelsForChannel(ctx context.Context, cfgManager *config.ConfigManager, channel *config.UpstreamConfig, baseURL, apiKey string) ([]string, bool, error) {
	return fetchVolcenginePlanModelsForChannel(ctx, cfgManager, channel, baseURL, apiKey, "", nil)
}

func fetchVolcenginePlanModelsForChannel(ctx context.Context, cfgManager *config.ConfigManager, channel *config.UpstreamConfig, baseURL, apiKey, endpoint string, httpClient *http.Client) ([]string, bool, error) {
	plan := volcenginePlanFromBaseURL(baseURL)
	if plan == "" {
		return nil, false, nil
	}
	if cfgManager != nil {
		accountUID, credentialUID := "", ""
		if channel != nil && strings.TrimSpace(channel.AccountUID) != "" {
			accountUID = channel.AccountUID
			credentialUID = channel.CredentialUIDForKey(apiKey)
		} else {
			accountUID, credentialUID = cfgManager.FindCredentialByAPIKey(apiKey)
		}
		if accountUID != "" && credentialUID != "" {
			if credential, ok := cfgManager.GetManagedAccountCredential(accountUID, credentialUID); ok && credential.VolcengineAccessKey != nil {
				client := &volcenginePlanClient{Endpoint: endpoint, HTTPClient: httpClient}
				models, err := client.FetchModels(ctx, credential.VolcengineAccessKey, plan)
				if err != nil {
					return nil, true, err
				}
				return models, true, nil
			}
		}
	}
	if plan == volcenginePlanAgent {
		return config.VolcengineAgentPlanModelIDs(), true, nil
	}
	return config.VolcengineCodingPlanModelIDs(), true, nil
}
