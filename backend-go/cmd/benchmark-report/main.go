// 独立 CLI：根据当前 model-registry snapshot 输出渠道无关的
// [BenchmarkSelectionReport] 模型选择报告（主程序不再输出该报告）。
//
// 用法：
//
//	benchmark-report -config /path/to/config.json
//
// 设计要点：
//   - 不依赖运行中的 ccx 后端；用 in-memory SQLite 数据库作为 ModelProfileStore。
//   - 候选画像从 presetstore 默认 snapshot 的 benchmarkProfiles 合成（真实模型 ID，
//     不注入 upstreamCapabilities 的正则 patterns），并复用全局能力表补齐上下文窗口、
//     effort 档位与能力布尔，使长上下文/思考强度场景的选择行为与运行态一致。
//   - 报告按"全局候选池 × 典型场景"输出一行一选（worker/supervisor/轻量/长上下文/编程），
//     不做逐渠道映射；渠道实际可选范围取决于各渠道探测画像。
//   - 复用 autopilot.GenerateBenchmarkSelectionReport + LogBenchmarkSelectionReport；
//     effort 展开与 EffortFloor 依赖 config.json 中的 autopilot.reasoningEffort 配置。
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
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
	// 1. 加载 config（resolver 的 effort 展开与任务类 EffortFloor 依赖其中的 autopilot 配置）。
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

	// 4. 从 snapshot 合成全局候选画像池（单一 poolUID）。
	injected, err := injectSyntheticProfiles(profileStore, bundle)
	if err != nil {
		return fmt.Errorf("合成画像失败: %w", err)
	}
	log.Printf("已注入 %d 个候选到全局池", injected)

	// 5. 构造 resolver 并生成场景报告。
	resolver := autopilot.NewModelResolver(profileStore, cfgManager)
	report := autopilot.GenerateBenchmarkSelectionReport(resolver, syntheticChannelUID)
	if report == nil || len(report.Scenarios) == 0 {
		log.Printf("无场景数据可输出（合成画像为空）")
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

// injectSyntheticProfiles 把 snapshot 中 benchmarkProfiles 的 CanonicalModel 展开成
// ModelProfile，全部 ProbeSuccess=true、中性 ProviderQuality，写入 in-memory store。
//
// 注意：不注入 upstreamCapabilities 的 patterns——它们是匹配用的正则表达式而非真实模型 ID，
// 注入后会让 selected/better_options 输出不可读的正则串；patterns 仅通过
// config.ResolveUpstreamCapability 用于回填画像的上下文/effort/能力字段。
func injectSyntheticProfiles(store *autopilot.ModelProfileStore, bundle *presetstore.PresetBundle) (int, error) {
	if bundle == nil || bundle.ModelRegistry == nil {
		return 0, nil
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

	global := config.BuiltinUpstreamModelCapabilities()
	for _, modelID := range ids {
		profile := buildSyntheticProfile(modelID, global, now)
		if err := store.Upsert(profile); err != nil {
			return 0, fmt.Errorf("Upsert %s: %w", modelID, err)
		}
	}
	return len(ids), nil
}

// buildSyntheticProfile 构造一条合成 ModelProfile，并用全局能力表补齐
// 上下文窗口、effort 档位与能力布尔（未命中能力表时保持零值，即"未知"）。
func buildSyntheticProfile(modelID string, global map[string]config.UpstreamModelCapability, now time.Time) *autopilot.ModelProfile {
	family := autopilot.InferModelFamily(modelID, "")
	quality := autopilot.ModelProfileQualityTierFromFamily(family, modelID)
	profile := &autopilot.ModelProfile{
		ChannelUID:                syntheticChannelUID,
		ChannelID:                 syntheticChannelIndex,
		ChannelKind:               syntheticChannelKind,
		ServiceType:               syntheticServiceType,
		MetricsKey:                syntheticMetricsKey,
		ModelID:                   modelID,
		UpdatedAt:                 now,
		ModelFamily:               family,
		QualityTier:               quality,
		ProviderQualityScore:      0,
		ProviderQualitySource:     "synthetic",
		ProviderQualityConfidence: 0,
		ProbeSuccess:              true,
		LastProbeAt:               now,
		Source:                    "synthetic_registry",
	}

	resolved := resolveSyntheticCapability(modelID, global)
	if !resolved.Known {
		return profile
	}
	capability := resolved.Capability
	profile.ContextTokens = capability.ContextWindowTokens
	profile.SupportsVision = capability.Capabilities["vision"]
	profile.SupportsDocument = capability.Capabilities["document"]
	profile.SupportsToolCalls = capability.Capabilities["toolCalls"]
	profile.SupportsReasoning = capability.Capabilities["reasoning"]
	for _, raw := range capability.ReasoningEfforts {
		if lv := autopilot.NormalizeEffortLevel(raw); lv != "" {
			profile.SupportedEffortLevels = append(profile.SupportedEffortLevels, lv)
		}
	}
	profile.SupportsEffortControl = len(profile.SupportedEffortLevels) > 0
	return profile
}

// digitDotToDashPattern 匹配数字间的点号（如 4.5），用于规范 ID 与能力表命名差异的兜底。
var digitDotToDashPattern = regexp.MustCompile(`(\d)\.(\d)`)

// resolveSyntheticCapability 解析合成画像对应的能力表条目。
// benchmarkProfiles 的 canonical ID 用点号版本（claude-haiku-4.5），而 upstreamCapabilities
// 部分 pattern 用连字符版本（claude-haiku-4-5）；直接匹配失败时按数字间点号转连字符兜底一次。
func resolveSyntheticCapability(modelID string, global map[string]config.UpstreamModelCapability) config.ResolvedUpstreamCapability {
	resolved := config.ResolveUpstreamCapability(modelID, nil, global)
	if resolved.Known {
		return resolved
	}
	if variant := digitDotToDashPattern.ReplaceAllString(modelID, "$1-$2"); variant != modelID {
		if r := config.ResolveUpstreamCapability(variant, nil, global); r.Known {
			return r
		}
	}
	return resolved
}
