package common

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// TestUsagePatternTaskDomain 钉住用量画像域维度握手：完成钩子从请求 context
// 取 RequestProfile 已推导的任务域，缺失时回退 general——按域渠道推荐
// 不再因画像恒记 general 而退化为全局推荐。
func TestUsagePatternTaskDomain(t *testing.T) {
	t.Run("无绑定画像回退 general", func(t *testing.T) {
		c := newAutopilotProfileTestContext(t, "/v1/messages", `{}`, nil)
		if got := usagePatternTaskDomain(c); got != autopilot.TaskDomainGeneral {
			t.Fatalf("usagePatternTaskDomain() = %q, want general", got)
		}
	})

	t.Run("携带画像时取已推导域", func(t *testing.T) {
		body := `{"model":"glm-5.2","system":"implement the function","messages":[{"role":"user","content":"写代码"}]}`
		c := newAutopilotProfileTestContext(t, "/v1/messages", body, nil)
		AttachAutopilotRequestProfile(c, scheduler.ChannelKindMessages, "glm-5.2", "completion", "session-test", []byte(body), 0)
		if got := usagePatternTaskDomain(c); got != autopilot.TaskDomainCoding {
			t.Fatalf("usagePatternTaskDomain() = %q, want coding", got)
		}
	})
}
