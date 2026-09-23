package common

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/racing"
	"github.com/gin-gonic/gin"
)

// 竞速标记诚实性：无对手分支的独占交付不得带 won/角色标记，
// 避免「竞速获胜」徽章与「竞速放大」筛选在非竞速请求上误报。

func newRacingMarkingContext(gate *racing.Gate, role string, branchID int) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := newBranchTestContext()
	if gate != nil {
		setBranchContext(c, gate, role, branchID)
	}
	return c
}

func TestRacingClaimClientCommit_SkipsWonMarkWithoutRival(t *testing.T) {
	gate := racing.NewGate()
	gate.RegisterCancel(0, func() {})
	c := newRacingMarkingContext(gate, racing.RolePrimary, 0)

	if !RacingClaimClientCommit(c) {
		t.Fatalf("无对手分支时 claim 应放行")
	}
	if _, ok := c.Get(racingOutcomeKey); ok {
		t.Fatalf("无对手分支时不得补记 won 标记")
	}
}

func TestRacingClaimClientCommit_MarksWonWithRival(t *testing.T) {
	gate := racing.NewGate()
	gate.RegisterCancel(0, func() {})
	gate.RegisterCancel(1, func() {})
	c := newRacingMarkingContext(gate, racing.RolePrimary, 0)

	if !RacingClaimClientCommit(c) {
		t.Fatalf("有对手分支时 claim 应放行")
	}
	if v, ok := c.Get(racingOutcomeKey); !ok || v != metrics.RacingStatusWon {
		t.Fatalf("有对手分支时应补记 won 标记: got=%v ok=%v", v, ok)
	}
}

func createRacingMarkingLog(t *testing.T, store *metrics.ChannelLogStore, metricsKey string, role string) string {
	t.Helper()
	return CreatePendingLog(
		store, metricsKey, 0, "ch", "m", "m", "", "",
		"sk", "https://example.com", "Messages", "",
		metrics.RequestSourceProxy, nil, "",
		WithRacingRole(role),
	)
}

func findRacingMarkingLog(t *testing.T, store *metrics.ChannelLogStore, metricsKey, requestID string) *metrics.ChannelLog {
	t.Helper()
	for _, log := range store.Get(metricsKey) {
		if log.RequestID == requestID {
			return log
		}
	}
	t.Fatalf("日志不存在: requestID=%s", requestID)
	return nil
}

func TestCompleteChannelLogWithRacingOutcome_ClearsRoleWhenNoRival(t *testing.T) {
	store := metrics.NewChannelLogStore()
	const key = "k-no-rival"
	requestID := createRacingMarkingLog(t, store, key, racing.RolePrimary)

	gate := racing.NewGate()
	gate.RegisterCancel(0, func() {})
	c := newRacingMarkingContext(nil, "", 0)
	c.Set(racing.ContextKeyGate, gate)

	CompleteChannelLogWithRacingOutcome(store, key, requestID, c)

	log := findRacingMarkingLog(t, store, key, requestID)
	if log.RacingRole != "" {
		t.Fatalf("未实际竞速时应清除角色标记: got=%q", log.RacingRole)
	}
	if log.RacingStatus != "" {
		t.Fatalf("未实际竞速时不得有竞速结果: got=%q", log.RacingStatus)
	}
}

func TestCompleteChannelLogWithRacingOutcome_MarksWonAndKeepsRole(t *testing.T) {
	store := metrics.NewChannelLogStore()
	const key = "k-won"
	requestID := createRacingMarkingLog(t, store, key, racing.RolePrimary)

	gate := racing.NewGate()
	gate.RegisterCancel(0, func() {})
	gate.RegisterCancel(1, func() {})
	c := newRacingMarkingContext(nil, "", 0)
	setBranchContext(c, gate, racing.RolePrimary, 0)
	SetChannelLogRacingWon(c)

	CompleteChannelLogWithRacingOutcome(store, key, requestID, c)

	log := findRacingMarkingLog(t, store, key, requestID)
	if log.RacingStatus != metrics.RacingStatusWon {
		t.Fatalf("竞速赢家应补记 won: got=%q", log.RacingStatus)
	}
	if log.RacingRole != racing.RolePrimary {
		t.Fatalf("赢家角色应保留: got=%q", log.RacingRole)
	}
}

func TestCompleteChannelLogWithRacingOutcome_KeepsRoleWhenRacedButLost(t *testing.T) {
	store := metrics.NewChannelLogStore()
	const key = "k-raced-lost"
	requestID := createRacingMarkingLog(t, store, key, racing.RoleShadow)

	gate := racing.NewGate()
	gate.RegisterCancel(0, func() {})
	gate.RegisterCancel(1, func() {})
	c := newRacingMarkingContext(nil, "", 0)
	c.Set(racing.ContextKeyGate, gate)

	CompleteChannelLogWithRacingOutcome(store, key, requestID, c)

	log := findRacingMarkingLog(t, store, key, requestID)
	if log.RacingRole != racing.RoleShadow {
		t.Fatalf("实际参与竞速的败者角色应保留: got=%q", log.RacingRole)
	}
	if log.RacingStatus != "" {
		t.Fatalf("败者结果由失败分类链写入，此处不得改动: got=%q", log.RacingStatus)
	}
}

func TestCompleteChannelLogWithRacingOutcome_NoopWithoutGate(t *testing.T) {
	store := metrics.NewChannelLogStore()
	const key = "k-no-gate"
	requestID := createRacingMarkingLog(t, store, key, racing.RoleShadow)

	c := newRacingMarkingContext(nil, "", 0)
	CompleteChannelLogWithRacingOutcome(store, key, requestID, c)

	log := findRacingMarkingLog(t, store, key, requestID)
	if log.RacingRole != racing.RoleShadow || log.RacingStatus != "" {
		t.Fatalf("未武装竞速时日志应保持原样: role=%q status=%q", log.RacingRole, log.RacingStatus)
	}
}
