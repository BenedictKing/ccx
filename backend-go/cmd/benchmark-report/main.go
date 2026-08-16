// 独立 CLI：根据当前 model-registry snapshot 与 config.json
// 输出 [BenchmarkSelectionReport] 模型选择报告（主程序已不再输出该报告）。
//
// 用法：
//
//	benchmark-report -config /path/to/config.json
//
// 设计要点：
//   - 不依赖运行中的 ccx 后端；用 in-memory SQLite 数据库作为 ModelProfileStore。
//   - 复用 autopilot.GenerateBenchmarkSelectionReport + LogBenchmarkSelectionReport。
//   - 候选画像从 presetstore 默认 snapshot 的 benchmarkProfiles 合成
//     （所有 ProbeSuccess=true、中性 ProviderQuality），注入 in-memory store。
//   - 报告所需的 auto-managed 渠道列表由 GenerateBenchmarkSelectionReport 内部从
//     cfgManager.GetConfig() 读取；CLI 仅负责合成候选画像。
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/presetstore"
	_ "modernc.org/sqlite"
)

const (
	syntheticChannelUID   = "ch_synthetic_registry"
	syntheticMetricsKey   = "synthetic"
	syntheticChannelKind  = "messages"
	syntheticServiceType  = "synthetic"
	syntheticChannelIndex = 0
)

func main() {
	configPath := flag.String("config", ".config/config.json", "CCX config.json 路径")
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("[benchmark-report] ")

	if err := run(*configPath); err != nil {
		log.Printf("失败: %v", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	// 1. 加载 config
	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		return fmt.Errorf("加载 config 失败: %w", err)
	}
	defer cfgManager.Close()

	// 2. 强制 presetstore 默认 snapshot 重建（CLI 进程触发 builtinOnce.Do 首次构建）。
	bundle := presetstore.Default().Get()

	// 3. 创建 in-memory ModelProfileStore。
	db, err := openInMemoryDB()
	if err != nil {
		return fmt.Errorf("打开 in-memory 数据库失败: %w", err)
	}
	defer db.Close()

	profileStore, err := autopilot.NewModelProfileStoreWithDB(db)
	if err != nil {
		return fmt.Errorf("构造 profile store 失败: %w", err)
	}

	// 4. 从 snapshot 合成候选画像，注入 in-memory store。
	// 为每个 auto-managed 渠道都复制一份共享画像池，让 ListActiveByChannel(uid) 都能查到。
	channels := autopilotCollectAutoManagedChannels(cfgManager.GetConfig())
	if err := injectSyntheticProfiles(profileStore, bundle, channels); err != nil {
		return fmt.Errorf("合成画像失败: %w", err)
	}

	// 5. 构造 resolver 并调起报告生成。
	// channels 列表由 GenerateBenchmarkSelectionReport 内部从 cfgManager.GetConfig() 读取。
	resolver := autopilot.NewModelResolver(profileStore, cfgManager)
	report := autopilot.GenerateBenchmarkSelectionReport(resolver, cfgManager)
	if report == nil || len(report.Scenarios) == 0 {
		log.Printf("无场景数据可输出（无 auto-managed 渠道或合成画像为空）")
		return nil
	}
	autopilot.LogBenchmarkSelectionReport(report)
	return nil
}

// openInMemoryDB 创建 in-memory SQLite。
func openInMemoryDB() (*sql.DB, error) {
	dsn := "file:benchmark_report?mode=memory&cache=shared&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

// syntheticChannelInfo 描述 CLI 内部要为其复制画像的渠道。
type syntheticChannelInfo struct {
	ChannelUID  string
	ChannelKind string
}

// autopilotCollectAutoManagedChannels 直接从 cfg 中收集所有 auto-managed 渠道。
// 这是复刻 benchmark_report.go.collectAutoManagedChannels 的导出版本——CLI 模式需要
// 在合成画像阶段为每个渠道都注入画像副本，因此必须能看到渠道列表。
func autopilotCollectAutoManagedChannels(cfg config.Config) []syntheticChannelInfo {
	var out []syntheticChannelInfo
	collect := func(list []config.UpstreamConfig, kind string) {
		for _, up := range list {
			if !up.AutoManaged || up.Status == "disabled" || up.ChannelUID == "" {
				continue
			}
			out = append(out, syntheticChannelInfo{ChannelUID: up.ChannelUID, ChannelKind: kind})
		}
	}
	collect(cfg.Upstream, "messages")
	collect(cfg.ResponsesUpstream, "responses")
	collect(cfg.ChatUpstream, "chat")
	return out
}

// injectSyntheticProfiles 把 snapshot 中 benchmarkProfiles 的 CanonicalModel 展开成
// ModelProfile，全部 ProbeSuccess=true、中性 ProviderQuality，写入 in-memory store。
//
// 注意：不注入 upstreamCapabilities 的 patterns——它们是匹配用的正则表达式而非真实模型 ID，
// 注入后会让 selected/mapped/better_options 输出不可读的正则串。
//
// 每个 auto-managed 渠道的 ChannelUID + ChannelKind 都被注入一份"指向同一全局候选池"的画像
// 副本（metricsKey 全部用 synthetic）。这样 ListActiveByChannel(uid) 都能返回共享的全局候选，
// CLI 报告的 per-channel 行就不再是 no_eligible_profiles。
//
// CLI 与运行态后端的差异：运行态后端每个渠道有自己探测到的画像子集，CLI 用的是全局共享池
// （所有 auto-managed 渠道看到相同的候选）。这是已知差异，已在 benchmark-report 命令头部说明。
func injectSyntheticProfiles(
	store *autopilot.ModelProfileStore,
	bundle *presetstore.PresetBundle,
	channels []syntheticChannelInfo,
) error {
	if bundle == nil || bundle.ModelRegistry == nil {
		return nil
	}
	now := time.Now().UTC()

	canonicalIDs := make(map[string]struct{})
	for _, bp := range bundle.ModelRegistry.BenchmarkProfiles {
		canonical := strings.TrimSpace(bp.CanonicalModel)
		if canonical == "" {
			continue
		}
		canonicalIDs[canonical] = struct{}{}
	}

	ids := make([]string, 0, len(canonicalIDs))
	for id := range canonicalIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// 1. 把每个 canonical model id 注入一次"全局池"（使用 syntheticChannelUID），
	// 供 ListActiveByChannel("ch_synthetic_registry") 复用。
	for _, modelID := range ids {
		profile := buildSyntheticProfile(syntheticChannelUID, syntheticChannelKind, syntheticServiceType, syntheticMetricsKey, modelID, now)
		if err := store.Upsert(profile); err != nil {
			return fmt.Errorf("Upsert global %s: %w", modelID, err)
		}
	}

	// 2. 为每个 auto-managed 渠道复制一份画像（ChannelUID 改为该渠道真实 UID，
	// ChannelKind 与渠道一致），保证 ListActiveByChannel(uid) 返回非空。
	for _, ch := range channels {
		for _, modelID := range ids {
			kind := ch.ChannelKind
			if kind == "" {
				kind = syntheticChannelKind
			}
			svcType := syntheticServiceType
			// 简化：CLI 模式不区分 serviceType，固定 synthetic。
			profile := buildSyntheticProfile(ch.ChannelUID, kind, svcType, syntheticMetricsKey, modelID, now)
			if err := store.Upsert(profile); err != nil {
				return fmt.Errorf("Upsert %s/%s: %w", ch.ChannelUID, modelID, err)
			}
		}
	}

	log.Printf("已注入 %d 个候选 × %d 渠道（含全局池）到 in-memory store", len(ids), len(channels)+1)
	return nil
}

// buildSyntheticProfile 构造一条合成 ModelProfile。
func buildSyntheticProfile(channelUID, channelKind, serviceType, metricsKey, modelID string, now time.Time) *autopilot.ModelProfile {
	family := autopilot.InferModelFamily(modelID, "")
	quality := autopilot.ModelProfileQualityTierFromFamily(family, modelID)
	return &autopilot.ModelProfile{
		ChannelUID:                channelUID,
		ChannelID:                 syntheticChannelIndex,
		ChannelKind:               channelKind,
		ServiceType:               serviceType,
		MetricsKey:                metricsKey,
		ModelID:                   modelID,
		UpdatedAt:                 now,
		ModelFamily:               family,
		QualityTier:               quality,
		ContextTokens:             0,
		SupportsVision:            false,
		SupportsDocument:          false,
		SupportsToolCalls:         false,
		SupportsReasoning:         false,
		ProviderQualityScore:      0,
		ProviderQualitySource:     "synthetic",
		ProviderQualityConfidence: 0,
		SupportsEffortControl:     false,
		ProbeSuccess:              true,
		LastProbeAt:               now,
		ProbeLatencyMs:            0,
		ProbeConfidence:           0,
		Source:                    "synthetic_registry",
	}
}
