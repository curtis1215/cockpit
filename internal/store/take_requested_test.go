package store

import (
	"sync"
	"sync/atomic"
	"testing"
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
