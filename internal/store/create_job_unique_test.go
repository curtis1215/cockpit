package store

import (
	"fmt"
	"sync"
	"testing"
)

// 同一個 (software, machine) 並發建立 job，每一輪只能有一筆 queued 被插入。
func TestCreateJobUniqueConcurrent(t *testing.T) {
	s := open(t)
	for round := 0; round < 30; round++ {
		machine := fmt.Sprintf("mac%d", round)
		var wg sync.WaitGroup
		ids := make(chan int64, 10)
		start := make(chan struct{})
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				id, err := s.CreateJobUnique("cc", machine, "command", "")
				if err != nil {
					t.Error(err)
				}
				ids <- id
			}()
		}
		close(start)
		wg.Wait()
		close(ids)

		created := 0
		for id := range ids {
			if id != 0 {
				created++
			}
		}
		if created != 1 {
			t.Fatalf("round %d: created %d jobs, want 1", round, created)
		}
		var queued int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE machine=? AND status='queued'`, machine).Scan(&queued); err != nil {
			t.Fatal(err)
		}
		if queued != 1 {
			t.Fatalf("round %d: queued jobs=%d, want 1", round, queued)
		}
	}
}
