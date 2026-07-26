package autopilot

import (
	"testing"
)

// ── NormalizeEffortLevel 测试 ──

func TestNormalizeEffortLevel(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected EffortLevel
	}{
		// 标准值
		{"off", "off", EffortOff},
		{"minimal", "minimal", EffortMinimal},
		{"low", "low", EffortLow},
		{"medium", "medium", EffortMedium},
		{"high", "high", EffortHigh},
		{"max", "max", EffortMax},

		// 别名
		{"none alias", "none", EffortOff},
		{"disabled alias", "disabled", EffortOff},
		{"min alias", "min", EffortMinimal},
		{"med alias", "med", EffortMedium},
		{"default alias", "default", EffortMedium},
		{"xhigh alias", "xhigh", EffortMax},
		{"ultra alias", "ultra", EffortMax},

		// 大小写不敏感
		{"uppercase OFF", "OFF", EffortOff},
		{"mixed case High", "High", EffortHigh},
		{"uppercase XHIGH", "XHIGH", EffortMax},
		{"mixed Ultra", "Ultra", EffortMax},

		// trim
		{"trimmed medium", "  medium  ", EffortMedium},
		{"trimmed min", "  min  ", EffortMinimal},

		// 空串和未知值 → 空串（fail-open）
		{"empty string", "", ""},
		{"unknown value", "unknown_effort", ""},
		{"numeric", "123", ""},
		{"spaces only", "   ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeEffortLevel(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeEffortLevel(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// ── EffortLevelDistance 测试 ──

func TestEffortLevelDistance(t *testing.T) {
	tests := []struct {
		name     string
		a, b     EffortLevel
		expected int
	}{
		{"same level", EffortMedium, EffortMedium, 0},
		{"adjacent up", EffortLow, EffortMedium, 1},
		{"adjacent down", EffortHigh, EffortMedium, 1},
		{"two apart", EffortOff, EffortLow, 2},
		{"max distance", EffortOff, EffortMax, 5},
		{"reversed max", EffortMax, EffortOff, 5},
		{"minimal to high", EffortMinimal, EffortHigh, 3},

		// 无效输入
		{"empty a", "", EffortMedium, -1},
		{"empty b", EffortMedium, "", -1},
		{"both empty", "", "", -1},
		{"unknown a", EffortLevel("unknown"), EffortHigh, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EffortLevelDistance(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("EffortLevelDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.expected)
			}
		})
	}

	// 对称性
	t.Run("symmetric", func(t *testing.T) {
		levels := AllEffortLevels()
		for _, a := range levels {
			for _, b := range levels {
				d1 := EffortLevelDistance(a, b)
				d2 := EffortLevelDistance(b, a)
				if d1 != d2 {
					t.Errorf("EffortLevelDistance not symmetric: (%q,%q)=%d, (%q,%q)=%d",
						a, b, d1, b, a, d2)
				}
			}
		}
	})
}

// ── EffortLevelOrdinal 测试 ──

func TestEffortLevelOrdinal(t *testing.T) {
	tests := []struct {
		name     string
		level    EffortLevel
		expected int
	}{
		{"off", EffortOff, 0},
		{"minimal", EffortMinimal, 1},
		{"low", EffortLow, 2},
		{"medium", EffortMedium, 3},
		{"high", EffortHigh, 4},
		{"max", EffortMax, 5},
		{"empty string", "", -1},
		{"unknown", EffortLevel("unknown"), -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EffortLevelOrdinal(tt.level)
			if got != tt.expected {
				t.Errorf("EffortLevelOrdinal(%q) = %d, want %d", tt.level, got, tt.expected)
			}
		})
	}
}

// ── IsUnpinnedEffort 测试 ──

func TestIsUnpinnedEffort(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"default is unpinned", "default", true},
		{"unknown is unpinned", "unknown", true},
		{"empty is unpinned", "", true},
		{"whitespace only is unpinned", "   ", true},
		{"uppercase DEFAULT is unpinned", "DEFAULT", true},
		{"mixed case Unknown is unpinned", "Unknown", true},
		{"trimmed default is unpinned", "  default  ", true},

		{"high is pinned", "high", false},
		{"max is pinned", "max", false},
		{"xhigh is pinned", "xhigh", false},
		{"medium is pinned", "medium", false},
		{"low is pinned", "low", false},
		{"ultra is pinned", "ultra", false},
		{"off is pinned", "off", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsUnpinnedEffort(tt.input)
			if got != tt.expected {
				t.Errorf("IsUnpinnedEffort(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

// ── EffortFallbackConfidence 测试 ──

func TestEffortFallbackConfidence(t *testing.T) {
	tests := []struct {
		name     string
		distance int
		expected float64
	}{
		{"distance 0", 0, 1.0},
		{"distance 1", 1, 0.7},
		{"distance 2", 2, 0.4},
		{"distance 3", 3, 0.2},
		{"distance 4", 4, 0.2},
		{"distance 100", 100, 0.2},
		{"negative", -1, 0.2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EffortFallbackConfidence(tt.distance)
			if got != tt.expected {
				t.Errorf("EffortFallbackConfidence(%d) = %v, want %v", tt.distance, got, tt.expected)
			}
		})
	}

	// 单调递减
	t.Run("monotonic non-increasing", func(t *testing.T) {
		prev := EffortFallbackConfidence(0)
		for d := 1; d <= 5; d++ {
			curr := EffortFallbackConfidence(d)
			if curr > prev {
				t.Errorf("EffortFallbackConfidence not non-increasing: d=%d got %v > prev %v", d, curr, prev)
			}
			prev = curr
		}
	})
}
