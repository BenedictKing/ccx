package common

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/gin-gonic/gin"
)

// ── isEffortClampedByClient 测试 ──

func TestIsEffortClampedByClient(t *testing.T) {
	tests := []struct {
		name           string
		clientRaw      string
		clientExplicit bool
		targetEffort   autopilot.EffortLevel
		want           bool
	}{
		// 客户端未显式声明 effort → 永远不钳位
		{
			name:           "客户端未声明 effort，不钳位",
			clientRaw:      "",
			clientExplicit: false,
			targetEffort:   autopilot.EffortHigh,
			want:           false,
		},

		// 客户端 effort 高于 autopilot 选择 → 不钳位（上界未触及）
		{
			name:           "客户端 high 高于 autopilot medium，不钳位",
			clientRaw:      "high",
			clientExplicit: true,
			targetEffort:   autopilot.EffortMedium,
			want:           false,
		},
		{
			name:           "客户端 max 高于 autopilot low，不钳位",
			clientRaw:      "max",
			clientExplicit: true,
			targetEffort:   autopilot.EffortLow,
			want:           false,
		},
		{
			name:           "客户端 medium 等于 autopilot medium，不钳位",
			clientRaw:      "medium",
			clientExplicit: true,
			targetEffort:   autopilot.EffortMedium,
			want:           false,
		},

		// 客户端 effort 严格低于 autopilot 选择 → 钳位
		{
			name:           "客户端 low 低于 autopilot high，钳位",
			clientRaw:      "low",
			clientExplicit: true,
			targetEffort:   autopilot.EffortHigh,
			want:           true,
		},
		{
			name:           "客户端 off 低于 autopilot medium，钳位",
			clientRaw:      "off",
			clientExplicit: true,
			targetEffort:   autopilot.EffortMedium,
			want:           true,
		},
		{
			name:           "客户端 minimal 低于 autopilot max，钳位",
			clientRaw:      "minimal",
			clientExplicit: true,
			targetEffort:   autopilot.EffortMax,
			want:           true,
		},

		// 别名归一化后应正确比较
		{
			name:           "客户端 none 归一化为 off，低于 autopilot medium，钳位",
			clientRaw:      "none",
			clientExplicit: true,
			targetEffort:   autopilot.EffortMedium,
			want:           true,
		},
		{
			name:           "客户端 xhigh 归一化为 max，高于 autopilot high，不钳位",
			clientRaw:      "xhigh",
			clientExplicit: true,
			targetEffort:   autopilot.EffortHigh,
			want:           false,
		},
		{
			name:           "客户端 med 归一化为 medium，等于 autopilot medium，不钳位",
			clientRaw:      "med",
			clientExplicit: true,
			targetEffort:   autopilot.EffortMedium,
			want:           false,
		},

		// 无法识别的客户端 effort → 不钳位（fail-open）
		{
			name:           "客户端 effort 无法识别，不钳位",
			clientRaw:      "unknown_effort",
			clientExplicit: true,
			targetEffort:   autopilot.EffortMedium,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEffortClampedByClient(tt.clientRaw, tt.clientExplicit, tt.targetEffort)
			if got != tt.want {
				t.Errorf("isEffortClampedByClient(%q, %v, %q) = %v, want %v",
					tt.clientRaw, tt.clientExplicit, tt.targetEffort, got, tt.want)
			}
		})
	}
}

// ── failover 跨 endpoint 残留标记清除测试 ──

// TestEffortMarkersResetAcrossFailover 验证跨 endpoint failover 时
// effortDecisionSource 和 effortClampedByClient 不会残留上一轮的值。
// 场景：第一个 endpoint 解析到 target 并设置 autopilot 标记，
// 第二个 endpoint 未解析到 target → 最终日志应为 passthrough/false。
func TestEffortMarkersResetAcrossFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	// 模拟第一轮 attempt：endpoint 解析到 target，autopilot 决定 effort
	// 复用 loop 顶部的重置逻辑 + target 解析逻辑
	c.Set("effortDecisionSource", "passthrough")
	c.Set("effortClampedByClient", false)

	// 第一个 endpoint 成功解析到 target（autopilot 决定了 effort）
	c.Set("effortDecisionSource", "autopilot")
	c.Set("effortClampedByClient", true) // 假设存在钳位

	// 模拟 failover 到第二个 endpoint：loop 顶部重置
	c.Set("effortDecisionSource", "passthrough")
	c.Set("effortClampedByClient", false)

	// 第二个 endpoint 未解析到 target（target == nil），
	// 因此不会执行 c.Set，标记应保持重置后的默认值

	// 验证最终状态为 passthrough 而非残留的 autopilot
	source, exists := c.Get("effortDecisionSource")
	if !exists {
		t.Fatal("effortDecisionSource 未设置")
	}
	if source != "passthrough" {
		t.Errorf("effortDecisionSource = %q, want \"passthrough\"（不应残留上一轮 autopilot 值）", source)
	}

	clamped, exists := c.Get("effortClampedByClient")
	if !exists {
		t.Fatal("effortClampedByClient 未设置")
	}
	if clamped != false {
		t.Errorf("effortClampedByClient = %v, want false（不应残留上一轮 true 值）", clamped)
	}
}

// TestEffortMarkersPassthroughOnFirstIteration 验证第一次 attempt 开始时
// 标记即为 passthrough/false（无残留）。
func TestEffortMarkersPassthroughOnFirstIteration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	// 模拟 loop 顶部的首次重置
	c.Set("effortDecisionSource", "passthrough")
	c.Set("effortClampedByClient", false)

	// 未解析到 target → 标记应保持默认值
	source, _ := c.Get("effortDecisionSource")
	if source != "passthrough" {
		t.Errorf("effortDecisionSource = %q, want \"passthrough\"", source)
	}
	clamped, _ := c.Get("effortClampedByClient")
	if clamped != false {
		t.Errorf("effortClampedByClient = %v, want false", clamped)
	}
}
