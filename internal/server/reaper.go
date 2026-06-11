package server

import (
	"log"
	"time"
)

// markJobSeen 記錄 job 的最後一次 agent 活動（claim / log / control）。
func (s *Server) markJobSeen(id int64) { s.jobSeen.Store(id, time.Now()) }

// reapStaleJobs 把失聯超過 grace 的 running job 標成 failed，回傳收割數。
// 失聯定義：自最後一次 agent 活動（claim / log / control）起超過 grace 毫無
// 動靜。agent 執行任務時每 2 秒會打一次 control，所以 grace 分鐘級即可安全
// 判定 agent 從未收到 job（claim 給已斷線的連線）或已中斷。server 重啟後
// 沒有活動紀錄的 running job 以 boot 時間為基準，給執行中的 agent 重新累積
// 活動紀錄的機會。
func (s *Server) reapStaleJobs(now time.Time, grace time.Duration) int {
	ids, err := s.st.RunningJobIDs()
	if err != nil {
		log.Printf("reaper: list running jobs: %v", err)
		return 0
	}
	n := 0
	for _, id := range ids {
		seen := s.boot
		if v, ok := s.jobSeen.Load(id); ok {
			seen = v.(time.Time)
		}
		if now.Sub(seen) <= grace {
			continue
		}
		_ = s.st.AppendJobLog(id, "■ job 失聯逾時，server 自動標記失敗（agent 可能從未收到任務或已中斷）")
		if err := s.st.FinishJob(id, "failed", -1, ""); err != nil {
			log.Printf("reaper: finish job %d: %v", id, err)
			continue
		}
		s.jobSeen.Delete(id)
		log.Printf("reaper: job %d 失聯逾 %s，已標記 failed", id, grace)
		n++
	}
	return n
}

// StartJobReaper 啟動背景迴圈，定期收割失聯的 running job。
func (s *Server) StartJobReaper(interval, grace time.Duration) {
	go func() {
		for {
			time.Sleep(interval)
			s.reapStaleJobs(time.Now(), grace)
		}
	}()
}
