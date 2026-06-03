package supervisor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestSuperviseRestarts(t *testing.T) {
	var starts int32
	ctx, cancel := context.WithCancel(context.Background())
	// 用一個會立刻結束的指令；supervisor 應重啟它幾次
	s := New("bash", []string{"-lc", "true"}, 50*time.Millisecond)
	s.onStart = func() { atomic.AddInt32(&starts, 1) }
	go s.Run(ctx)
	time.Sleep(300 * time.Millisecond)
	cancel()
	if atomic.LoadInt32(&starts) < 2 {
		t.Fatalf("expected multiple restarts, got %d", starts)
	}
}

func TestSuperviseNoCmdIsNoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New("", nil, 50*time.Millisecond)
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("empty supervisor should return immediately")
	}
}
