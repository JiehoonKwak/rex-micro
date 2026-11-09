package daemon

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
)

// stop leaves no helper behind, even one that ignores its input closing.
func TestOverlayHelperStopEndsTheProcess(t *testing.T) {
	script := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	h := &overlayHelper{exe: script, logf: t.Logf}
	if err := h.start(); err != nil {
		t.Fatal(err)
	}
	p := h.cmd.Process
	start := time.Now()
	h.stop()
	if err := p.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("the helper still runs after stop")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("stop took %v", d)
	}
}

// Without an executable (tests, Options.Self empty) the overlay starts nothing.
func TestOverlayWithoutExecutableStartsNothing(t *testing.T) {
	h := &overlayHelper{logf: t.Logf}
	h.show("<p>keys</p>")
	if h.cmd != nil {
		t.Fatal("started a helper with no executable")
	}
}

// The overlay follows the engine: the held dial shows the active layer, a
// layer change while it is up redraws it, and the release hides it.
func TestKeysOverlayFollowsTheLayer(t *testing.T) {
	cfg, err := config.Parse(`[[layer]]
name = "agents"
[layer.keys]
ENC_CLK = { press = { builtin = "none" }, hold = { builtin = "keys.show" } }
[[layer]]
name = "browser"
`)
	if err != nil {
		t.Fatal(err)
	}
	d := &daemon{logger: log.New(testWriter{t}, "", 0), cfg: cfg, keysOut: make(chan string, 1)}
	d.eng = engine.New(cfg, nil, nil, nil)
	d.eng.OnLayer, d.eng.OnKeys = d.onLayer, d.onKeys
	next := func() string {
		t.Helper()
		select {
		case p := <-d.keysOut:
			return p
		default:
			t.Fatal("nothing sent to the overlay")
			return ""
		}
	}
	now := time.Now()
	d.eng.Input(protocol.Input{Kind: protocol.InputKey, Key: "ENC_CLK", Down: true}, now)
	d.eng.Advance(now.Add(time.Second))
	if p := next(); !strings.Contains(p, ">agents<") {
		t.Fatalf("shown page is not the agents layer")
	}
	d.eng.SelectLayer(1) // e.g. the touch sensor, while the dial is held
	if p := next(); !strings.Contains(p, ">browser<") {
		t.Fatalf("the overlay did not redraw for the new layer")
	}
	d.eng.Input(protocol.Input{Kind: protocol.InputKey, Key: "ENC_CLK", Down: false}, now.Add(2*time.Second))
	if p := next(); p != "" {
		t.Fatal("release did not hide the overlay")
	}
}
