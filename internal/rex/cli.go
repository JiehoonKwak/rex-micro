package rex

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// CLI owns where the rex executable is. It is the one place that decides it;
// every rex call asks Path.
//
// The Rex server is itself the rex binary of the Rex in use (`rex server
// run`), so once a server answers, CLI follows that server's executable. The
// CLI then always speaks the server's own version, and when an app update
// moves Rex to a new folder, the next server is followed after Forget. No
// install path or version is written down here: before a server answers,
// Bootstrap names a rex to ask it with.
type CLI struct {
	// Fixed, when set (REX_MICRO_REX), is used as is and never followed.
	Fixed string
	// Bootstrap returns a rex to reach the server with, "" for none.
	Bootstrap func() string
	// Exe returns the executable of a running process.
	Exe func(pid int) (string, error)
	// Retry is how long to wait before asking an unanswering server again;
	// 0 means 5s.
	Retry time.Duration

	ask    sync.Mutex // one follow at a time; callers wait for its answer
	mu     sync.Mutex
	server string    // the followed server's executable, "" until one answered
	tried  time.Time // when following last failed
}

// Path returns the rex to run: the followed server's executable, else the
// bootstrap one, else "rex". While no server is followed, it asks the server
// (at most once per Retry); concurrent callers wait for that answer rather
// than start a long-lived rex on the bootstrap one. Code that must not wait,
// such as the daemon's loop, uses Known.
func (l *CLI) Path() string {
	if l.Fixed != "" {
		return l.Fixed
	}
	if exe := l.Known(); exe != "" {
		return exe
	}
	l.ask.Lock()
	defer l.ask.Unlock()
	if exe := l.Known(); exe != "" {
		return exe // another caller just followed it
	}
	retry := l.Retry
	if retry == 0 {
		retry = 5 * time.Second
	}
	boot := ""
	if l.Bootstrap != nil {
		boot = l.Bootstrap()
	}
	l.mu.Lock()
	ask := boot != "" && l.Exe != nil && time.Since(l.tried) >= retry
	l.mu.Unlock()
	if ask {
		exe := l.follow(boot)
		l.mu.Lock()
		if exe != "" {
			l.server, l.tried = exe, time.Time{}
		} else {
			l.tried = time.Now()
		}
		l.mu.Unlock()
		if exe != "" {
			return exe
		}
	}
	if boot == "" {
		return "rex"
	}
	return boot
}

// Now returns the rex to run without waiting for the server: the known one,
// else the bootstrap one, else "rex". A shell binding gets it, so a press
// while Rex is down is not held up by Path asking the server.
func (l *CLI) Now() string {
	if exe := l.Known(); exe != "" {
		return exe
	}
	if l.Bootstrap != nil {
		if boot := l.Bootstrap(); boot != "" {
			return boot
		}
	}
	return "rex"
}

// Known returns the rex Path would run without asking anything: the fixed
// one or the followed server's, "" when neither is known yet (or the
// followed one was removed by an update).
func (l *CLI) Known() string {
	if l.Fixed != "" {
		return l.Fixed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.server != "" {
		if _, err := os.Stat(l.server); err != nil {
			l.server = ""
		}
	}
	return l.server
}

// follow asks the server, through boot, for its process and returns that
// process's executable, or "" when no server answers. Only a server reached
// over its local socket runs on this Mac: REX_SERVER may name another host,
// whose pid means nothing here.
func (l *CLI) follow(boot string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := Command(ctx, boot, "server", "status", "--json").Output()
	if err != nil {
		return ""
	}
	var st struct {
		PID int `json:"pid"`
		You struct {
			Transport string `json:"transport"`
		} `json:"you"`
	}
	if json.Unmarshal(out, &st) != nil || st.PID <= 0 || st.You.Transport != "unix" {
		return ""
	}
	exe, err := l.Exe(st.PID)
	if err != nil {
		return ""
	}
	if _, err := os.Stat(exe); err != nil {
		return ""
	}
	return exe
}

// handOver looks for the server's own rex while a stream runs on the stand-in
// path, and calls switchTo once one is known that differs from it.
func (l *CLI) handOver(ctx context.Context, path string, switchTo func()) {
	every := l.Retry
	if every == 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if p := l.Path(); l.Known() != "" && p != path {
			switchTo()
			return
		}
	}
}

// Forget drops the followed server, so the next Path follows anew: call it
// when the server went away, since the next one may be a newer Rex.
func (l *CLI) Forget() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.server, l.tried = "", time.Time{}
	l.mu.Unlock()
}
