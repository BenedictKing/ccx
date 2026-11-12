package handlers

import (
	"context"
	"testing"
	"time"
)

func TestCapabilityWorkerRegistersCancellationBeforeLaunch(t *testing.T) {
	resetCapabilityTestState(t)
	job := newCapabilityTestJob(0, "worker", "messages", "claude", nil, time.Second, 10)
	capabilityJobs.create(job)
	started := make(chan context.Context, 1)
	capabilityJobs.start(job.JobID, false, func(ctx context.Context) {
		started <- ctx
		<-ctx.Done()
	})
	cancel, ok := capabilityJobs.getCancelFunc(job.JobID)
	if !ok {
		t.Fatal("queued worker must already be cancellable")
	}
	cancel()
	select {
	case ctx := <-started:
		if ctx.Err() == nil {
			t.Fatal("cancellation before execution was lost")
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	cancelAndWaitCapabilityWorkers(t, capabilityJobs)
	capabilityJobs.RLock()
	defer capabilityJobs.RUnlock()
	if len(capabilityJobs.activeWorkers) != 0 {
		t.Fatal("completed worker was not removed")
	}
}

func TestCapabilityWorkerRespectsQueuedCancellation(t *testing.T) {
	resetCapabilityTestState(t)
	job := newCapabilityTestJob(0, "cancelled", "messages", "claude", nil, time.Second, 10)
	job.Lifecycle = CapabilityLifecycleCancelled
	capabilityJobs.create(job)
	started := make(chan error, 1)
	capabilityJobs.start(job.JobID, false, func(ctx context.Context) { started <- ctx.Err() })
	select {
	case err := <-started:
		if err != context.Canceled {
			t.Fatalf("queued cancellation = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
}

func TestCapabilityRetryDoesNotReplaceMainCancellation(t *testing.T) {
	resetCapabilityTestState(t)
	job := newCapabilityTestJob(0, "retry", "messages", "claude", nil, time.Second, 10)
	capabilityJobs.create(job)
	mainStopped := make(chan struct{})
	capabilityJobs.start(job.JobID, false, func(ctx context.Context) {
		<-ctx.Done()
		close(mainStopped)
	})
	retryStarted := make(chan context.Context, 1)
	capabilityJobs.start(job.JobID, true, func(ctx context.Context) {
		retryStarted <- ctx
		<-ctx.Done()
	})
	cancel, ok := capabilityJobs.getCancelFunc(job.JobID)
	if !ok {
		t.Fatal("main cancellation missing")
	}
	cancel()
	select {
	case <-mainStopped:
	case <-time.After(time.Second):
		t.Fatal("retry replaced main cancellation")
	}
	select {
	case ctx := <-retryStarted:
		if ctx.Err() != nil {
			t.Fatalf("main cancellation unexpectedly cancelled independent retry: %v", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("retry did not start")
	}
	// 清理必须同时取消并等待独立重试，不得留到下一个用例。
	cancelAndWaitCapabilityWorkers(t, capabilityJobs)
}
