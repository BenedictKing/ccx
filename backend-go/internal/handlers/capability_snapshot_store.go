package handlers

import (
	"net/http"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

type CapabilityProtocolJobRef struct {
	JobID       string `json:"jobId"`
	ChannelKind string `json:"channelKind"`
	ChannelID   int    `json:"channelId"`
}

type CapabilitySnapshot struct {
	IdentityKey         string                              `json:"identityKey"`
	SourceType          string                              `json:"sourceType"`
	ProtocolJobIDs      map[string]string                   `json:"protocolJobIds,omitempty"`
	ProtocolJobRefs     map[string]CapabilityProtocolJobRef `json:"protocolJobRefs,omitempty"`
	Tests               []CapabilityProtocolJobResult       `json:"tests"`
	CompatibleProtocols []string                            `json:"compatibleProtocols"`
	TotalDuration       int64                               `json:"totalDuration"`
	Progress            CapabilityTestJobProgress           `json:"progress"`
	Lifecycle           CapabilityLifecycle                 `json:"lifecycle"`
	Outcome             CapabilityOutcome                   `json:"outcome"`
	UpdatedAt           string                              `json:"updatedAt"`
}

type capabilitySnapshotStore struct {
	sync.RWMutex
	snapshots map[string]*CapabilitySnapshot
	ttl       time.Duration
}

const capabilitySnapshotTTL = 2 * time.Hour

var capabilitySnapshots = newCapabilitySnapshotStoreWithGC()

func newCapabilitySnapshotStore() *capabilitySnapshotStore {
	return &capabilitySnapshotStore{
		snapshots: make(map[string]*CapabilitySnapshot),
		ttl:       capabilitySnapshotTTL,
	}
}

func newCapabilitySnapshotStoreWithGC() *capabilitySnapshotStore {
	store := newCapabilitySnapshotStore()
	go store.gcLoop()
	return store
}

func cloneCapabilitySnapshot(snapshot *CapabilitySnapshot) *CapabilitySnapshot {
	if snapshot == nil {
		return nil
	}
	cloned := *snapshot
	cloned.ProtocolJobIDs = make(map[string]string, len(snapshot.ProtocolJobIDs))
	for protocol, jobID := range snapshot.ProtocolJobIDs {
		cloned.ProtocolJobIDs[protocol] = jobID
	}
	cloned.ProtocolJobRefs = make(map[string]CapabilityProtocolJobRef, len(snapshot.ProtocolJobRefs))
	for protocol, jobRef := range snapshot.ProtocolJobRefs {
		cloned.ProtocolJobRefs[protocol] = jobRef
	}
	cloned.Tests = make([]CapabilityProtocolJobResult, len(snapshot.Tests))
	for i, test := range snapshot.Tests {
		cloned.Tests[i] = test
		cloned.Tests[i].ModelResults = append([]CapabilityModelJobResult(nil), test.ModelResults...)
	}
	cloned.CompatibleProtocols = append([]string(nil), snapshot.CompatibleProtocols...)
	return &cloned
}

func snapshotFromCapabilityJob(identityKey string, job *CapabilityTestJob) *CapabilitySnapshot {
	if job == nil {
		return nil
	}
	protocolJobIDs := make(map[string]string, len(job.Tests))
	protocolJobRefs := make(map[string]CapabilityProtocolJobRef, len(job.Tests))
	for _, test := range job.Tests {
		if test.Protocol == "" || job.JobID == "" {
			continue
		}
		protocolJobIDs[test.Protocol] = job.JobID
		protocolJobRefs[test.Protocol] = CapabilityProtocolJobRef{
			JobID:       job.JobID,
			ChannelKind: job.ChannelKind,
			ChannelID:   job.ChannelID,
		}
	}
	return &CapabilitySnapshot{
		IdentityKey:         identityKey,
		SourceType:          job.SourceType,
		ProtocolJobIDs:      protocolJobIDs,
		ProtocolJobRefs:     protocolJobRefs,
		Tests:               append([]CapabilityProtocolJobResult(nil), job.Tests...),
		CompatibleProtocols: append([]string(nil), job.CompatibleProtocols...),
		TotalDuration:       job.TotalDuration,
		Progress:            job.Progress,
		Lifecycle:           job.Lifecycle,
		Outcome:             job.Outcome,
		UpdatedAt:           job.UpdatedAt,
	}
}

func (s *capabilitySnapshotStore) update(identityKey string, updater func(snapshot *CapabilitySnapshot)) *CapabilitySnapshot {
	s.Lock()
	defer s.Unlock()

	snapshot, ok := s.snapshots[identityKey]
	if !ok {
		snapshot = &CapabilitySnapshot{IdentityKey: identityKey}
		s.snapshots[identityKey] = snapshot
	}

	updater(snapshot)
	if snapshot.UpdatedAt == "" {
		snapshot.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	}
	return cloneCapabilitySnapshot(snapshot)
}

func (s *capabilitySnapshotStore) replaceFromJob(identityKey string, job *CapabilityTestJob) *CapabilitySnapshot {
	if job == nil {
		return nil
	}
	s.Lock()
	defer s.Unlock()

	existing, hasExisting := s.snapshots[identityKey]
	if !hasExisting {
		existing = &CapabilitySnapshot{
			IdentityKey:     identityKey,
			ProtocolJobIDs:  make(map[string]string),
			ProtocolJobRefs: make(map[string]CapabilityProtocolJobRef),
		}
		s.snapshots[identityKey] = existing
	}
	if existing.ProtocolJobIDs == nil {
		existing.ProtocolJobIDs = make(map[string]string)
	}
	if existing.ProtocolJobRefs == nil {
		existing.ProtocolJobRefs = make(map[string]CapabilityProtocolJobRef)
	}

	// 按协议合并 ProtocolJobIDs
	for _, test := range job.Tests {
		if test.Protocol == "" || job.JobID == "" {
			continue
		}
		existing.ProtocolJobIDs[test.Protocol] = job.JobID
		existing.ProtocolJobRefs[test.Protocol] = CapabilityProtocolJobRef{
			JobID:       job.JobID,
			ChannelKind: job.ChannelKind,
			ChannelID:   job.ChannelID,
		}
	}

	// 按协议合并 Tests：同协议以最新 job 数据覆盖
	mergedTests := make([]CapabilityProtocolJobResult, len(existing.Tests))
	copy(mergedTests, existing.Tests)
	for _, jobTest := range job.Tests {
		found := false
		for i, existingTest := range mergedTests {
			if existingTest.Protocol == jobTest.Protocol {
				mergedTests[i] = jobTest
				found = true
				break
			}
		}
		if !found {
			mergedTests = append(mergedTests, jobTest)
		}
	}
	existing.Tests = mergedTests

	existing.CompatibleProtocols = mergeSnapshotCompatibleProtocols(existing.Tests)
	existing.TotalDuration = maxInt64(existing.TotalDuration, job.TotalDuration)
	existing.Progress = mergeSnapshotProgress(existing.Tests)
	existing.Lifecycle = mergeSnapshotLifecycle(existing.Tests)
	existing.Outcome = mergeSnapshotOutcome(existing.Tests, existing.Lifecycle)
	existing.SourceType = job.SourceType
	existing.UpdatedAt = job.UpdatedAt
	if existing.UpdatedAt == "" {
		existing.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	}

	return cloneCapabilitySnapshot(existing)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func mergeSnapshotCompatibleProtocols(tests []CapabilityProtocolJobResult) []string {
	compatible := make([]string, 0)
	for i := range tests {
		if tests[i].Outcome == CapabilityOutcomeSuccess || tests[i].Outcome == CapabilityOutcomePartial {
			compatible = append(compatible, tests[i].Protocol)
		}
	}
	return compatible
}

func mergeSnapshotProgress(tests []CapabilityProtocolJobResult) CapabilityTestJobProgress {
	progress := CapabilityTestJobProgress{}
	for _, test := range tests {
		for _, modelResult := range test.ModelResults {
			progress.TotalModels++
			switch modelResult.Status {
			case CapabilityModelStatusQueued:
				progress.QueuedModels++
			case CapabilityModelStatusRunning:
				progress.RunningModels++
			case CapabilityModelStatusSuccess:
				progress.SuccessModels++
				progress.CompletedModels++
			case CapabilityModelStatusFailed:
				progress.FailedModels++
				progress.CompletedModels++
			case CapabilityModelStatusSkipped:
				progress.SkippedModels++
				progress.CompletedModels++
			}
		}
	}
	return progress
}

func mergeSnapshotLifecycle(tests []CapabilityProtocolJobResult) CapabilityLifecycle {
	allTerminal := true
	allCancelled := len(tests) > 0

	for _, test := range tests {
		if test.Lifecycle == CapabilityLifecycleActive {
			return CapabilityLifecycleActive
		}
		if test.Lifecycle == CapabilityLifecyclePending {
			allTerminal = false
		}
		if test.Lifecycle != CapabilityLifecycleCancelled {
			allCancelled = false
		}
	}

	if allCancelled {
		return CapabilityLifecycleCancelled
	}
	if !allTerminal {
		return CapabilityLifecyclePending
	}
	return CapabilityLifecycleDone
}

func mergeSnapshotOutcome(tests []CapabilityProtocolJobResult, lifecycle CapabilityLifecycle) CapabilityOutcome {
	switch lifecycle {
	case CapabilityLifecycleCancelled:
		return CapabilityOutcomeCancelled
	case CapabilityLifecycleActive, CapabilityLifecyclePending:
		anySuccess := false
		for i := range tests {
			if tests[i].Outcome == CapabilityOutcomeSuccess || tests[i].Outcome == CapabilityOutcomePartial {
				anySuccess = true
				break
			}
		}
		if anySuccess {
			return CapabilityOutcomePartial
		}
		return CapabilityOutcomeUnknown
	case CapabilityLifecycleDone:
		anyPartial := false
		anySuccess := false
		anyFailed := false
		for i := range tests {
			switch tests[i].Outcome {
			case CapabilityOutcomePartial:
				anyPartial = true
			case CapabilityOutcomeSuccess:
				anySuccess = true
			case CapabilityOutcomeFailed:
				anyFailed = true
			}
		}
		switch {
		case anyPartial:
			return CapabilityOutcomePartial
		case anySuccess:
			return CapabilityOutcomeSuccess
		case anyFailed:
			return CapabilityOutcomeFailed
		default:
			return CapabilityOutcomeUnknown
		}
	}
	return CapabilityOutcomeUnknown
}

func (s *capabilitySnapshotStore) get(identityKey string) (*CapabilitySnapshot, bool) {
	s.RLock()
	defer s.RUnlock()
	snapshot, ok := s.snapshots[identityKey]
	if !ok {
		return nil, false
	}
	return cloneCapabilitySnapshot(snapshot), true
}

func (s *capabilitySnapshotStore) gcLoop() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.gc()
	}
}

func (s *capabilitySnapshotStore) gc() {
	cutoff := time.Now().Add(-s.ttl)
	s.Lock()
	defer s.Unlock()
	for identityKey, snapshot := range s.snapshots {
		if snapshot == nil {
			delete(s.snapshots, identityKey)
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, snapshot.UpdatedAt)
		if err != nil || updatedAt.Before(cutoff) {
			delete(s.snapshots, identityKey)
		}
	}
}

func resolveCapabilityIdentityKey(channel *config.UpstreamConfig) string {
	if channel == nil {
		return ""
	}
	baseURL := ""
	if len(channel.GetAllBaseURLs()) > 0 {
		baseURL = channel.GetAllBaseURLs()[0]
	}
	apiKey := ""
	if len(channel.APIKeys) > 0 {
		apiKey = channel.APIKeys[0]
	} else if len(channel.DisabledAPIKeys) > 0 {
		apiKey = channel.DisabledAPIKeys[0].Key
	}
	return metrics.GenerateMetricsIdentityKey(baseURL, apiKey, channel.ServiceType)
}

func capabilityJobMatchesChannel(job *CapabilityTestJob, channel *config.UpstreamConfig, channelKind string, channelID int) bool {
	if job == nil {
		return false
	}
	if job.ChannelKind != channelKind {
		return false
	}
	if job.ChannelID == channelID {
		return true
	}
	if channel == nil {
		return false
	}
	identityKey := resolveCapabilityIdentityKey(channel)
	return identityKey != "" && job.IdentityKey == identityKey
}

func GetCapabilitySnapshot(cfgManager *config.ConfigManager, channelKind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseCapabilityChannelID(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
			return
		}

		channel, getErr := getCapabilityTestChannel(cfgManager, channelKind, id)
		if getErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "channel not found"})
			return
		}

		identityKey := resolveCapabilityIdentityKey(channel)
		snapshot, ok := capabilitySnapshots.get(identityKey)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Capability snapshot not found"})
			return
		}
		c.JSON(http.StatusOK, snapshot)
	}
}
