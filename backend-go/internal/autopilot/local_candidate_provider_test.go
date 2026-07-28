package autopilot

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── 表驱动测试 ──

// 创建测试用的 LocalRuntimeStore（复用 local_model_runtime_test.go 中的 newTestLocalRuntimeStore）。
// 注意：newTestLocalRuntimeStore 在 local_model_runtime_test.go 中定义，接受 *testing.T 参数。

func TestCollectLocalCandidates(t *testing.T) {
	store := newTestLocalRuntimeStore(t)

	// 准备测试数据：3 个运行时
	healthyRuntime := &LocalModelRuntimeProfile{
		RuntimeUID:        "lr_healthy01",
		Name:              "ollama-qwen2",
		RuntimeType:       RuntimeTypeOllama,
		Status:            LocalRuntimeHealthy,
		ContextTokens:     4096,
		SupportsTools:     true,
		SupportsVision:    false,
		SupportsReasoning: false,
	}
	slowRuntime := &LocalModelRuntimeProfile{
		RuntimeUID:        "lr_slow01",
		Name:              "ollama-llama3-slow",
		RuntimeType:       RuntimeTypeOllama,
		Status:            LocalRuntimeSlow,
		ContextTokens:     8192,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: false,
	}
	unavailableRuntime := &LocalModelRuntimeProfile{
		RuntimeUID:        "lr_unavail01",
		Name:              "lmstudio-dead",
		RuntimeType:       RuntimeTypeLMStudio,
		Status:            LocalRuntimeUnavailable,
		ContextTokens:     2048,
		SupportsTools:     false,
		SupportsVision:    false,
		SupportsReasoning: false,
	}
	require.NoError(t, store.Upsert(healthyRuntime))
	require.NoError(t, store.Upsert(slowRuntime))
	require.NoError(t, store.Upsert(unavailableRuntime))

	tests := []struct {
		name      string
		cfg       config.LocalModelRoutingConfig
		taskClass TaskClass
		wantLen   int      // 期望返回条目数
		wantUIDs  []string // 期望返回的 RuntimeUID 列表（有序）
		wantEmpty bool     // 期望返回 nil/空列表
	}{
		{
			name: "默认配置_Mode=shadow_返回空_零行为不变量",
			cfg: config.LocalModelRoutingConfig{
				Enabled:                  true,
				Mode:                     "shadow",
				AllowLocalForTaskClasses: []string{"lightweight", "worker"},
				NeverDemoteTaskClasses:   []string{"supervisor", "vision", "long_context"},
			},
			taskClass: TaskClassLightweight,
			wantEmpty: true,
		},
		{
			name: "Enabled=false_返回空",
			cfg: config.LocalModelRoutingConfig{
				Enabled:                  false,
				Mode:                     config.AutopilotModeAuto,
				AllowLocalForTaskClasses: []string{"lightweight", "worker"},
			},
			taskClass: TaskClassLightweight,
			wantEmpty: true,
		},
		{
			name: "Mode=空字符串_返回空",
			cfg: config.LocalModelRoutingConfig{
				Enabled:                  true,
				Mode:                     "",
				AllowLocalForTaskClasses: []string{"lightweight", "worker"},
			},
			taskClass: TaskClassLightweight,
			wantEmpty: true,
		},
		{
			name: "Mode=off_返回空",
			cfg: config.LocalModelRoutingConfig{
				Enabled:                  true,
				Mode:                     "off",
				AllowLocalForTaskClasses: []string{"lightweight", "worker"},
			},
			taskClass: TaskClassLightweight,
			wantEmpty: true,
		},
		{
			name: "Mode=disabled_返回空",
			cfg: config.LocalModelRoutingConfig{
				Enabled:                  true,
				Mode:                     "disabled",
				AllowLocalForTaskClasses: []string{"lightweight", "worker"},
			},
			taskClass: TaskClassLightweight,
			wantEmpty: true,
		},
		{
			name: "taskClass不在允许列表_返回空",
			cfg: config.LocalModelRoutingConfig{
				Enabled:                  true,
				Mode:                     config.AutopilotModeAuto,
				AllowLocalForTaskClasses: []string{"lightweight"},
			},
			taskClass: TaskClassSupervisor,
			wantEmpty: true,
		},
		{
			name: "taskClass在NeverDemoteTaskClasses_返回空_双重防御",
			cfg: config.LocalModelRoutingConfig{
				Enabled:                  true,
				Mode:                     config.AutopilotModeAuto,
				AllowLocalForTaskClasses: []string{"lightweight", "supervisor"},
				NeverDemoteTaskClasses:   []string{"supervisor"},
			},
			taskClass: TaskClassSupervisor,
			wantEmpty: true,
		},
		{
			name: "Mode=auto_taskClass=lightweight_只返回健康的运行时",
			cfg: config.LocalModelRoutingConfig{
				Enabled:                  true,
				Mode:                     config.AutopilotModeAuto,
				AllowLocalForTaskClasses: []string{"lightweight", "worker"},
				NeverDemoteTaskClasses:   []string{"supervisor", "vision", "long_context"},
			},
			taskClass: TaskClassLightweight,
			wantLen:   1,
			wantUIDs:  []string{"lr_healthy01"},
		},
		{
			name: "Mode=assist_taskClass=worker_只返回健康的运行时",
			cfg: config.LocalModelRoutingConfig{
				Enabled:                  true,
				Mode:                     config.AutopilotModeAuto,
				AllowLocalForTaskClasses: []string{"lightweight", "worker"},
				NeverDemoteTaskClasses:   []string{"supervisor", "vision"},
			},
			taskClass: TaskClassWorker,
			wantLen:   1,
			wantUIDs:  []string{"lr_healthy01"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CollectLocalCandidates(store, tt.cfg, tt.taskClass)

			if tt.wantEmpty {
				assert.Empty(t, result, "期望返回空列表")
				return
			}

			assert.Len(t, result, tt.wantLen, "期望条目数不匹配")
			gotUIDs := make([]string, len(result))
			for i, e := range result {
				gotUIDs[i] = e.RuntimeUID
			}
			// 不强制排序，只检查包含
			for _, wantUID := range tt.wantUIDs {
				assert.Contains(t, gotUIDs, wantUID, "期望包含 %s", wantUID)
			}
		})
	}
}

func TestCollectLocalCandidates_FieldMapping(t *testing.T) {
	store := newTestLocalRuntimeStore(t)

	rt := &LocalModelRuntimeProfile{
		RuntimeUID:        "lr_map01",
		Name:              "ollama-qwen2.5-coder",
		RuntimeType:       RuntimeTypeOllama,
		Status:            LocalRuntimeHealthy,
		ContextTokens:     32768,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
	}
	require.NoError(t, store.Upsert(rt))

	cfg := config.LocalModelRoutingConfig{
		Enabled:                  true,
		Mode:                     config.AutopilotModeAuto,
		AllowLocalForTaskClasses: []string{"worker"},
		NeverDemoteTaskClasses:   []string{"supervisor"},
	}

	result := CollectLocalCandidates(store, cfg, TaskClassWorker)
	require.Len(t, result, 1)

	entry := result[0]
	assert.Equal(t, "lr_map01", entry.RuntimeUID)
	assert.Equal(t, "ollama-qwen2.5-coder", entry.DisplayName)
	assert.Equal(t, true, entry.SupportsVision)
	assert.Equal(t, true, entry.SupportsToolCalls)
	assert.Equal(t, true, entry.SupportsReasoning)
	assert.Equal(t, 32768, entry.ContextWindowTokens)
	assert.Equal(t, 0.0, entry.EstimatedCost, "本地运行时 EstimatedCost 固定为 0")
}

func TestCollectLocalCandidates_DisplayNameFallback(t *testing.T) {
	store := newTestLocalRuntimeStore(t)

	// Name 为空的运行时
	rtNoName := &LocalModelRuntimeProfile{
		RuntimeUID:    "lr_noname01",
		Name:          "", // 无显示名
		RuntimeType:   RuntimeTypeOllama,
		Status:        LocalRuntimeHealthy,
		ContextTokens: 4096,
	}
	require.NoError(t, store.Upsert(rtNoName))

	cfg := config.LocalModelRoutingConfig{
		Enabled:                  true,
		Mode:                     config.AutopilotModeAuto,
		AllowLocalForTaskClasses: []string{"lightweight"},
	}

	result := CollectLocalCandidates(store, cfg, TaskClassLightweight)
	require.Len(t, result, 1)

	// 当 Name 为空时，DisplayName 应回退到 RuntimeUID
	assert.Equal(t, "lr_noname01", result[0].DisplayName)
}

func TestCollectLocalCandidates_EmptyStore(t *testing.T) {
	store := newTestLocalRuntimeStore(t) // 空 store

	cfg := config.LocalModelRoutingConfig{
		Enabled:                  true,
		Mode:                     config.AutopilotModeAuto,
		AllowLocalForTaskClasses: []string{"lightweight"},
	}

	result := CollectLocalCandidates(store, cfg, TaskClassLightweight)
	assert.Empty(t, result, "空 store 应返回空列表")
}

func TestCollectLocalCandidates_AllHealthy(t *testing.T) {
	store := newTestLocalRuntimeStore(t)

	// 插入多个健康运行时
	for i := 0; i < 3; i++ {
		rt := &LocalModelRuntimeProfile{
			RuntimeUID:    "lr_multi" + string(rune('a'+i)),
			Name:          "model-" + string(rune('a'+i)),
			RuntimeType:   RuntimeTypeOllama,
			Status:        LocalRuntimeHealthy,
			ContextTokens: 4096 * (i + 1),
		}
		require.NoError(t, store.Upsert(rt))
	}

	cfg := config.LocalModelRoutingConfig{
		Enabled:                  true,
		Mode:                     config.AutopilotModeAuto,
		AllowLocalForTaskClasses: []string{"lightweight"},
	}

	result := CollectLocalCandidates(store, cfg, TaskClassLightweight)
	assert.Len(t, result, 3, "3 个健康运行时应全部返回")
}

func TestStringSliceContains(t *testing.T) {
	tests := []struct {
		name   string
		list   []string
		target string
		want   bool
	}{
		{name: "存在", list: []string{"a", "b", "c"}, target: "b", want: true},
		{name: "不存在", list: []string{"a", "b", "c"}, target: "d", want: false},
		{name: "空列表", list: nil, target: "a", want: false},
		{name: "空目标", list: []string{"a", "b"}, target: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stringSliceContains(tt.list, tt.target))
		})
	}
}
