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
	tried  time.Time // when following last failed
}

// Path returns the rex to run: the followed server's executable, else the
// bootstrap one, else "rex".
func (l *CLI) Path() string {
	if l.Fixed != "" {
		return l.Fixed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.server != "" {
		if _, err := os.Stat(l.server); err == nil {
			return l.server
		}
		l.server = "" // removed by an update; follow the new server
	}
	boot := ""
	if l.Bootstrap != nil {
		boot = l.Bootstrap()
	}
	retry := l.Retry
	if retry == 0 {
		retry = 5 * time.Second
	}
	if boot != "" && l.Exe != nil && time.Since(l.tried) >= retry {
		if exe := l.follow(boot); exe != "" {
			l.server = exe
			return exe
		}
		l.tried = time.Now()
	}
	if boot == "" {
		return "rex"
	}
	return boot
}

// follow asks the local server, through boot, for its process and returns
// that process's executable, or "" when no server answers.
func (l *CLI) follow(boot string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := Command(ctx, boot, "server", "status", "--json").Output()
	if err != nil {
		return ""
	}
	var st struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(out, &st) != nil || st.PID <= 0 {
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

// Following reports the server executable being followed, "" for none.
func (l *CLI) Following() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.server
}
