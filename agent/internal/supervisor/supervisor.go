package supervisor

import (
	"context"
	"os/exec"
	"time"
)

type Supervisor struct {
	cmd     string
	args    []string
	backoff time.Duration
	onStart func() // test hook
}

func New(cmd string, args []string, backoff time.Duration) *Supervisor {
	return &Supervisor{cmd: cmd, args: args, backoff: backoff}
}

// Run keeps the child process alive, restarting it after backoff when it exits,
// until ctx is cancelled. If cmd is empty it returns immediately (Beszel optional).
func (s *Supervisor) Run(ctx context.Context) {
	if s.cmd == "" {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		if s.onStart != nil {
			s.onStart()
		}
		c := exec.CommandContext(ctx, s.cmd, s.args...)
		_ = c.Start()
		_ = c.Wait()
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.backoff):
		}
	}
}
