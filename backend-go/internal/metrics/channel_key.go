package metrics

// BuildLBChannelKey returns the single source of truth for LB metric keys.
//
// Format: "<kind>:<channelName>"
//   - kind separates entry points (messages / chat / responses / gemini / images);
//     it disambiguates same-named channels living in different upstream lists.
//   - channelName is the unique upstream name within a kind (validated by config).
//
// Both the writers (handlers/common stream first-token tracker, scheduler hooks
// emitting RecordTPS / RecordActiveConnDelta) and the readers
// (scheduler.LBMetricsProvider for FTTL/TPS/ActiveConn lookups) MUST go through
// this function so the channelKey written and the channelKey read stay aligned.
//
// kind is intentionally typed as string so this package stays free of any
// scheduler import; callers convert their kind enum (e.g. scheduler.ChannelKind)
// to string before invoking.
func BuildLBChannelKey(kind, channelName string) string {
	return kind + ":" + channelName
}
