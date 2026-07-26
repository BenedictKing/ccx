package autopilot

// profile_coverage.go 实现模型画像覆盖率诊断（Task 7 覆盖率门槛）。
//
// 职责：对每个渠道的 endpoint 检查 ModelProfile 是否具备自动决策的最低画像条件。
// 用途：在手工映射表退役前，人工/程序化验证目标渠道是否已具备自动决策基础。
//
// 只读操作：不修改任何画像、配置或调度状态。

// CoverageEndpointReport 单个 endpoint 的画像覆盖率报告。
type CoverageEndpointReport struct {
	EndpointUID  string   `json:"endpointUid"`
	ChannelUID   string   `json:"channelUid"`
	ChannelKind  string   `json:"channelKind"`
	MetricsKey   string   `json:"metricsKey"`
	BaseURL      string   `json:"baseUrl,omitempty"`
	KeyMask      string   `json:"keyMask,omitempty"`
	HasProfile   bool     `json:"hasProfile"`              // ModelProfileStore 中是否存在该 endpoint 的模型画像
	Sources      []string `json:"sources,omitempty"`       // 该 endpoint 下所有模型画像的 Source 去重列表
	ProbedOK     bool     `json:"probedOk"`                // 是否至少有一个 ProbeSuccess=true 的画像
	HasEffort    bool     `json:"hasEffortControlSupport"` // 是否至少有一个 SupportsEffortControl=true 的画像
	ProfileCount int      `json:"profileCount"`            // 该 endpoint 下的模型画像总数
}

// CoverageChannelReport 单个渠道的画像覆盖率报告。
type CoverageChannelReport struct {
	ChannelUID  string                   `json:"channelUid"`
	ChannelKind string                   `json:"channelKind"`
	ChannelName string                   `json:"channelName,omitempty"`
	Verdict     string                   `json:"verdict"` // "ready" | "not_ready"
	Reasons     []string                 `json:"reasons,omitempty"`
	Endpoints   []CoverageEndpointReport `json:"endpoints"`
}

// CoverageReportResponse GET /api/health-center/profile-coverage 的响应结构。
type CoverageReportResponse struct {
	Channels []CoverageChannelReport `json:"channels"`
}

// computeProfileCoverage 只读计算所有渠道的画像覆盖率。
// 数据来源：ProfileStore（endpoint 清单）+ ModelProfileStore（模型画像）。
func computeProfileCoverage(mgr *Manager) CoverageReportResponse {
	profiles := mgr.ProfileStore().ListActive()

	// 按 channelUID 分组
	grouped := make(map[string][]*KeyEndpointProfile)
	for _, p := range profiles {
		grouped[p.ChannelUID] = append(grouped[p.ChannelUID], p)
	}

	// 渠道名称映射
	channelNames := buildChannelNameMap(mgr.cfgManager.GetConfig())

	resp := CoverageReportResponse{}
	for channelUID, eps := range grouped {
		report := computeChannelCoverage(mgr, channelUID, eps, channelNames)
		resp.Channels = append(resp.Channels, report)
	}
	// 保证输出稳定
	if resp.Channels == nil {
		resp.Channels = []CoverageChannelReport{}
	}
	return resp
}

// computeChannelCoverage 计算单个渠道的画像覆盖率。
func computeChannelCoverage(mgr *Manager, channelUID string, endpoints []*KeyEndpointProfile, channelNames map[string]string) CoverageChannelReport {
	channelKind := ""
	if len(endpoints) > 0 {
		channelKind = endpoints[0].ChannelKind
	}

	report := CoverageChannelReport{
		ChannelUID:  channelUID,
		ChannelKind: channelKind,
		ChannelName: channelNames[channelUID],
		Endpoints:   make([]CoverageEndpointReport, 0, len(endpoints)),
	}

	hasMissingProfile := false
	hasUnprobedEndpoint := false
	hasPureBuiltinRegistry := false

	modelStore := mgr.ModelProfileStore()
	for _, ep := range endpoints {
		epReport := CoverageEndpointReport{
			EndpointUID: ep.EndpointUID,
			ChannelUID:  ep.ChannelUID,
			ChannelKind: ep.ChannelKind,
			MetricsKey:  ep.MetricsKey,
			BaseURL:     ep.BaseURL,
			KeyMask:     ep.KeyMask,
		}

		if modelStore == nil {
			epReport.HasProfile = false
			hasMissingProfile = true
			report.Endpoints = append(report.Endpoints, epReport)
			continue
		}

		modelProfiles := modelStore.GetModelProfiles(ep.ChannelUID, ep.ChannelKind, ep.MetricsKey)
		epReport.ProfileCount = len(modelProfiles)
		epReport.HasProfile = len(modelProfiles) > 0

		if len(modelProfiles) > 0 {
			sourceSet := make(map[string]struct{})
			for _, mp := range modelProfiles {
				sourceSet[mp.Source] = struct{}{}
				if mp.ProbeSuccess {
					epReport.ProbedOK = true
				}
				if mp.SupportsEffortControl {
					epReport.HasEffort = true
				}
			}
			for src := range sourceSet {
				epReport.Sources = append(epReport.Sources, src)
			}

			// 检查是否所有画像来源都是纯 builtin_registry 猜测
			allBuiltin := true
			for _, mp := range modelProfiles {
				if mp.Source != "" && mp.Source != "builtin_registry" {
					allBuiltin = false
					break
				}
			}
			if allBuiltin {
				hasPureBuiltinRegistry = true
			}

			if !epReport.ProbedOK {
				hasUnprobedEndpoint = true
			}
		} else {
			hasMissingProfile = true
		}

		report.Endpoints = append(report.Endpoints, epReport)
	}

	// 聚合为渠道级判定
	reasons := []string{}
	if hasMissingProfile {
		reasons = append(reasons, "存在 endpoint 缺少模型画像")
	}
	if hasPureBuiltinRegistry {
		reasons = append(reasons, "存在 endpoint 画像来源仅含 builtin_registry 猜测")
	}
	if hasUnprobedEndpoint {
		reasons = append(reasons, "存在 endpoint 无探测成功的画像")
	}

	if len(reasons) == 0 {
		report.Verdict = "ready"
	} else {
		report.Verdict = "not_ready"
		report.Reasons = reasons
	}
	return report
}
