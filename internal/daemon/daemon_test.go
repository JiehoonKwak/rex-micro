package daemon

import (
	"errors"

	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/engine"

	"github.com/JiehoonKwak/rex-micro/internal/hid"
	"github.com/JiehoonKwak/rex-micro/internal/macos"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

type fakeDevice struct {
	events chan hid.Event
	mu     sync.Mutex
	writes [][]byte
}

func (f *fakeDevice) Events() <-chan hid.Event { return f.events }
func (f *fakeDevice) Transport() string        { return hid.TransportUSB }
func (f *fakeDevice) Close() error             { return nil }
func (f *fakeDevice) Send(frame func(string) [][]byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range frame(hid.TransportUSB) {
		f.writes = append(f.writes, append([]byte{}, r...))
	}
	return nil
}

// sent decodes every message the daemon wrote to the pad.
func (f *fakeDevice) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var d protocol.Decoder
	var out []string
	for _, w := range f.writes {
		for _, m := range d.Feed(w) {
			out = append(out, string(m.Raw))
		}
	}
	return out
}

type fakeRex struct {
	mu    sync.Mutex
	calls []string
}

const snapshotJSON = `[{"session":"s1","session_label":"api","session_index":1,"window":"w1","block":"b1","label":"shell","focused":true,"app":"claude-code","state":"blocked","since":"2026-11-05T20:00:00+09:00"},
 {"session":"s2","session_label":"web","session_index":2,"window":"w2","block":"b2","label":"shell","focused":true}]`

func (f *fakeRex) Run(_ context.Context, stdin []byte, args ...string) ([]byte, error) {
	call := strings.Join(args, " ")
	f.mu.Lock()
	if call != "do -" {
		f.calls = append(f.calls, call)
	}
	f.mu.Unlock()
	switch {
	case call == "do -":
		return []byte(snapshotJSON), nil
	case call == "client ls --json":
		return []byte(`{"clients":[{"client_id":"client:app","info":{"kind":"app"},"principal":{"transport":"unix"}}]}`), nil
	}
	return nil, nil
}

func (f *fakeRex) has(want string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == want {
			return true
		}
	}
	return false
}

// inOrder reports whether every call in want was made, in that order.
func (f *fakeRex) inOrder(want ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := 0
	for _, c := range f.calls {
		if i < len(want) && c == want[i] {
			i++
		}
	}
	return i == len(want)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// report frames a device notification the way the pad sends it, with the
// report ID that macOS prepends.
func report(t *testing.T, msg string) hid.Event {
	t.Helper()
	return hid.Event{Kind: hid.Report, Transport: hid.TransportUSB, Data: append([]byte{6}, protocol.Frame([]byte(msg), "USB")[0]...)}
}

func TestDaemonEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REX_MICRO_SOCKET", shortSocket(t))
	dev := &fakeDevice{events: make(chan hid.Event, 16)}
	rx := &fakeRex{}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Start(Options{ConfigPath: filepath.Join(dir, "none.toml"), Logger: log.New(testWriter{t}, "", 0), Device: dev, Runner: rx, Stop: stop})
	}()
	dev.events <- hid.Event{Kind: hid.Connected, Transport: hid.TransportUSB}

	// The blocked agent lights Agent Key 1 amber; the empty keys stay dark.
	eventually(t, "Agent Key lighting", func() bool {

		for _, m := range dev.sent() {
			if strings.Contains(m, `"v.oai.thstatus"`) && strings.Contains(m, `{"b":0.8,"c":16096779,"e":4,"id":0,"s":0.8}`) &&
				strings.Contains(m, `{"b":0,"c":0,"e":0,"id":1,"s":0}`) {
				return true
			}
		}
		return false
	})

	// APPROVE sends Enter to the pane focused in the Rex app.
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"ACT07","act":1,"ag":0}}`)
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"ACT07","act":0,"ag":0}}`)
	eventually(t, "Enter to the focused pane", func() bool { return rx.has("-C client:app do pane.send_key key=enter") })

	// Agent Key 1 brings the agent's session and pane into view (after the
	// double-press window): focus the block first, then select the session,
	// or the app keeps showing the old pane of a session already on screen.
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"AG00","act":1,"ag":0}}`)
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"AG00","act":0,"ag":0}}`)
	eventually(t, "show agent pane", func() bool {
		return rx.inOrder("focus -s s1 b1", "-C client:app do session.select session_id=s1 window_id=w1")
	})

	var st Status
	if err := Call("status", &st); err != nil {
		t.Fatal(err)
	}
	if !st.Connected || st.Slots[0].State != "blocked" || st.Selected != "b1" || st.Layer != "agents" {
		t.Fatalf("status = %+v", st)
	}

	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SocketPath()); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

type slowRex struct {
	fakeRex
	gate chan struct{}
}

func (s *slowRex) Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	if strings.Contains(strings.Join(args, " "), "pane.send_key") {
		select {
		case <-s.gate:
		case <-ctx.Done():
		}
	}
	return s.fakeRex.Run(ctx, stdin, args...)
}

func startDaemon(t *testing.T, cfgText string, rx rex.Runner) (*fakeDevice, string, func()) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("REX_MICRO_SOCKET", shortSocket(t))
	cfgPath := filepath.Join(dir, "config.toml")
	if cfgText != "" {
		if err := os.WriteFile(cfgPath, []byte(cfgText), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dev := &fakeDevice{events: make(chan hid.Event, 512)}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Start(Options{ConfigPath: cfgPath, Logger: log.New(testWriter{t}, "", 0), Device: dev, Runner: rx, Stop: stop})
	}()
	dev.events <- hid.Event{Kind: hid.Connected, Transport: hid.TransportUSB}
	eventually(t, "daemon up", func() bool {
		select {
		case err := <-done:
			t.Fatalf("daemon exited: %v", err)
		default:
		}
		var s Status
		return Call("status", &s) == nil && s.Connected
	})
	return dev, cfgPath, func() { close(stop); <-done }
}

// A Rex that stops answering must never freeze the daemon.
func TestSlowRexDoesNotBlockTheDaemon(t *testing.T) {
	rx := &slowRex{gate: make(chan struct{})}
	dev, _, stop := startDaemon(t, "", rx)
	defer stop()
	defer close(rx.gate)
	for i := 0; i < 100; i++ {
		dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"ENC_CW","act":2}}`)
	}
	time.Sleep(300 * time.Millisecond) // let the loop take every tick before asking
	start := time.Now()
	var st Status
	if err := Call("status", &st); err != nil || time.Since(start) > time.Second {
		t.Fatalf("status took %v, err %v", time.Since(start), err)
	}
}

// A client that pauses the daemon and then dies must not leave it paused.
func TestPauseEndsWhenClientDisconnects(t *testing.T) {
	_, _, stop := startDaemon(t, "", &fakeRex{})
	defer stop()
	release, err := Pause()
	if err != nil {
		t.Fatal(err)
	}
	var st Status
	_ = Call("status", &st)
	if !st.Paused {
		t.Fatal("not paused")
	}
	release() // same as the client process exiting: the socket closes
	eventually(t, "resume", func() bool { var s Status; return Call("status", &s) == nil && !s.Paused })
}

// A bad edit keeps the running config; fixing it applies the new one.
func TestReloadKeepsLastGoodConfig(t *testing.T) {
	good := "[[layer]]\nname = \"one\"\n[layer.keys]\nACT07 = { key = \"enter\" }\n"
	_, path, stop := startDaemon(t, good, &fakeRex{})
	defer stop()
	_ = os.WriteFile(path, []byte("[[layer]]\nname = \"two\"\n[layer.keys]\nACT10 = { key = \"x\" }\n"), 0o644)
	var out map[string]string
	_ = Call("reload", &out)
	var st Status
	_ = Call("status", &st)
	if st.Layer != "one" || st.ConfigErr == "" || out["error"] == "" {
		t.Fatalf("bad reload: layer %q, error %q / %q", st.Layer, st.ConfigErr, out["error"])
	}
	_ = os.WriteFile(path, []byte(strings.Replace(good, "one", "three", 1)), 0o644)
	_ = Call("reload", &out)
	st = Status{} // omitempty fields would otherwise keep the old value
	_ = Call("status", &st)
	if st.Layer != "three" || st.ConfigErr != "" {
		t.Fatalf("good reload: layer %q, error %q", st.Layer, st.ConfigErr)
	}
}

// Unplugging mid push-to-talk still runs the up action.
func TestUnplugFinishesHeldKey(t *testing.T) {
	cfg := "[[layer]]\nname = \"a\"\n[layer.keys]\nMIC = { down = { rex = [\"mic-on\"] }, up = { rex = [\"mic-off\"] } }\n"
	rx := &fakeRex{}
	dev, _, stop := startDaemon(t, cfg, rx)
	defer stop()
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"ACT10","act":1}}`)
	eventually(t, "mic on", func() bool { return rx.has("mic-on") })
	dev.events <- hid.Event{Kind: hid.Disconnected, Transport: hid.TransportUSB}
	eventually(t, "mic off", func() bool { return rx.has("mic-off") })
}

// shortSocket returns a socket path under /tmp: macOS caps Unix socket paths
// at 104 bytes, which t.TempDir() can exceed.
func shortSocket(t *testing.T) string {
	d, err := os.MkdirTemp("/tmp", "rm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return filepath.Join(d, "s")
}

// Replies recorded on firmware 0.6.2: ours to an id-less notification, then
// ChatGPT's to its own numbered thstatus.
func TestReportsAnotherAppDrivingTheLights(t *testing.T) {
	dev, _, stop := startDaemon(t, "", &fakeRex{})
	defer stop()
	dev.events <- report(t, `{"result":{"ok":1},"id":null,"method":"v.oai.thstatus"}`)
	time.Sleep(100 * time.Millisecond)
	var st Status
	if err := Call("status", &st); err != nil || st.Foreign != "" {
		t.Fatalf("own reply flagged as foreign: %+v, %v", st.Foreign, err)
	}
	dev.events <- report(t, `{"result":{"ok":1},"id":83,"method":"v.oai.thstatus"}`)
	eventually(t, "foreign lighting reported", func() bool {
		var s Status
		return Call("status", &s) == nil && s.Foreign != ""
	})
}

const micConfig = "[[layer]]\nname = \"a\"\n[layer.keys]\nMIC = { down = { rex = [\"mic-on\"] }, up = { rex = [\"mic-off\"] } }\n"

// Review 2026-11-05: with the worker stuck on Rex and the backlog full, the
// MIC release was dropped while its start still ran.
func TestKeyReleaseSurvivesBacklog(t *testing.T) {
	rx := &slowRex{gate: make(chan struct{})}
	cfg := micConfig + "ENC_CW = { key = \"page_down\" }\nENC_CC = { key = \"page_up\" }\n"
	dev, _, stop := startDaemon(t, cfg, rx)
	defer stop()
	dial := `{"m":"v.oai.hid","p":{"k":"ENC_CW","act":2}}`
	dev.events <- report(t, dial) // the worker blocks on this one
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"ACT10","act":1}}`)
	for i := 0; i < 40; i++ {
		dev.events <- report(t, dial)
	}
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"ACT10","act":0}}`)
	time.Sleep(200 * time.Millisecond) // let the loop queue everything
	close(rx.gate)
	eventually(t, "mic-off after mic-on", func() bool {
		rx.mu.Lock()
		defer rx.mu.Unlock()
		on, off := -1, -1
		for i, c := range rx.calls {
			switch c {
			case "mic-on":
				on = i
			case "mic-off":
				off = i
			}
		}
		return on >= 0 && off > on
	})
}

// Review 2026-11-05: an automatic upgrade (the same teardown as a stop) left a
// held push-to-talk key started.
func TestStopFinishesHeldKey(t *testing.T) {
	rx := &fakeRex{}
	dev, _, stop := startDaemon(t, micConfig, rx)
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"ACT10","act":1}}`)
	eventually(t, "mic on", func() bool { return rx.has("mic-on") })
	stop()
	if !rx.has("mic-off") {
		t.Fatal("daemon exited with the MIC still held")
	}
}

// Review 2026-11-05: a timed-out shell binding kept its child running, which
// could finish after the next key's command.
func TestShellTimeoutStopsChildren(t *testing.T) {
	old := shellTimeout
	shellTimeout = 200 * time.Millisecond
	defer func() { shellTimeout = old }()
	marker := filepath.Join(t.TempDir(), "late")
	err := executor{}.Shell("/bin/sh -c 'sleep 1; touch "+marker+"'; :", false)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the shell's child outlived the timeout")
	}
}

func TestFrontAppLayerLightsAndShortcuts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REX_MICRO_SOCKET", shortSocket(t))
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`
[[layer]]
name = "agents"
[layer.keys]
ENC_CW = { key = "page_down" }

[[layer]]
name = "browser"
color = "#22C55E"
match = { app = "Google Chrome" }
[layer.keys]
ENC_CW = { shortcut = "cmd+shift+]" }
AG00 = { shortcut = "cmd+t" }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	dev := &fakeDevice{events: make(chan hid.Event, 16)}
	var mu sync.Mutex
	front := "Google Chrome"
	var posted []macos.Shortcut
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Start(Options{ConfigPath: cfgPath, Logger: log.New(testWriter{t}, "", 0), Device: dev, Runner: &fakeRex{}, Stop: stop,
			FrontApp: func() string { mu.Lock(); defer mu.Unlock(); return front },
			Shortcut: func(s macos.Shortcut) error { mu.Lock(); posted = append(posted, s); mu.Unlock(); return nil },
			Trusted:  func() bool { return true }})
	}()
	defer func() { close(stop); <-done }()
	dev.events <- hid.Event{Kind: hid.Connected, Transport: hid.TransportUSB}

	// Chrome in front: the browser layer's green lights the backlight and
	// AG00, which the layer rebinds, instead of AG00's (blocked) agent.
	defer func() {
		if t.Failed() {
			for _, m := range dev.sent() {
				t.Log(m)
			}
		}
	}()
	eventually(t, "browser layer lighting", func() bool {
		for _, m := range dev.sent() {
			if strings.Contains(m, `"v.oai.thstatus"`) && strings.Contains(m, `{"b":0.48,"c":2278750,"e":1,"id":0,"s":0}`) {
				return true
			}
		}
		return false
	})
	dev.events <- report(t, `{"m":"v.oai.hid","p":{"k":"ENC_CC","act":1,"ag":0}}`) // firmware's ENC_CC = physical clockwise
	eventually(t, "dial sends the tab shortcut", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(posted) == 1 && posted[0] == macos.Shortcut{Code: 30, Flags: 0x120000}
	})

	mu.Lock()
	front = "Rex"
	mu.Unlock()
	eventually(t, "back to the base layer", func() bool {
		var st Status
		return Call("status", &st) == nil && st.Layer == "agents" && st.FrontApp == "Rex"
	})
}

func TestShortcutSkippedWhenAnotherAppCameToFront(t *testing.T) {
	var posted int
	front := "TextEdit"
	program := ""
	x := executor{post: func(macos.Shortcut) error { posted++; return nil }, front: func() string { return front },
		program: func() string { return program }, logger: log.New(testWriter{t}, "", 0)}
	if err := x.Shortcut("cmd+w", "Google Chrome", ""); !errors.Is(err, engine.ErrSkipped) || posted != 0 {
		t.Fatalf("posted %d, err %v", posted, err)
	}
	front = "Google Chrome"
	if err := x.Shortcut("cmd+w", "Google Chrome", ""); err != nil || posted != 1 {
		t.Fatalf("posted %d, err %v", posted, err)
	}
	front, program = "Rex Beta", "zsh"
	if _ = x.Shortcut("cmd+w", "Rex", "lazygit"); posted != 1 {
		t.Fatal("a lazygit shortcut reached the zsh pane")
	}
	if _ = x.Shortcut("cmd+w", "Rex", "zsh"); posted != 2 {
		t.Fatal("Rex editions are one app")
	}
}

func TestStopRunsReleaseBehindSlowBacklog(t *testing.T) {
	var q effectQueue
	var mu sync.Mutex
	var ran []string
	rec := func(s string, d time.Duration) func() {
		return func() { time.Sleep(d); mu.Lock(); ran = append(ran, s); mu.Unlock() }
	}
	go func() {
		for {
			f, ok := q.pop()
			if !ok {
				return
			}
			f()
			q.done()
		}
	}()
	q.push(rec("mic-on", 0), false)
	q.push(rec("slow1", 300*time.Millisecond), false)
	q.push(rec("slow2", 300*time.Millisecond), true)
	q.push(rec("slow3", 300*time.Millisecond), true)
	q.push(rec("press", 0), false)
	q.push(rec("mic-off", 0), true)
	time.Sleep(50 * time.Millisecond)
	q.keepEssential()
	// The backlog takes longer than the timeout in total, but keeps progressing.
	if !q.drain(400 * time.Millisecond) {
		t.Fatal("drain gave up while effects were still finishing")
	}
	q.close()
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(ran, ","); !strings.HasSuffix(got, "slow2,slow3,mic-off") || strings.Contains(got, "press") {
		t.Fatalf("ran %s", got)
	}
}

func TestStopRunsReleaseWhileOneActionHangs(t *testing.T) {
	var q effectQueue
	var mu sync.Mutex
	var ran []string
	rec := func(s string, d time.Duration) func() {
		return func() { time.Sleep(d); mu.Lock(); ran = append(ran, s); mu.Unlock() }
	}
	go func() {
		for {
			f, ok := q.pop()
			if !ok {
				return
			}
			f()
			q.done()
		}
	}()
	q.push(rec("mic-on", 0), false)
	q.push(rec("hang", time.Second), false) // e.g. four slow Rex calls in one binding
	q.push(rec("mic-off", 0), true)
	time.Sleep(50 * time.Millisecond)
	if q.finish(200 * time.Millisecond) {
		t.Fatal("drained although an action was still running")
	}
	q.close()
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(ran, ","); got != "mic-on,mic-off" {
		t.Fatalf("ran %s before exit", got)
	}
}
