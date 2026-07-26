package autopilot

// ResolvedRouteTarget is the atomic result of one autopilot routing decision.
// Model and Effort are decided together; never split.
type ResolvedRouteTarget struct {
	Model         string      // target model ID
	Effort        EffortLevel // target effort level; empty = don't rewrite effort
	EffortDecided bool        // true if Autopilot decided the effort (not passthrough)
	Reason        string      // decision reason for trace and logs
}
