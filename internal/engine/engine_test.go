package engine

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/agents"
	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

type recorder struct {
	calls []string
	ctx   string // app/program of the last shortcut
}

func (r *recorder) Show(p rex.Pane, zoom bool) error {
	r.calls = append(r.calls, fmt.Sprintf("show %s zoom=%v", p.Block, zoom))
	return nil
}
func (r *recorder) Perform(a string, args map[string]string) error {
	r.calls = append(r.calls, fmt.Sprintf("perform %s %v", a, args))
	return nil
}
func (r *recorder) SendKeys(b string, k []string) error {
	r.calls = append(r.calls, fmt.Sprintf("keys %q %s", b, strings.Join(k, ",")))
	return nil
}
func (r *recorder) SendText(b, t string) error {
	r.calls = append(r.calls, fmt.Sprintf("text %q %q", b, t))
	return nil
}
func (r *recorder) CLI(a []string) error {
	r.calls = append(r.calls, "cli "+strings.Join(a, " "))
	return nil
}
func (r *recorder) Shell(c string, _ bool) error { r.calls = append(r.calls, "shell "+c); return nil }
func (r *recorder) Shortcut(c, app, program string) error {
	r.calls, r.ctx = append(r.calls, "shortcut "+c), app+"/"+program
	return nil
}

func setup(t *testing.T, extra string) (*Engine, *recorder) {
	t.Helper()
	cfg, err := config.Parse(extra)
	if err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	e := New(cfg, r, nil, t.Logf)
	return e, r
}

var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func press(e *Engine, key string, at time.Time) {
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: key, Down: true}, at)
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: key, Down: false}, at.Add(30*time.Millisecond))
}

func want(t *testing.T, r *recorder, calls ...string) {
	t.Helper()
	if calls == nil {
		calls = []string{}
	}
	got := r.calls
	if got == nil {
		got = []string{}
	}
	if !reflect.DeepEqual(got, calls) {
		t.Fatalf("calls:\n got  %q\n want %q", got, calls)
	}
	r.calls = nil
}

func TestDefaultLayerOne(t *testing.T) {
	e, r := setup(t, "")
	press(e, "ACT07", t0)
	want(t, r, `keys "" enter`)
	press(e, "ACT08", t0)
	want(t, r, `keys "" escape`)
	press(e, "ACT06", t0)
	want(t, r, "perform pane.zoom map[]")
	e.Input(protocol.Input{Kind: protocol.InputDial, Key: "ENC_CW"}, t0)
	want(t, r, `keys "" page_down`)
}

func TestWideKeyFiresOnce(t *testing.T) {
	e, r := setup(t, "[[layer]]\nname = \"a\"\n[layer.keys]\nMIC = { down = { shell = \"start\" }, up = { shell = \"stop\" } }\n")
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT10", Down: true}, t0)
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT11", Down: true}, t0)
	want(t, r, "shell start")
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT11", Down: false}, t0)
	want(t, r)
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT10", Down: false}, t0)
	want(t, r, "shell stop")
}

func TestAgentKeysShowSlotAndDoubleZooms(t *testing.T) {
	e, r := setup(t, "")
	panes := []rex.Pane{
		{Block: "b1", App: "claude-code", State: "working", SessionIndex: 1},
		{Block: "b2", App: "codex", State: "blocked", SessionIndex: 2},
	}
	var a agents.Assigner
	e.SetPanes(panes, a.Assign(panes, e.cfg.Agents))

	press(e, "AG01", t0)
	want(t, r) // waits: could be a double
	e.Advance(t0.Add(400 * time.Millisecond))
	want(t, r, "show b2 zoom=false")

	press(e, "AG00", t0)
	press(e, "AG00", t0.Add(150*time.Millisecond))
	want(t, r, "show b1 zoom=true")
	e.Advance(t0.Add(time.Second))
	want(t, r) // the double consumed the first tap

	// target = "agent" now follows the last shown agent, b1.
	e2, r2 := setup(t, "[[layer]]\nname = \"a\"\n[layer.keys]\nACT07 = { key = \"y\", target = \"agent\" }\n")
	e2.SetPanes(panes, a.Assign(panes, e2.cfg.Agents))
	press(e2, "ACT07", t0)
	want(t, r2, `keys "b2" y`) // nothing selected yet: most urgent (blocked) agent
	e2.selected = "b1"
	press(e2, "ACT07", t0)
	want(t, r2, `keys "b1" y`)
}

func TestHoldSuppressesPress(t *testing.T) {
	e, r := setup(t, "[[layer]]\nname = \"a\"\n[layer.keys]\nACT06 = { press = { shell = \"p\" }, hold = { shell = \"h\" } }\n")
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT06", Down: true}, t0)
	e.Advance(t0.Add(600 * time.Millisecond))
	want(t, r, "shell h")
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT06", Down: false}, t0.Add(700*time.Millisecond))
	want(t, r)
	press(e, "ACT06", t0.Add(2*time.Second))
	want(t, r, "shell p")
}

func TestLayersFallThroughAndCycle(t *testing.T) {
	cfg := config.Default + "\n[[layer]]\nname = \"tools\"\ncolor = \"#00FFFF\"\n[layer.keys]\nACT07 = { rex = [\"run\", \"--\", \"lazygit\"] }\n"
	e, r := setup(t, cfg)
	var changed []int
	e.OnLayer = func(i int) { changed = append(changed, i) }
	press(e, "ENC_CLK", t0)
	if e.Layer() != 1 || !reflect.DeepEqual(changed, []int{1}) {
		t.Fatalf("layer = %d, changed = %v", e.Layer(), changed)
	}
	press(e, "ACT07", t0)
	want(t, r, "cli run -- lazygit")
	press(e, "ACT08", t0) // not in tools: falls through to the base layer
	want(t, r, `keys "" escape`)
	press(e, "ENC_CLK", t0) // ENC_CLK falls through too, wrapping to layer 1
	if e.Layer() != 0 {
		t.Fatalf("layer = %d after wrap", e.Layer())
	}
}

func TestStickFiresOncePerPush(t *testing.T) {
	e, r := setup(t, "")
	for _, d := range []float64{0.3, 0.7, 0.9, 0.8} {
		e.Input(protocol.Input{Kind: protocol.InputStick, Angle: 0.01, Dist: d}, t0)
	}
	want(t, r, "perform pane.focus.right map[]")
	e.Input(protocol.Input{Kind: protocol.InputStick, Angle: 0, Dist: 0}, t0)
	e.Input(protocol.Input{Kind: protocol.InputStick, Angle: 0.23, Dist: 1}, t0)
	want(t, r, "perform pane.focus.down map[]")
}

func TestDirection(t *testing.T) {
	// Recorded on hardware: up 0.77, right 0.01, down 0.23, left 0.49.
	cases := map[float64]string{0.77: "UP", 0.75: "UP", 0.01: "RIGHT", 0.97: "RIGHT", 0.23: "DOWN", 0.49: "LEFT", 0.6: "LEFT"}
	for a, w := range cases {
		if got := Direction(a, 0); got != w {
			t.Errorf("Direction(%v) = %s, want %s", a, got, w)
		}
	}
	if got := Direction(0.75, 0.25); got != "RIGHT" {
		t.Errorf("offset: got %s", got)
	}
	for _, off := range []float64{-1.5, -0.25, 1} { // never panics on odd offsets
		_ = Direction(0.77, off)
	}
}

func TestAgentTargetWithoutAgentIsSkipped(t *testing.T) {
	e, r := setup(t, "[[layer]]\nname = \"a\"\n[layer.keys]\nACT07 = { key = \"enter\", target = \"agent\" }\nACT08 = { text = \"go\" }\n")
	e.SetPanes([]rex.Pane{{Block: "shell"}}, [agents.Keys]agents.Slot{})
	press(e, "ACT07", t0)
	press(e, "ACT08", t0)
	want(t, r) // never falls back to whatever pane is focused
}

func TestPushToTalkSurvivesLayerChangeAndReset(t *testing.T) {
	cfg := "[[layer]]\nname = \"a\"\n[layer.keys]\nMIC = { down = { shell = \"start\" }, up = { shell = \"stop\" } }\nACT06 = { builtin = \"layer.next\" }\n[[layer]]\nname = \"b\"\n[layer.keys]\nMIC = { builtin = \"none\" }\n"
	e, r := setup(t, cfg)
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT10", Down: true}, t0)
	press(e, "ACT06", t0) // switch layers while MIC is held
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT10", Down: false}, t0)
	want(t, r, "shell start", "shell stop")

	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT10", Down: true}, t0) // layer b: MIC does nothing
	e.Reset()
	want(t, r)
	press(e, "ACT06", t0) // layer b has no ACT06: falls through to a's layer.next, back to a
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT10", Down: true}, t0)
	e.Reset() // pad unplugged mid-dictation: the up action still runs
	want(t, r, "shell start", "shell stop")
	press(e, "ACT07", t0) // and keys work again afterwards
}

const appLayers = `
[[layer]]
name = "base"
color = "#8B5CF6"
[layer.keys]
ACT07 = { key = "enter" }
ENC_CLK = { builtin = "layer.next" }
ENC_CW = { key = "page_down" }

[[layer]]
name = "browser"
color = "#22C55E"
match = { app = "Google Chrome" }
[layer.keys]
ENC_CW = { shortcut = "cmd+shift+]" }
AG00 = { shortcut = "cmd+t" }

[[layer]]
name = "git"
match = { program = "lazygit" }
[layer.keys]
ENC_CW = { key = "j" }

[[layer]]
name = "spare"
`

func dial(e *Engine) {
	e.Input(protocol.Input{Kind: protocol.InputDial, Key: "ENC_CW"}, t0)
}

func layerName(e *Engine) string { return e.LayerConfig().Name }

func TestFrontAppPicksLayer(t *testing.T) {
	e, r := setup(t, appLayers)
	e.SetContext("Google Chrome", "")
	if layerName(e) != "browser" {
		t.Fatalf("layer = %s", layerName(e))
	}
	dial(e)
	want(t, r, "shortcut cmd+shift+]")
	press(e, "ACT07", t0) // unbound in browser: falls through to the base
	want(t, r, `keys "" enter`)

	e.SetContext("Rex Beta", "zsh")
	if layerName(e) != "base" {
		t.Fatalf("back in Rex: layer = %s", layerName(e))
	}
	e.SetContext("Rex Beta", "lazygit") // program rules apply inside Rex
	dial(e)
	want(t, r, `keys "" j`)
	e.SetContext("Google Chrome", "lazygit") // ...and only there
	if layerName(e) != "browser" {
		t.Fatalf("layer = %s", layerName(e))
	}
}

func TestManualLayerHoldsUntilContextCallsForAnother(t *testing.T) {
	e, _ := setup(t, appLayers)
	e.SetContext("Rex", "zsh")
	press(e, "ENC_CLK", t0) // by hand: base -> browser
	press(e, "ENC_CLK", t0) // -> git
	press(e, "ENC_CLK", t0) // -> spare
	if layerName(e) != "spare" {
		t.Fatalf("layer = %s", layerName(e))
	}
	e.SetContext("Rex", "zsh") // another shell pane: same context
	e.SetContext("", "")       // nothing in front
	cfg, _ := config.Parse(appLayers)
	e.SetConfig(cfg) // an edit and reload
	if layerName(e) != "spare" {
		t.Fatalf("manual layer lost: %s", layerName(e))
	}
	e.SetContext("Google Chrome", "")
	e.SetContext("Rex", "zsh")
	if layerName(e) != "base" {
		t.Fatalf("returning to Rex: layer = %s", layerName(e))
	}
}

func TestReturningToRexRestoresBaseWithoutRules(t *testing.T) {
	e, _ := setup(t, "[[layer]]\nname = \"a\"\n[layer.keys]\nENC_CLK = { builtin = \"layer.next\" }\n[[layer]]\nname = \"b\"\n")
	e.SetContext("Rex", "zsh")
	press(e, "ENC_CLK", t0)
	e.SetContext("Rex Beta", "zsh") // same app, same program
	if layerName(e) != "b" {
		t.Fatalf("manual layer lost: %s", layerName(e))
	}
	e.SetContext("Rex", "claude") // another program
	if layerName(e) != "a" {
		t.Fatalf("new program: layer = %s", layerName(e))
	}
	press(e, "ENC_CLK", t0)
	e.SetContext("Google Chrome", "")
	e.SetContext("Rex", "claude")
	if layerName(e) != "a" {
		t.Fatalf("back in Rex: layer = %s", layerName(e))
	}
}

func TestLayerRebindsAgentKey(t *testing.T) {
	panes := []rex.Pane{{Block: "b1", App: "claude-code", State: "working", SessionIndex: 1}}
	var a agents.Assigner
	e, r := setup(t, appLayers)
	e.SetPanes(panes, a.Assign(panes, e.cfg.Agents))
	if _, ok := e.Rebound(0); ok {
		t.Fatal("AG00 rebound in the base layer")
	}
	e.SetContext("Google Chrome", "")
	press(e, "AG00", t0)
	want(t, r, "shortcut cmd+t")
	if c, ok := e.Rebound(0); !ok || c != "#22C55E" {
		t.Fatalf("Rebound(0) = %q, %v", c, ok)
	}
	if _, ok := e.Rebound(1); ok {
		t.Fatal("AG01 has no binding but reports rebound")
	}

	// agents.always keeps every Agent Key on its agent.
	e, r = setup(t, "[agents]\nalways = true\n"+appLayers)
	e.SetPanes(panes, a.Assign(panes, e.cfg.Agents))
	e.SetContext("Google Chrome", "")
	press(e, "AG00", t0)
	e.Advance(t0.Add(time.Second))
	want(t, r, "show b1 zoom=false")
	if _, ok := e.Rebound(0); ok {
		t.Fatal("rebound despite agents.always")
	}
}

func TestProgramRuleBeatsAppRule(t *testing.T) {
	e, _ := setup(t, `
[[layer]]
name = "base"
[[layer]]
name = "rex"
match = { app = "Rex" }
[[layer]]
name = "git"
match = { program = "lazygit" }
`)
	e.SetContext("Rex Beta", "lazygit")
	if layerName(e) != "git" {
		t.Fatalf("layer = %s", layerName(e))
	}
	e.SetContext("Rex Beta", "zsh")
	if layerName(e) != "rex" {
		t.Fatalf("layer = %s", layerName(e))
	}
}

func TestReloadFindsActiveLayerByName(t *testing.T) {
	text := "[[layer]]\nname = \"base\"\n[[layer]]\nname = \"unused\"\n[[layer]]\nname = \"browser\"\nmatch = { app = \"Google Chrome\" }\n"
	e, _ := setup(t, text)
	e.SetContext("Google Chrome", "")
	cfg, _ := config.Parse(strings.Replace(text, "[[layer]]\nname = \"unused\"\n", "", 1))
	e.SetConfig(cfg) // the editor deleted an earlier layer
	e.SetContext("Google Chrome", "")
	if layerName(e) != "browser" {
		t.Fatalf("layer = %s", layerName(e))
	}
	cfg, _ = config.Parse("[[layer]]\nname = \"base\"\n[[layer]]\nname = \"web\"\nmatch = { app = \"Google Chrome\" }\n")
	e.SetConfig(cfg) // the active layer was renamed: the context picks again
	if layerName(e) != "web" {
		t.Fatalf("after rename: layer = %s", layerName(e))
	}
}

func TestShortcutCarriesItsContext(t *testing.T) {
	e, r := setup(t, "[[layer]]\nname = \"base\"\n[layer.keys]\nACT06 = { shortcut = \"cmd+w\" }\n")
	e.SetContext("Rex Beta", "lazygit")
	press(e, "ACT06", t0)
	if r.ctx != "Rex Beta/lazygit" {
		t.Fatalf("ctx = %q", r.ctx)
	}
}

func TestHeldShortcutKeepsTheContextItWasPressedIn(t *testing.T) {
	e, r := setup(t, "[[layer]]\nname = \"base\"\n[layer.keys]\nACT06 = { press = { shortcut = \"cmd+w\" }, hold = { builtin = \"none\" } }\n")
	e.SetContext("Google Chrome", "")
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT06", Down: true}, t0)
	e.SetContext("TextEdit", "")
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT06", Down: false}, t0.Add(100*time.Millisecond))
	want(t, r, "shortcut cmd+w")
	if r.ctx != "Google Chrome/" {
		t.Fatalf("ctx = %q; the guard must check the app it was pressed in", r.ctx)
	}
}

// skipper is a recorder whose shortcuts report ErrSkipped while skip is set.
type skipper struct {
	recorder
	skip bool
}

func (s *skipper) Shortcut(c, app, program string) error {
	s.recorder.Shortcut(c, app, program)
	if s.skip {
		return ErrSkipped
	}
	return nil
}

const toggleMIC = "[[layer]]\nname = \"base\"\n[layer.keys]\nMIC = { down = { shortcut = \"right_option\" }, up = { shortcut = \"right_option\" } }\n"

func micDown(e *Engine) {
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT10", Down: true}, t0)
}
func micUp(e *Engine) {
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT10", Down: false}, t0.Add(time.Second))
}

func TestToggleReleaseFollowsItsPress(t *testing.T) {
	cfg, err := config.Parse(toggleMIC)
	if err != nil {
		t.Fatal(err)
	}
	var queue []func()
	var essential []bool
	run := func(f func(), ess bool) { queue = append(queue, f); essential = append(essential, ess) }

	// The press ran: the release always goes out, even after focus moved.
	s := &skipper{}
	e := New(cfg, s, run, t.Logf)
	e.SetContext("Rex", "zsh")
	micDown(e)
	e.SetContext("Google Chrome", "")
	micUp(e)
	for _, f := range queue {
		f()
	}
	if len(s.calls) != 2 || s.ctx != "/" || !essential[1] {
		t.Fatalf("calls %q ctx %q essential %v", s.calls, s.ctx, essential)
	}

	// Shutdown dropped the press: the release must not toggle on its own.
	queue, essential = nil, nil
	s = &skipper{}
	e = New(cfg, s, run, t.Logf)
	e.SetContext("Rex", "zsh")
	micDown(e)
	micUp(e)
	queue[1]() // only the essential release runs
	if len(s.calls) != 0 {
		t.Fatalf("release ran without its press: %q", s.calls)
	}

	// The press was skipped (its app left the front): skip the release too.
	queue = nil
	s = &skipper{skip: true}
	e = New(cfg, s, run, t.Logf)
	e.SetContext("Rex", "zsh")
	micDown(e)
	micUp(e)
	queue[0]()
	s.skip = false
	queue[1]()
	if len(s.calls) != 1 {
		t.Fatalf("calls %q", s.calls)
	}
}

func TestUnrelatedUpShortcutKeepsFocusCheck(t *testing.T) {
	e, r := setup(t, "[[layer]]\nname = \"base\"\n[layer.keys]\nACT06 = { down = { shortcut = \"cmd+l\" }, up = { shortcut = \"cmd+w\" } }\n")
	e.SetContext("Google Chrome", "")
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT06", Down: true}, t0)
	e.SetContext("TextEdit", "")
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: "ACT06", Down: false}, t0.Add(time.Second))
	if r.ctx != "Google Chrome/" {
		t.Fatalf("cmd+w ctx = %q; only a toggle's second tap may skip the focus check", r.ctx)
	}
}

// blocker is a recorder whose shell commands wait until release closes.
type blocker struct {
	recorder
	began, release chan struct{}
}

func (b *blocker) Shell(c string, d bool) error {
	if c == "slow" {
		close(b.began)
		<-b.release
	}
	return b.recorder.Shell(c, d)
}

func TestReleaseRunsBesideADownStillRunning(t *testing.T) {
	cfg, err := config.Parse("[[layer]]\nname = \"base\"\n[layer.keys]\nMIC = { down = { shell = \"slow\" }, up = { shell = \"mic-off\" } }\n")
	if err != nil {
		t.Fatal(err)
	}
	var queue []func()
	b := &blocker{began: make(chan struct{}), release: make(chan struct{})}
	e := New(cfg, b, func(f func(), _ bool) { queue = append(queue, f) }, t.Logf)
	micDown(e)
	micUp(e)
	done := make(chan struct{})
	go func() { queue[0](); close(done) }()
	<-b.began
	queue[1]() // shutdown runs the release while the down still runs
	close(b.release)
	<-done
	if !strings.Contains(strings.Join(b.calls, ","), "shell mic-off") {
		t.Fatalf("calls %q", b.calls)
	}
}
