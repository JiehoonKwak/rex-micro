package cli

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/daemon"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	usagelog "github.com/JiehoonKwak/rex-micro/internal/usage"
)

// The report lists every binding, the never-used ones first, so the keys
// worth rebinding lead each layer.
func TestUsageReportPutsUnusedBindingsFirst(t *testing.T) {
	cfg, err := config.Parse(`[[layer]]
name = "agents"
[layer.keys]
ACT07 = { shortcut = "cmd+w", label = "Close pane" }
ACT08 = { shortcut = "cmd+;", label = "Back" }
`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 12, 10, 12, 0, 0, 0, time.Local)
	l := usagelog.New()
	bound := daemon.Bound{Record: true, Uses: engine.New(cfg, nil, nil, nil).Bound()}
	for _, u := range bound.Uses {
		if u.Key == "ACT07" {
			for i := 0; i < 3; i++ {
				l.Add(u, now)
			}
		}
	}
	var b strings.Builder
	if err := usageReport(&b, bound, l, 30, now); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	closeAt, backAt := strings.Index(out, "Close pane"), strings.Index(out, "Back")
	if closeAt < 0 || backAt < 0 || backAt > closeAt || !strings.Contains(out, "never") || !strings.Contains(out, "recorded since 2026-12-10") {
		t.Fatalf("report:\n%s", out)
	}
	if !regexpLine(out, `ACT07\s+press\s+Close pane\s+3\s+1\s+2026-12-10`) {
		t.Fatalf("Close pane's row is wrong:\n%s", out)
	}
}

func regexpLine(out, pattern string) bool {
	return regexp.MustCompile(`(?m)` + pattern).MatchString(out)
}

// usage refuses rather than show zeros it cannot vouch for: a record it
// cannot read, or a running daemon too old to say what it counts.
func TestUsageRefusesWhatItCannotVouchFor(t *testing.T) {
	dir, err := os.MkdirTemp("", "rmu")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "daemon.sock")
	t.Setenv("REX_MICRO_SOCKET", sock)
	t.Setenv("REX_MICRO_CONFIG", filepath.Join(dir, "config.toml"))

	if err := os.WriteFile(daemon.UsagePath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := usageCmd(nil); err == nil || !strings.Contains(err.Error(), "usage.json") {
		t.Errorf("an unreadable record gave %v", err)
	}
	os.Remove(daemon.UsagePath())

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			bufio.NewReader(c).ReadBytes('\n')
			c.Write([]byte(`{"error":"unknown command \"usage\""}` + "\n"))
			c.Close()
		}
	}()
	if err := usageCmd(nil); err == nil || !strings.Contains(err.Error(), "older") {
		t.Errorf("an old daemon gave %v", err)
	}
}
