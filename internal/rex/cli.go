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

	mu     sync.Mutex
	server string    // the followed server's executable, "" until one answered
	tried  time.Time // when following last started without success
	asking bool      // a follow is under way; others use the bootstrap meanwhile
}

// Path returns the rex to run: the followed server's executable, else the
// bootstrap one, else "rex".
func (l *CLI) Path() string {
	if l.Fixed != "" {
		return l.Fixed
	}
	l.mu.Lock()
	if l.server != "" {
		if _, err := os.Stat(l.server); err == nil {
			defer l.mu.Unlock()
			return l.server
		}
		l.server = "" // removed by an update; follow the new server
	}
	retry := l.Retry
	if retry == 0 {
		retry = 5 * time.Second
	}
	ask := !l.asking && l.Exe != nil && time.Since(l.tried) >= retry
	if ask {
		l.asking, l.tried = true, time.Now()
	}
	l.mu.Unlock()

	boot := ""
	if l.Bootstrap != nil {
		boot = l.Bootstrap()
	}
	if ask {
		// Unlocked: asking takes up to seconds when Rex does not answer.
		exe := ""
		if boot != "" {
			exe = l.follow(boot)
		}
		l.mu.Lock()
		l.asking = false
		if exe != "" {
			l.server, l.tried = exe, time.Time{}
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

// Known returns the rex Path would run without asking anything: the fixed
// one or the followed server's, "" when neither is known yet.
func (l *CLI) Known() string {
	if l.Fixed != "" {
		return l.Fixed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
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
