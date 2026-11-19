package rex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeBoot writes a rex that answers `server status --json` with the pid in
// pidFile (or fails when it is missing) and counts its calls in calls.
func fakeBoot(t *testing.T, dir string) (boot, pidFile, calls string) {
	t.Helper()
	boot, pidFile, calls = filepath.Join(dir, "rex"), filepath.Join(dir, "pid"), filepath.Join(dir, "calls")
	script := fmt.Sprintf("#!/bin/sh\necho x >> %q\n[ \"$2 $3 $4\" = \"server status --json\" ] || exit 2\n[ -f %q ] || exit 1\nprintf '{\"pid\":%%s,\"you\":{\"transport\":\"unix\"}}' \"$(cat %q)\"\n", calls, pidFile, pidFile)
	if err := os.WriteFile(boot, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return
}

func writeFile(t *testing.T, path, text string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func count(path string) int {
	b, _ := os.ReadFile(path)
	return strings.Count(string(b), "x")
}

func TestCLIFollowsTheRunningServersExecutable(t *testing.T) {
	dir := t.TempDir()
	boot, pidFile, calls := fakeBoot(t, dir)
	exes := map[int]string{
		41: writeFile(t, filepath.Join(dir, "rex-2044"), ""),
		42: writeFile(t, filepath.Join(dir, "rex-2045"), ""),
	}
	l := &CLI{Bootstrap: func() string { return boot }, Exe: func(pid int) (string, error) {
		if p, ok := exes[pid]; ok {
			return p, nil
		}
		return "", fmt.Errorf("no pid %d", pid)
	}, Retry: time.Hour}

	// No server: the bootstrap rex, and no second ask before Retry.
	if got := l.Path(); got != boot {
		t.Fatalf("no server: %s", got)
	}
	l.Path()
	if n := count(calls); n != 1 {
		t.Fatalf("asked an absent server %d times within Retry", n)
	}

	// A server answers: its own executable, asked once.
	writeFile(t, pidFile, "41")
	l.Forget()
	if got := l.Path(); got != exes[41] {
		t.Fatalf("server 41: %s", got)
	}
	before := count(calls)
	l.Path()
	if count(calls) != before {
		t.Fatal("asked the server again while following it")
	}

	// The server restarts as a newer Rex: Forget follows the new one.
	writeFile(t, pidFile, "42")
	l.Forget()
	if got := l.Path(); got != exes[42] {
		t.Fatalf("after restart: %s", got)
	}

	// An update removed the followed executable: follow anew, no Forget.
	writeFile(t, pidFile, "41")
	_ = os.Remove(exes[42])
	if got := l.Path(); got != exes[41] {
		t.Fatalf("after removal: %s", got)
	}
}

func TestStreamEndForgetsTheServer(t *testing.T) {
	dir := t.TempDir()
	boot, pidFile, _ := fakeBoot(t, dir)
	exe := writeFile(t, filepath.Join(dir, "rex-server"), "#!/bin/sh\nexit 0\n")
	writeFile(t, pidFile, "7")
	l := &CLI{Bootstrap: func() string { return boot }, Exe: func(int) (string, error) { return exe, nil }}
	if got := l.Path(); got != exe {
		t.Fatalf("not following: %s", got)
	}
	_ = ExecRunner{CLI: l}.Stream(t.Context(), nil, func([]byte) {}, "-S", "m1", "do", "-")
	if l.Known() != exe {
		t.Fatal("another host's stream ending dropped this Mac's server")
	}
	_ = ExecRunner{CLI: l}.Stream(t.Context(), nil, func([]byte) {}, "do", "-")
	if l.Known() != "" {
		t.Fatal("a stream that ended (the server went away) kept the old server")
	}
}

// REX_SERVER can point rex at another host; its pid is not a process here.
func TestCLIFollowsOnlyALocalServer(t *testing.T) {
	dir := t.TempDir()
	boot := writeFile(t, filepath.Join(dir, "rex"), "#!/bin/sh\nprintf '{\"pid\":7,\"you\":{\"transport\":\"tailscale\"}}'\n")
	l := &CLI{Bootstrap: func() string { return boot }, Exe: func(int) (string, error) {
		t.Fatal("looked up a remote server's pid on this Mac")
		return "", nil
	}}
	if got := l.Path(); got != boot {
		t.Fatalf("remote server followed: %s", got)
	}
}

func TestCLIFixedIsNeverFollowed(t *testing.T) {
	l := &CLI{Fixed: "/opt/rex", Bootstrap: func() string { t.Fatal("bootstrap called"); return "" }}
	if got := l.Path(); got != "/opt/rex" {
		t.Fatal(got)
	}
}

func TestCLIWithoutAnyRex(t *testing.T) {
	l := &CLI{Bootstrap: func() string { return "" }}
	if got := l.Path(); got != "rex" {
		t.Fatal(got)
	}
}
