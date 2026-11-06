package rex

import (
	"context"
	"os"
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
