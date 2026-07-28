package common

import (
	"net/http"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// 422 可达性回归：兼容性学习块曾被嵌套在 `resp.StatusCode == 400` 守卫内，
// 导致声称支持的 422 分支永远不可达。这里从两侧钉住修复后的行为：
//
//  1. 信号识别层确实接受 422（CompatTraitFromError）；
//  2. failover 分类层不会把 422 提前吃掉（正常模式下 422 不 failover，
//     会继续往下走到学习块），所以学习块能真正拿到 422。
//
// 无法在单测里完整驱动真实 failover（需要 scheduler/metrics 全套依赖），
// 因此按环节分别验证，与 TestCompatLearningFlowDeveloperRole 的做法一致。

func TestCompatTraitFromErrorAcceptsUnprocessableEntity(t *testing.T) {
	body := []byte(`{"error":{"message":"messages[0].role: unknown variant ` + "`developer`" +
		`, expected one of ` + "`system`" + `, ` + "`user`" + `"}}`)

	for _, statusCode := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
		signal := CompatTraitFromError(statusCode, body, CompatSignalContext{HasDeveloperRole: true})
		if signal == nil {
			t.Fatalf("statusCode=%d 应识别出兼容性信号", statusCode)
		}
		if signal.Trait != config.TraitDowngradeDeveloperRole {
			t.Errorf("statusCode=%d Trait = %q, want %q", statusCode, signal.Trait, config.TraitDowngradeDeveloperRole)
		}
	}
}

func TestUnprocessableEntityReachesCompatLearning(t *testing.T) {
	// 正常模式下 422 被分类为「不 failover」：不会在 shouldFailover 分支提前 continue，
	// 因此会向下流到兼容性学习块。这是 422 分支可达的前提条件。
	body := []byte(`{"error":{"message":"messages[0].role: unknown variant ` + "`developer`" + `"}}`)

	shouldFailover, _ := ShouldRetryWithNextKeyWithLogTag(
		http.StatusUnprocessableEntity, body, false, "Responses", "")
	if shouldFailover {
		t.Fatal("422 在正常模式下不应 failover，否则会在学习块之前 continue")
	}
}
