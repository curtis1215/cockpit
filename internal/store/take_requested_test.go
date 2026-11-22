package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 同一個請求只能被取走一次：多個並發的 Take 只能有一個回傳 true。
func TestTakeRequestedAtomic(t *testing.T) {
	s := open(t)
	cases := []struct {
		name string
		set  func(string) error
		take func(string) bool
	}{
		{"check", s.SetCheckRequested, s.TakeCheckRequested},
		{"upgrade", s.SetUpgradeRequested, s.TakeUpgradeRequested},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.take("nobody") {
				t.Fatal("no record: want false")
			}
			for round := 0; round < 50; round++ {
				if err := c.set("mac"); err != nil {
					t.Fatal(err)
				}
				var wg sync.WaitGroup
				var got int32
				start := make(chan struct{})
				for i := 0; i < 10; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						if c.take("mac") {
							atomic.AddInt32(&got, 1)
						}
					}()
				}
				close(start)
				wg.Wait()
				if got != 1 {
					t.Fatalf("round %d: %d takes returned true, want 1", round, got)
				}
			}
		})
	}
}

// 沒有待處理請求時，Take 只能讀旗標，不該去搶寫入鎖：另一條連線持有寫入交易時仍須立即回傳 false。
// 修正前會先執行 UPDATE，搶不到鎖就等滿 busy_timeout（5 秒）才放棄。
func TestTakeRequestedNoPendingDoesNotWaitForWriteLock(t *testing.T) {
	s := open(t)
	if err := s.SetCheckRequested("mac"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUpgradeRequested("mac"); err != nil {
		t.Fatal(err)
	}
	if !s.TakeCheckRequested("mac") || !s.TakeUpgradeRequested("mac") {
		t.Fatal("setup: pending request should be taken")
	}

	ctx := context.Background()
	c, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.ExecContext(ctx, "ROLLBACK")
		c.Close()
	})
	if _, err := c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		take func(string) bool
	}{
		{"check", s.TakeCheckRequested},
		{"upgrade", s.TakeUpgradeRequested},
	} {
		start := time.Now()
		if tc.take("mac") {
			t.Fatalf("%s: no pending request, want false", tc.name)
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("%s: no-pending Take took %v while another writer held the lock; it should only read the flag", tc.name, d)
		}
	}
}
