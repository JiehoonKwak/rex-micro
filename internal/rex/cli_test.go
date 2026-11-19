package rex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// The daemon starts its local watcher and the hosts listing at once: while
// the first caller asks the server, the others must wait for its answer, not
// start a long-lived rex on the bootstrap one.
func TestConcurrentCallersAllGetTheFollowedServer(t *testing.T) {
	dir := t.TempDir()
	boot := writeFile(t, filepath.Join(dir, "rex"), "#!/bin/sh\nsleep 0.3\nprintf '{\"pid\":7,\"you\":{\"transport\":\"unix\"}}'\n")
	exe := writeFile(t, filepath.Join(dir, "rex-server"), "")
	l := &CLI{Bootstrap: func() string { return boot }, Exe: func(int) (string, error) { return exe, nil }}
	var wg sync.WaitGroup
	got := make([]string, 4)
	for i := range got {
		wg.Add(1)
		go func() { defer wg.Done(); got[i] = l.Path() }()
	}
	wg.Wait()
	for i, g := range got {
		if g != exe {
			t.Errorf("caller %d got %s, want the server's %s", i, g, exe)
		}
	}
}

// Rex answers only after the local watcher started on the bootstrap rex: the
// watcher moves to the server's own rex instead of staying on the stand-in
// for the rest of the server's run.
func TestLocalWatcherHandsOverToTheServersRex(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	stream := func(block string) string {
		return fmt.Sprintf("[ \"$2\" = do ] && { printf '[{\"session\":\"s\",\"block\":\"%s\"}]\\n'; exec sleep 30; }\n", block)
	}
	boot := writeFile(t, filepath.Join(dir, "rex"), "#!/bin/sh\n"+
		fmt.Sprintf("[ \"$2 $3\" = \"server status\" ] && { [ -f %q ] || exit 1; printf '{\"pid\":7,\"you\":{\"transport\":\"unix\"}}'; exit 0; }\n", pidFile)+
		stream("boot"))
	exe := writeFile(t, filepath.Join(dir, "rex-server"), "#!/bin/sh\n"+stream("server"))
	l := &CLI{Bootstrap: func() string { return boot }, Exe: func(int) (string, error) { return exe, nil }, Retry: 50 * time.Millisecond}
	c := &Client{Runner: ExecRunner{CLI: l}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	seen := make(chan string, 8)
	go func() {
		_ = c.watchOne(ctx, "", "", time.Hour, nil, func(p []Pane) {
			if len(p) == 1 {
				seen <- p[0].Block
			}
		})
	}()
	wait := func(want string) {
		t.Helper()
		select {
		case got := <-seen:
			if got != want {
				t.Fatalf("snapshot from %s, want %s", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no snapshot from %s", want)
		}
	}
	wait("boot") // the server did not answer the lookup yet
	writeFile(t, pidFile, "7")
	wait("server")
	if l.Known() != exe {
		t.Fatalf("handed over but forgot the server: %q", l.Known())
	}
}
