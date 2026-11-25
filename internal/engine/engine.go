// Package engine turns pad inputs into actions: layers, gestures (press,
// double, hold, down, up), the wide MIC key, joystick directions and the
// Agent Keys. It is pure: time is passed in, and side effects go through an
// Executor, so every behaviour is testable without hardware or Rex.
package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/agents"
	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
	"github.com/JiehoonKwak/rex-micro/internal/usage"
)

// Executor performs the side effects of bindings.
type Executor interface {
	Show(p rex.Pane, zoom bool) error
	Perform(action string, args map[string]string) error
	SendKeys(block string, keys []string) error
	SendText(block, text string) error
	CLI(args []string) error
	// Shortcut sends a key combination such as "cmd+shift+]" to the front
	// app, but only while app and program (in front when the key was
	// pressed) still are; "" skips that check. It returns ErrSkipped when
	// the check fails.
	Shortcut(combo, app, program string) error
	// HoldShortcut presses (down) or releases a shortcut held with a pad key,
	// at once and with no focus check.
	HoldShortcut(combo string, down bool) error
	// Shell runs cmd with env (NAME=value entries, see ShellEnv) added to its
	// environment; it waits for it (keeping effects in order) unless detach.
	Shell(cmd string, detach bool, env []string) error
}

// ErrSkipped reports an effect that chose not to act, e.g. a shortcut whose
// app has left the front. A down effect that skipped also skips its up.
var ErrSkipped = errors.New("skipped")

// Engine holds layer, gesture and agent-selection state.
type Engine struct {
	cfg   *config.Config
	exec  Executor
	run   func(f func(), essential bool) // where effects run; synchronous by default
	logf  func(string, ...any)
	layer int

	panes    []rex.Pane
	slots    [agents.Keys]agents.Slot
	selected string // block ID of the agent pane last shown

	keys     map[string]*keyState
	micHeld  map[string]bool
	stickDir string

	// OnLayer is called after the active layer changes.
	OnLayer func(index int)
	cur     *keyState // the key whose gesture is being fired
	gesture string
	queued  bool // fire handed an effect to run

	// Context: the app in front and the program in the pane Rex shows.
	app, program string
	matched      string // layer the context last selected, by name

	// OnUse is called when a binding does something: once per use (a
	// down/up pair counts as its down).
	OnUse func(usage.Use)

	// OnKeys is called when the keys overlay (keys.show) should appear or go.
	OnKeys    func(show bool)
	keysShown bool
	keysKey   string    // the key that showed it
	keysHeld  bool      // shown by a hold (or down): up until keysKey is released
	keysUntil time.Time // when an overlay a press showed goes by itself
	now       time.Time // time of the input or tick being handled
}

type keyState struct {
	down         bool
	downAt       time.Time
	holdFired    bool
	tapAt        time.Time // first tap of a possible double, zero when none pending
	pressed      bool      // the press already fired on the first down (eager)
	binding      *config.KeyBinding
	app, program string // context when the gesture began; its actions target it
	layer        string // the active layer's name then
	owner        string // the name of the layer whose binding this is
	// target is a copy of the selected agent pane then (agentPane), which
	// key and text bindings with an agent target and shell bindings act on,
	// so a refresh or another selection during a double-press or hold wait
	// cannot redirect them; an agent this gesture itself shows replaces it
	// (show). Nil when there was no agent.
	target *rex.Pane
	// agent is the block an Agent Key stood for when the gesture began, so a
	// refresh that reorders the keys during the double-press wait cannot send
	// the press to another pane. Only the ID is kept: where the pane is (its
	// window) is looked up when the gesture fires.
	agent string
	press *keyPress
}

// press pairs one key press's down and up effects. The up runs only if the
// down did (or there is none), so a toggle pressed once on down and once on
// up never ends up toggled when the down was dropped or skipped. A released
// down always gets its up, wherever focus has gone since.
type keyPress struct {
	hasDown bool
	started atomic.Bool
}

// New builds an engine. run decides where effects execute (nil = inline).
// run executes an effect; essential is true for effects that finish something
// already started (a key's up gesture), which a busy runner must not drop.
func New(cfg *config.Config, exec Executor, run func(f func(), essential bool), logf func(string, ...any)) *Engine {
	if run == nil {
		run = func(f func(), _ bool) { f() }
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Engine{cfg: cfg, exec: exec, run: run, logf: logf, keys: map[string]*keyState{}, micHeld: map[string]bool{}}
}

// SetConfig swaps the config, keeping the layer when it still exists.
func (e *Engine) SetConfig(cfg *config.Config) {
	// Find the active layer by name: an edit may add, remove or reorder layers.
	name := e.cfg.Layers[e.layer].Name
	e.cfg, e.layer = cfg, 0
	for i, l := range cfg.Layers {
		if l.Name == name {
			e.layer = i
		}
	}
	if cfg.Layers[e.layer].Name != name {
		e.matched = "" // the active layer is gone; let the context choose again
	}
	e.follow()
	// Held keys keep the binding they went down with, so a release still
	// runs its `up` action (push-to-talk must always stop).
}

// Reset cancels every held key and pending gesture, running the `up` action
// of held keys so paired actions (push-to-talk) finish. Call it when inputs
// stop arriving: the pad disconnected or the daemon paused.
func (e *Engine) Reset() {
	e.hideKeys()
	for name, s := range e.keys {
		if s.down {
			e.fire(name, "up", s)
		}
	}
	e.keys = map[string]*keyState{}
	e.micHeld = map[string]bool{}
	e.stickDir = ""
}

// SetPanes gives the engine the latest Rex snapshot and Agent Key slots.
func (e *Engine) SetPanes(panes []rex.Pane, slots [agents.Keys]agents.Slot) {
	e.panes, e.slots = panes, slots
}

// SetContext tells the engine which app is in front and which program runs
// in the pane Rex shows. When either changes, the engine switches to the layer
// the match rules pick (the base layer when none matches), so coming back to
// Rex or starting a program restores its layer. A layer picked by hand stays
// while the app and program stay, e.g. while focus moves between shell panes.
// An empty app (no window in front) changes nothing.
func (e *Engine) SetContext(app, program string) {
	if app == "" {
		return
	}
	changed := config.AppName(app) != config.AppName(e.app) || program != e.program
	e.app, e.program = app, program
	if changed {
		e.matched = ""
	}
	e.follow()
}

func (e *Engine) follow() {
	if e.app == "" {
		return
	}
	i := MatchLayer(e.cfg.Layers, e.app, e.program)
	name := e.cfg.Layers[i].Name
	if name == e.matched {
		return
	}
	e.matched = name
	e.setLayer(i)
}

// MatchLayer picks the layer for a context: a layer matching the program
// first, then one matching the app, else the base layer.
func MatchLayer(layers []config.Layer, app, program string) int {
	for pass := 0; pass < 2; pass++ {
		for i, l := range layers {
			if l.Match == nil || (pass == 0) != (l.Match.Program != "") {
				continue
			}
			if l.Match.Matches(app, program) {
				return i
			}
		}
	}
	return 0
}

// SelectLayer turns on layer i as if picked by hand (layer.next does the
// same): it stays until the app in front or the Rex program changes.
func (e *Engine) SelectLayer(i int) {
	if i >= 0 && i < len(e.cfg.Layers) {
		e.setLayer(i)
	}
}

func (e *Engine) Layer() int                { return e.layer }
func (e *Engine) LayerConfig() config.Layer { return e.cfg.Layers[e.layer] }
func (e *Engine) Selected() string          { return e.selected }

// Input handles one pad input at time now.
func (e *Engine) Input(in protocol.Input, now time.Time) {
	e.now = now
	switch in.Kind {
	case protocol.InputDial:
		e.keysInput(in.Key, false)
		e.fireNow(in.Key, e.binding(in.Key))
	case protocol.InputStick:
		e.keysInput("STICK", false)
		e.stick(in, now)
	case protocol.InputKey:
		name := in.Key
		if name == "ACT10" || name == "ACT11" {
			// One wide keycap presses both switches; treat them as one MIC key.
			was := len(e.micHeld) > 0
			if in.Down {
				e.micHeld[name] = true
			} else {
				delete(e.micHeld, name)
			}
			now2 := len(e.micHeld) > 0
			if was == now2 {
				return
			}
			name = "MIC"
		}
		e.keysInput(name, !in.Down) // the logical key, once MIC is one
		if in.Down {
			e.keyDown(name, now)
		} else {
			e.keyUp(name, now)
		}
	}
}

func (e *Engine) state(name string) *keyState {
	s := e.keys[name]
	if s == nil {
		s = &keyState{}
		e.keys[name] = s
	}
	return s
}

func (e *Engine) keyDown(name string, now time.Time) {
	s := e.state(name)
	if s.down {
		return
	}
	if !s.tapAt.IsZero() && now.Sub(s.tapAt) >= e.cfg.Timing.DoublePress.Duration {
		e.Advance(now) // the tap expired before its timer ran: not a double
	}
	b := e.binding(name)
	first := s.tapAt.IsZero()
	if first {
		// A double completes with the binding and context it started with.
		s.binding, s.agent, s.pressed = b, "", false
		e.begin(name, s)
		if i, ok := agentIndex(name); ok && e.slots[i].Pane != nil {
			s.agent = e.slots[i].Pane.Block
		}
	}
	s.down, s.downAt, s.holdFired = true, now, false
	s.press = &keyPress{hasDown: gestureAction(s.binding, "down") != nil}
	e.fire(name, "down", s)
	switch {
	case s.binding.Gesture("hold") == nil && s.binding.Gesture("double") == nil:
		e.fire(name, "press", s)
	case first && eager(name, s.binding):
		s.pressed = true
		e.fire(name, "press", s)
	}
}

// eager reports whether a key's press fires the moment the key goes down
// although the key also has a double: an Agent Key whose double only adds
// to its press (agent.show, then agent.show_zoom zooms the same pane, which
// the key pinned when it went down), so showing at once leaves the same
// result and saves the tap and the double window, most of its delay. A key
// with other gestures (hold, down, up) waits as before: an early show would
// change the agent those act on.
func eager(name string, b *config.KeyBinding) bool {
	if _, ok := agentIndex(name); !ok {
		return false
	}
	for _, g := range []string{"hold", "down", "up"} {
		if gestureAction(b, g) != nil {
			return false
		}
	}
	p, d := b.Gesture("press"), b.Gesture("double")
	return p != nil && d != nil && p.Builtin == "agent.show" && d.Builtin == "agent.show_zoom"
}

func (e *Engine) keyUp(name string, now time.Time) {
	s := e.state(name)
	if !s.down {
		return
	}
	s.down = false
	b := s.binding
	e.fire(name, "up", s)
	if s.holdFired || (b.Gesture("hold") == nil && b.Gesture("double") == nil) {
		return
	}
	if b.Gesture("double") == nil {
		e.fire(name, "press", s)
		return
	}
	if !s.tapAt.IsZero() {
		s.tapAt = time.Time{}
		e.fire(name, "double", s)
		return
	}
	if s.pressed && !eager(name, e.binding(name)) {
		return // the layer changed under the press and rebinds the key: the show was the gesture
	}
	s.tapAt = now
}

// Advance fires gestures whose time has come: holds and single presses that
// waited to rule out a double.
func (e *Engine) Advance(now time.Time) {
	e.now = now
	e.keysTimeout(now)
	for name, s := range e.keys {
		if s.down && !s.holdFired && s.binding.Gesture("hold") != nil && now.Sub(s.downAt) >= e.cfg.Timing.Hold.Duration {
			s.holdFired = true
			s.tapAt = time.Time{}
			e.fire(name, "hold", s)
		}
		if !s.down && !s.tapAt.IsZero() && now.Sub(s.tapAt) >= e.cfg.Timing.DoublePress.Duration {
			s.tapAt = time.Time{}
			if !s.pressed {
				e.fire(name, "press", s)
			}
		}
	}
}

// NextDeadline returns when Advance next has work, or zero when nothing waits.
func (e *Engine) NextDeadline() time.Time {
	var next time.Time
	consider := func(t time.Time) {
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}
	if e.keysShown && !e.keysHeld {
		consider(e.keysUntil)
	}
	for _, s := range e.keys {
		if s.down && !s.holdFired && s.binding.Gesture("hold") != nil {
			consider(s.downAt.Add(e.cfg.Timing.Hold.Duration))
		}
		if !s.down && !s.tapAt.IsZero() {
			consider(s.tapAt.Add(e.cfg.Timing.DoublePress.Duration))
		}
	}
	return next
}

func (e *Engine) stick(in protocol.Input, now time.Time) {
	th := e.cfg.Stick.Threshold
	if in.Dist < th*0.5 {
		e.stickDir = ""
		return
	}
	if in.Dist < th || e.stickDir != "" {
		return
	}
	e.stickDir = Direction(in.Angle, e.cfg.Stick.AngleOffset)
	name := "STICK_" + e.stickDir
	e.fireNow(name, e.binding(name))
}

// Direction quantizes a 0-1 joystick angle into UP, RIGHT, DOWN or LEFT.
// On the pad, angle 0 points right and grows clockwise (measured on firmware
// 0.6.2 by `rex-micro test`); offset rotates it further for a unit that differs.
func Direction(angle, offset float64) string {
	a := math.Mod(angle+0.25+offset+0.125, 1) // +1/4: right -> up; +1/8 centers each quadrant
	if a < 0 || math.IsNaN(a) {
		a = math.Mod(a+math.Ceil(-a)+1, 1)
	}
	if math.IsNaN(a) {
		return "UP"
	}
	return [...]string{"UP", "RIGHT", "DOWN", "LEFT"}[int(a*4)%4]
}

// binding finds a key's binding in the active layer, falling through to the
// base layer, then to the Agent Key defaults.
func (e *Engine) binding(name string) *config.KeyBinding {
	if b, _ := e.layerBinding(name); b != nil {
		return b
	}
	if _, ok := agentIndex(name); ok {
		return e.agentDefault()
	}
	return nil
}

// Binding is what key name does in the active layer, and the layer that
// gives it: the active one, 0 when it falls through, -1 for the Agent Key
// default.
func (e *Engine) Binding(name string) (*config.KeyBinding, int) {
	if b, l := e.layerBinding(name); b != nil {
		return b, l
	}
	return e.binding(name), -1
}

// AgentSlot is what Agent Key i stands for: its pane (nil when none) and
// the state its light shows.
func (e *Engine) AgentSlot(i int) agents.Slot {
	if i < 0 || i >= agents.Keys {
		return agents.Slot{}
	}
	return e.slots[i]
}

// AgentPane is the pane Agent Key i stands for, nil when it has none.
func (e *Engine) AgentPane(i int) *rex.Pane {
	if i < 0 || i >= agents.Keys {
		return nil
	}
	return e.slots[i].Pane
}

// layerBinding is the binding a layer gives the key, and that layer's index.
// With agents.always the Agent Keys have none.
func (e *Engine) layerBinding(name string) (*config.KeyBinding, int) {
	if _, ok := agentIndex(name); ok && e.cfg.Agents.Always {
		return nil, -1
	}
	if b := e.cfg.Layers[e.layer].Keys[name]; b != nil {
		return b, e.layer
	}
	if b := e.cfg.Layers[0].Keys[name]; b != nil {
		return b, 0
	}
	return nil, -1
}

// Rebound reports whether Agent Key i does something other than show its
// agent in the active layer, and the color of the layer that rebinds it.
func (e *Engine) Rebound(i int) (color string, ok bool) {
	b, l := e.layerBinding(fmt.Sprintf("AG%02d", i))
	if b == nil {
		return "", false
	}
	for _, g := range []string{"press", "double", "hold", "down", "up"} {
		if a := b.Gesture(g); a != nil && !strings.HasPrefix(a.Builtin, "agent.show") {
			return e.cfg.Layers[l].Color, true
		}
	}
	return "", false // it still only shows its agent
}

func agentDefault(v string) *config.Action {
	switch v {
	case "show":
		return &config.Action{Builtin: "agent.show"}
	case "show_zoom":
		return &config.Action{Builtin: "agent.show_zoom"}
	}
	return nil
}

// fireNow fires the press of a control with no held state (dial, stick).
func (e *Engine) fireNow(name string, b *config.KeyBinding) {
	s := &keyState{binding: b}
	e.begin(name, s)
	e.fire(name, "press", s)
}

// gestureAction is what a gesture does. A held shortcut is its key's down and
// up and nothing else.
func gestureAction(b *config.KeyBinding, gesture string) *config.Action {
	if h := b.HeldShortcut(); h != nil {
		if gesture == "down" || gesture == "up" {
			return h
		}
		return nil
	}
	return b.Gesture(gesture)
}

// begin records the context key name's gesture starts in: the app and
// program in front, the active layer, the layer that binds the key, and the
// selected agent pane.
func (e *Engine) begin(name string, s *keyState) {
	s.app, s.program, s.layer, s.target = e.app, e.program, e.cfg.Layers[e.layer].Name, nil
	_, owner := e.layerBinding(name)
	s.owner = e.cfg.Layers[max(owner, 0)].Name
	if p := e.agentPane(); p != nil {
		cp := *p
		s.target = &cp
	}
}

func (e *Engine) fire(name, gesture string, s *keyState) {
	a := gestureAction(s.binding, gesture)
	if a == nil {
		return
	}
	e.cur, e.gesture, e.queued = s, gesture, false
	defer func() { e.cur, e.gesture = nil, "" }()
	e.countUse(name, gesture, s)
	e.do(name, a)
	if gesture == "down" && !e.queued && s.press != nil {
		s.press.started.Store(true) // a builtin acts at once
	}
}

// countUse reports one use of a binding to OnUse.
func (e *Engine) countUse(name, gesture string, s *keyState) {
	if e.OnUse == nil {
		return
	}
	if u, ok := useOf(s.owner, name, gesture, s.binding); ok {
		e.OnUse(u)
	}
}

// useOf is the use a gesture of binding b counts as. A key's up is part of
// the use its down began, and a held shortcut (down and up) is one press.
func useOf(layer, name, gesture string, b *config.KeyBinding) (usage.Use, bool) {
	a := gestureAction(b, gesture)
	if a == nil || (gesture == "up" && gestureAction(b, "down") != nil) {
		return usage.Use{}, false
	}
	what := actionID(a)
	if b.HeldShortcut() != nil {
		gesture = "press"
	} else if gesture == "down" {
		what += " / up: " + actionID(gestureAction(b, "up")) // the release is part of it
	}
	return usage.Use{Layer: layer, Key: name, Gesture: gesture, Action: what, Does: Brief(a)}, true
}

// actionID is what an action does, without its label: a binding's identity
// in the usage record. Encoding JSON sorts map keys, so it is stable.
func actionID(a *config.Action) string {
	if a == nil {
		return "-"
	}
	c := *a
	c.Label = ""
	b, _ := json.Marshal(c)
	return string(b)
}

// Uses lists every use layer i's own bindings can count, in pad order: what
// a usage report covers, including the ones never used.
func (e *Engine) Uses(i int) []usage.Use {
	var out []usage.Use
	for _, name := range config.KeyNames {
		b := e.OwnBinding(i, name)
		if b == nil {
			continue
		}
		for _, g := range []string{"press", "double", "hold", "down", "up"} {
			if u, ok := useOf(e.cfg.Layers[i].Name, name, g, b); ok && (len(out) == 0 || out[len(out)-1] != u) {
				out = append(out, u)
			}
		}
	}
	return out
}

// OwnBinding is what layer i itself binds key name to, nil for a key it
// leaves to the base; the base also has the Agent Keys' [agents] defaults.
func (e *Engine) OwnBinding(i int, name string) *config.KeyBinding {
	_, agent := agentIndex(name)
	if i == 0 && agent && e.cfg.Agents.Always {
		return e.agentDefault()
	}
	if agent && e.cfg.Agents.Always {
		return nil
	}
	if b := e.cfg.Layers[i].Keys[name]; b != nil || i > 0 || !agent {
		return b
	}
	return e.agentDefault()
}

func (e *Engine) agentDefault() *config.KeyBinding {
	return &config.KeyBinding{Press: agentDefault(e.cfg.Agents.Press), Double: agentDefault(e.cfg.Agents.Double)}
}

func (e *Engine) do(name string, a *config.Action) {
	switch a.Kind() {
	case "builtin":
		e.builtin(name, a.Builtin)
	case "action":
		action, args := a.Action, a.Args
		e.effect(name, func() error { return e.exec.Perform(action, args) })
	case "key":
		block, keys := "", []string(a.Key) // "" = the pane focused in the Rex app
		if a.Target == "agent" {
			p := e.cur.target
			if p == nil {
				e.logf("%s: no agent pane to send %v to; skipped", name, keys)
				return
			}
			block = p.Block
		}
		e.effect(name, func() error { return e.exec.SendKeys(block, keys) })
	case "text":
		p := e.cur.target // text always goes to the selected agent pane
		if p == nil {
			e.logf("%s: no agent pane to type into; skipped", name)
			return
		}
		block, text := p.Block, a.Text
		e.effect(name, func() error { return e.exec.SendText(block, text) })
	case "shortcut":
		combo, app, program := a.Shortcut, e.cur.app, e.cur.program
		if d := gestureAction(e.cur.binding, "down"); e.gesture == "up" && d != nil && d.Shortcut == combo {
			// The second tap of a toggle whose first tap ran: always send
			// it, or the toggle stays on. Any other up keeps the focus check.
			app, program = "", ""
		}
		if a.Held {
			e.holdNow(name, combo)
			break
		}
		e.effect(name, func() error { return e.exec.Shortcut(combo, app, program) })
	case "rex":
		args := a.Rex
		e.effect(name, func() error { return e.exec.CLI(args) })
	case "shell":
		cmd, detach := a.Shell, a.Detach
		env := ShellEnv(name, e.gesture, e.cur.layer, e.cur.target) // as the gesture began
		e.effect(name, func() error { return e.exec.Shell(cmd, detach, env) })
	}
}

// holdNow presses or releases a held shortcut at once, bypassing the effect
// queue: the app on the other end times the press (tap versus hold), so the
// edges must move when the pad key does, not after a slow action ahead of
// them. Posting at once also makes a focus check moot. The release follows
// only a press that went out.
func (e *Engine) holdNow(name, combo string) {
	down, p := e.gesture == "down", e.cur.press
	e.queued = true
	if !down && p != nil && !p.started.Load() {
		e.logf("%s: its press did not go out; nothing to release", name)
		return
	}
	err := e.exec.HoldShortcut(combo, down)
	if down && p != nil && err == nil {
		p.started.Store(true)
	}
	if err != nil {
		e.logf("%s: %v", name, err)
	}
}

func (e *Engine) effect(name string, f func() error) {
	gesture, p := e.gesture, e.cur.press
	e.queued = true
	e.run(func() {
		if gesture == "up" && p != nil && p.hasDown && !p.started.Load() {
			e.logf("%s: its down action did not run; skipped the up action", name)
			return
		}
		if gesture == "down" && p != nil {
			// Started as soon as it begins: a release run beside a down
			// that is still going (at shutdown) must still finish it.
			p.started.Store(true)
		}
		err := f()
		if gesture == "down" && p != nil && errors.Is(err, ErrSkipped) {
			p.started.Store(false)
		}
		if err != nil && !errors.Is(err, ErrSkipped) {
			e.logf("%s: %v", name, err)
		}
	}, gesture == "up")
}

// pane is the current snapshot of a block, nil once it has closed.
func (e *Engine) pane(block string) *rex.Pane {
	for i := range e.panes {
		if e.panes[i].Block == block {
			return &e.panes[i]
		}
	}
	return nil
}

// agentPane is the selected agent if it still runs, else the most urgent one.
// ShellEnv is what a shell binding learns about its press: the key, gesture
// and active layer, and the selected agent pane (the one key and text
// bindings with an agent target use). With no agent the REX_MICRO_AGENT_*
// names are left out, not set empty. The daemon adds REX_MICRO_REX.
// docs/recipes.md lists these names; keep the two in step.
func ShellEnv(key, gesture, layer string, agent *rex.Pane) []string {
	env := []string{"REX_MICRO_KEY=" + key, "REX_MICRO_GESTURE=" + gesture, "REX_MICRO_LAYER=" + layer}
	if agent == nil {
		return env
	}
	for _, kv := range [][2]string{
		{"BLOCK", agent.Block}, {"SESSION", agent.Session}, {"WINDOW", agent.Window}, {"HOST", agent.Host},
		{"APP", agent.App}, {"STATE", agent.State}, {"KIND", agent.Kind}, {"MSG", agent.Msg},
		{"LABEL", agent.Label}, {"SESSION_LABEL", agent.SessionLabel},
	} {
		env = append(env, "REX_MICRO_AGENT_"+kv[0]+"="+kv[1])
	}
	return env
}

func (e *Engine) agentPane() *rex.Pane {
	for i := range e.panes {
		if e.panes[i].Block == e.selected && e.panes[i].Agent() {
			return &e.panes[i]
		}
	}
	return agents.MostUrgent(e.panes)
}

func (e *Engine) builtin(name, b string) {
	switch {
	case b == "none":
	case b == "layer.next":
		e.setLayer((e.layer + 1) % len(e.cfg.Layers))
	case b == "layer.prev":
		e.setLayer((e.layer + len(e.cfg.Layers) - 1) % len(e.cfg.Layers))
	case strings.HasPrefix(b, "layer."):
		for i, l := range e.cfg.Layers {
			if l.Name == strings.TrimPrefix(b, "layer.") {
				e.setLayer(i)
			}
		}
	case strings.HasPrefix(b, "session."):
		n, _ := strconv.Atoi(strings.TrimPrefix(b, "session."))
		var pick *rex.Pane
		for i := range e.panes {
			p := &e.panes[i]
			if p.SessionIndex == n && (pick == nil || p.Focused) {
				pick = p
			}
		}
		if pick == nil {
			e.logf("%s: no session %d", name, n)
			return
		}
		p := *pick
		e.effect(name, func() error { return e.exec.Show(p, false) })
	case b == "agent.show" || b == "agent.show_zoom":
		var p *rex.Pane
		if _, ok := agentIndex(name); ok {
			if e.cur != nil && e.cur.agent != "" {
				p = e.pane(e.cur.agent)
			}
		} else {
			p = e.agentPane()
		}
		if p == nil {
			return
		}
		e.show(name, *p, b == "agent.show_zoom")
	case b == "keys.show":
		e.showKeys(name)
	case b == "agent.attention":
		var waiting []rex.Pane
		for _, p := range e.panes {
			if p.State == "blocked" || p.State == "error" || p.State == "done" {
				waiting = append(waiting, p)
			}
		}
		if p := agents.MostUrgent(waiting); p != nil {
			e.show(name, *p, false)
		}
	default:
		e.logf("%s: unknown builtin %q", name, b)
	}
}

func (e *Engine) show(name string, p rex.Pane, zoom bool) {
	if p.Agent() {
		e.selected = p.Block
		if e.cur != nil {
			// The rest of this gesture (a press after a down that showed it)
			// acts on the agent just shown.
			cp := p
			e.cur.target = &cp
		}
	}
	e.effect(name, func() error { return e.exec.Show(p, zoom) })
}

func (e *Engine) setLayer(i int) {
	if i == e.layer {
		return
	}
	e.layer = i
	// Pending double-taps belong to the old layer; a key that is down
	// finishes its gesture with the binding it began with. An Agent Key that
	// already showed its agent keeps its tap while the new layer still has it
	// show and zoom: the show itself can switch the layer (the program it
	// brings into view), and the double completes the gesture it began.
	for name, s := range e.keys {
		if !s.down && (!s.pressed || !eager(name, e.binding(name))) {
			s.tapAt = time.Time{}
		}
	}
	e.logf("layer %d (%s)", i+1, e.cfg.Layers[i].Name)
	if e.OnLayer != nil {
		e.OnLayer(i)
	}
}

func agentIndex(name string) (int, bool) {
	if len(name) == 4 && strings.HasPrefix(name, "AG0") && name[3] >= '0' && name[3] <= '5' {
		return int(name[3] - '0'), true
	}
	return 0, false
}
