package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helperArgs are the arguments the daemon runs its own executable with.
var helperArgs = map[string]bool{"_overlay": true, "_trusted": true}

const spawnLog = "REX_MICRO_TEST_SELF_SPAWN_LOG"

// TestMain keeps the tests from starting themselves. Under `go test` the
// daemon's own executable is this test binary, so a daemon that runs
// `<self> _overlay` would run every test again, each starting more
// (2026-11-09: 788 processes, two kernel panics). A copy started that way
// records itself and exits at once, and the run fails.
func TestMain(m *testing.M) {
	for _, a := range os.Args[1:] {
		if helperArgs[a] {
			if f, err := os.OpenFile(os.Getenv(spawnLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
				f.Close()
			}
			os.Exit(3)
		}
	}
	dir, err := os.MkdirTemp("", "rex-micro-daemon-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log := filepath.Join(dir, "self-spawn.log")
	os.Setenv(spawnLog, log)
	code := m.Run()
	if b, err := os.ReadFile(log); err == nil && len(b) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: the tests started their own binary as a helper:\n%s", b)
		code = 1
	}
	os.RemoveAll(dir)
	os.Exit(code)
}
