package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestChanStream_Iteration(t *testing.T) {
	ch := make(chan int, 3)
	ch <- 1
	ch <- 2
	ch <- 3
	close(ch)

	s := NewChanStream(context.Background(), ch, nil)
	var got []int
	for s.Next() {
		got = append(got, s.Current())
	}
	if err := s.Err(); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("unexpected values: %v", got)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Close 应幂等
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestChanStream_ContextCancelStopsNext(t *testing.T) {
	ch := make(chan int) // 永不写入
	parent, cancel := context.WithCancel(context.Background())
	s := NewChanStream(parent, ch, nil)

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	if s.Next() {
		t.Fatal("Next should return false after ctx cancel")
	}
	if !errors.Is(s.Err(), context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", s.Err())
	}
}

func TestChanStream_CloseCancelsCtx(t *testing.T) {
	ch := make(chan int)
	closeCalled := false
	s := NewChanStream(context.Background(), ch, func() error {
		closeCalled = true
		return nil
	})

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !closeCalled {
		t.Fatal("closeFn not invoked")
	}
	// Next 应立即返回 false 并报 ctx.Canceled
	if s.Next() {
		t.Fatal("Next should return false after Close")
	}
}

func TestChanStream_SetErrPropagates(t *testing.T) {
	ch := make(chan int, 1)
	ch <- 42
	s := NewChanStream(context.Background(), ch, nil)
	want := errors.New("upstream broke")
	s.SetErr(want)
	close(ch)

	for s.Next() {
		_ = s.Current()
	}
	if !errors.Is(s.Err(), want) {
		t.Fatalf("expected %v, got %v", want, s.Err())
	}
}
