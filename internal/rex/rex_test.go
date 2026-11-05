package rex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
