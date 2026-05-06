package providers

import (
	"context"
	"testing"
	"time"
)

func TestSendStreamEventReturnsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	eventChan := make(chan string)
	cancel()

	done := make(chan bool, 1)
	go func() {
		done <- sendStreamEvent(ctx, eventChan, "event: ping\n\n")
	}()

	select {
	case sent := <-done:
		if sent {
			t.Fatal("sendStreamEvent returned true after context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("sendStreamEvent blocked after context cancellation")
	}
}

func TestSendStreamErrorReturnsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	errChan := make(chan error)
	cancel()

	done := make(chan bool, 1)
	go func() {
		done <- sendStreamError(ctx, errChan, context.Canceled)
	}()

	select {
	case sent := <-done:
		if sent {
			t.Fatal("sendStreamError returned true after context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("sendStreamError blocked after context cancellation")
	}
}
