package rex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type stub struct{ out string }

func (s stub) Run(context.Context, []byte, ...string) ([]byte, error) { return []byte(s.out), nil }

func TestSnapshotParses(t *testing.T) {
	c := &Client{Runner: stub{`[{"session":"s1","session_index":1,"block":"b1","app":"claude-code","state":"blocked","since":"2026-11-05T20:00:00+09:00"},{"session":"s2","block":"b2"}]`}}
	panes, err := c.Snapshot()
	if err != nil || len(panes) != 2 || !panes[0].Agent() || panes[1].Agent() {
		t.Fatalf("panes=%+v err=%v", panes, err)
	}
	empty := &Client{Runner: stub{"{}\n"}}
	if p, err := empty.Snapshot(); err != nil || len(p) != 0 {
		t.Fatalf("empty: %v %v", p, err)
	}
}

func TestExecRunnerNeverStartsAServer(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "rex")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := ExecRunner{Path: fake}.Run(context.Background(), nil, "session", "list")
	if err != nil || strings.TrimSpace(string(out)) != "--autostart=false session list" {
		t.Fatalf("%q %v", out, err)
	}
}

type streamer struct {
	stub
	args  []string
	stdin string
	lines []string
}

func (s *streamer) Stream(_ context.Context, stdin []byte, line func([]byte), args ...string) error {
	s.args, s.stdin = args, string(stdin)
	for _, l := range s.lines {
		line([]byte(l))
	}
	return nil
}

func TestWatchStreamsSnapshotsFromOneProcess(t *testing.T) {
	s := &streamer{lines: []string{
		"listening: 15 handlers, every session; Ctrl-C to stop",
		`[{"session":"s1","block":"b1","app":"codex","state":"working"}]`,
		"{}",
	}}
	c := &Client{Runner: s}
	var got [][]Pane
	err := c.Watch(context.Background(), 10*time.Second, nil, func(p []Pane) { got = append(got, p) })
	if err == nil {
		t.Fatal("Watch returned no error when rex stopped")
	}
	if strings.Join(s.args, " ") != "do - --all" || !strings.Contains(s.stdin, "local watch = 10\n") {
		t.Fatalf("args %v stdin head %q", s.args, s.stdin[:60])
	}
	if len(got) != 2 || len(got[0]) != 1 || got[0][0].App != "codex" || len(got[1]) != 0 {
		t.Fatalf("snapshots %+v", got)
	}
}

// hosts fakes this Mac's server and three hosts: M1 drops its first
// connection and answers the second, DOWN never answers, and NEW only
// appears in `rex hosts` from the third listing on; the first listing fails.
// The local watcher stops once release is closed.
type hosts struct {
	mu       sync.Mutex
	calls    []string
	stdin    map[string]string // first script per stream target
	listings int
	m1, down int // stream attempts
	release  chan struct{}
}

func (h *hosts) Run(_ context.Context, _ []byte, args ...string) ([]byte, error) {
	call := strings.Join(args, " ")
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, call)
	switch call {
	case "hosts --json":
		h.listings++
		switch {
		case h.listings == 1:
			return nil, errors.New("hosts unavailable")
		case h.listings == 2:
			return []byte(`{"hosts":[{"host":{"label":"M1"}},{"host":{"label":"DOWN"}}]}`), nil
		}
		return []byte(`{"hosts":[{"host":{"label":"M1"}},{"host":{"label":"DOWN"}},{"host":{"label":"NEW"}}]}`), nil
	case "client ls --json":
		return []byte(`{"clients":[{"client_id":"app","info":{"kind":"app","label":"M3"},"principal":{"transport":"unix"}}]}`), nil
	}
	return nil, nil
}

func (h *hosts) Stream(ctx context.Context, stdin []byte, line func([]byte), args ...string) error {
	target := strings.Join(args, " ")
	h.mu.Lock()
	if h.stdin == nil {
		h.stdin = map[string]string{}
	}
	if _, ok := h.stdin[target]; !ok {
		h.stdin[target] = string(stdin)
	}
	h.mu.Unlock()
	switch target {
	case "do - --all":
		line([]byte(`[{"session":"s1","session_index":1,"window":"w1","block":"b1","app":"codex","state":"working","focused":true,"shown_at":"2026-11-09T10:00:00Z"}]`))
		select {
		case <-h.release:
			return errors.New("local rex quit")
		case <-ctx.Done():
			return nil
		}
	case "-S M1 do - --all":
		h.mu.Lock()
		h.m1++
		first := h.m1 == 1
		h.mu.Unlock()
		line([]byte(`[{"session":"rs1","session_index":1,"window":"rw1","block":"r1","app":"claude-code","state":"blocked","focused":true,"shown_at":"2026-11-09T11:00:00Z"}]`))
		if first {
			return errors.New("connection reset")
		}
		<-ctx.Done()
		return nil
	case "-S DOWN do - --all":
		h.mu.Lock()
		h.down++
		h.mu.Unlock()
		return errors.New("host unreachable")
	case "-S NEW do - --all":
		line([]byte(`[{"session":"ns1","session_index":1,"window":"nw1","block":"n1","app":"codex","state":"done"}]`))
		<-ctx.Done()
		return nil
	}
	return fmt.Errorf("unexpected stream %v", args)
}

func (h *hosts) has(want string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		if c == want {
			return true
		}
	}
	return false
}

// Agents on every Rex host reach the keys, as in the Rex sidebar: hosts are
// rediscovered, retried, and their panes routed to the right server.
func TestWatchFollowsEveryHost(t *testing.T) {
	h := &hosts{release: make(chan struct{})}
	c := &Client{Runner: h}
	var merged []Pane
	var once sync.Once
	// Fails rather than hangs if a host is never rediscovered or retried.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.Watch(ctx, 20*time.Millisecond, nil, func(p []Pane) {
		h.mu.Lock()
		retried := h.m1 >= 2 && h.down >= 2
		h.mu.Unlock()
		if len(p) == 3 && retried {
			merged = p
			once.Do(func() { close(h.release) })
		}
	})
	if err == nil || !strings.Contains(err.Error(), "local rex quit") {
		t.Fatalf("Watch error = %v, want the local watcher's", err)
	}
	if len(merged) != 3 {
		t.Fatalf("never saw this Mac, M1 after its reconnect and NEW together")
	}
	local, m1, nw := merged[0], merged[1], merged[2]
	if local.Host != "" || local.SessionIndex != 1 {
		t.Fatalf("local pane = %+v", local)
	}
	// Sessions number on after this Mac's; a host added later is followed.
	if m1.Host != "M1" || m1.SessionIndex != 2 || nw.Host != "NEW" || nw.SessionIndex != 3 {
		t.Fatalf("host panes = %+v %+v", m1, nw)
	}
	// On a host, only this Mac's app (joined there as "M3") marks what it shows,
	// so Current can be a pane on another host.
	if !strings.Contains(h.stdin["-S M1 do - --all"], `local app_pref = "M3"`) || m1.ShownAt.IsZero() {
		t.Fatalf("M1 script head %q, ShownAt %v", h.stdin["-S M1 do - --all"][:40], m1.ShownAt)
	}
	if cur := Current(merged); cur == nil || cur.Block != "r1" {
		t.Fatalf("Current = %+v, want the M1 pane the app sized last", cur)
	}

	if err := c.Show(m1); err != nil {
		t.Fatal(err)
	}
	if !h.has("-S M1 focus -s rs1 r1") || !h.has("-C app do session.select session_id=rs1 window_id=rw1") {
		t.Fatalf("show calls = %v", h.calls)
	}
	_ = c.SendKeys("r1", "y")
	_ = c.SendText("r1", "go")
	_ = c.Zoom("r1", true)
	_ = c.SendKeys("b1", "y")
	for _, want := range []string{"-S M1 send-key -b r1 y", "-S M1 send -b r1 go", "-S M1 zoom r1 --on", "send-key -b b1 y"} {
		if !h.has(want) {
			t.Fatalf("missing %q in %v", want, h.calls)
		}
	}
}

// A host's watcher dropping must not reroute an action already queued for
// one of its panes to this Mac; a fresh snapshot without the pane does.
func TestHostRoutesSurviveADisconnect(t *testing.T) {
	c := &Client{}
	m := &merger{c: c, emit: func([]Pane) {}, byHost: map[string][]Pane{}, order: []string{"M1"}}
	m.publish("M1", []Pane{{Block: "r1"}, {Block: "r2"}}, true)
	m.publish("M1", nil, false)
	if c.host("r1") != "M1" {
		t.Fatalf("route lost on disconnect")
	}
	m.publish("M1", []Pane{{Block: "r2"}}, true)
	if c.host("r1") != "" || c.host("r2") != "M1" {
		t.Fatalf("routes after r1 closed: r1=%q r2=%q", c.host("r1"), c.host("r2"))
	}
}
