package daemon

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
)

// streamRex streams like the real rex CLI: the first watch fails at once
// (Rex down), every later one stays connected until its context ends.
type streamRex struct {
	fakeRex
	mu      sync.Mutex
	starts  int
	running int
}

func (s *streamRex) Stream(ctx context.Context, _ []byte, line func([]byte), _ ...string) error {
	s.mu.Lock()
	s.starts++
	first := s.starts == 1
	if !first {
		s.running++
	}
	s.mu.Unlock()
	if first {
		return errors.New("rex is not running")
	}
	line([]byte(snapshotJSON))
	<-ctx.Done()
	s.mu.Lock()
	s.running--
	s.mu.Unlock()
	return ctx.Err()
}

func (s *streamRex) state() (starts, running int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts, s.running
}

func TestRexWatcherRestartsOnReloadAndStopsWithTheDaemon(t *testing.T) {
	rx := &streamRex{}
	_, cfgPath, stop := startDaemon(t, "[rex]\npoll = \"1h\"\n", rx)
	eventually(t, "first watch fails", func() bool { n, _ := rx.state(); return n == 1 })
	// Waiting an hour to retry; a changed [rex] section must not wait.
	if err := os.WriteFile(cfgPath, []byte("[rex]\npoll = \"1h\"\napp = \"M3\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	_ = Call("reload", &out)
	eventually(t, "watch restarted by reload", func() bool { n, r := rx.state(); return n == 2 && r == 1 })
	stop()
	if _, r := rx.state(); r != 0 {
		t.Fatal("the rex watcher outlived the daemon")
	}
}
