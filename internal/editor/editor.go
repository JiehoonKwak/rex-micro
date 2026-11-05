// Package editor is `rex-micro edit`: a full-screen editor for the config.
// Press a control on the pad (or move with the arrow keys) to select it, pick
// what each gesture does from a searchable list, and manage layers. Every
// change is a line edit through package tomledit, checked by parsing the
// result, so the file keeps its comments and order.
package editor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	"github.com/JiehoonKwak/rex-micro/internal/tomledit"
)

// RexAction is one entry of `rex actions`.
type RexAction struct {
	Name, Title, Category string
	Args                  []string // argument names; required ones end in "!"
}

// Options are the editor's inputs; tests fake all of them.
type Options struct {
	Path     string
	Text     string        // the file's text, or the default config when it is missing
	Pad      <-chan string // names of controls pressed on the pad; nil without a pad
	PadNote  string        // why there is no pad, shown in the help line
	Actions  []RexAction
	Programs []string // programs seen in Rex panes, for match rules
	Apps     func() []string
	// Save writes text. Unless force, it returns ErrChanged when the file
	// changed on disk since it was opened or last saved.
	Save func(text string, force bool) error
}

// ErrChanged means someone else changed the file while the editor had it open.
var ErrChanged = errors.New("the file changed on disk since you opened it")

type mode int

const (
	modeMain mode = iota
	modeList
	modeInput
	modeConfirm
)

type focus int

const (
	focusPad focus = iota
	focusGestures
)

// Model is the bubbletea model.
type Model struct {
	opts   Options
	doc    *tomledit.Doc
	cfg    *config.Config
	saved  string
	layer  int
	sel    int
	focus  focus
	ges    int
	mode   mode
	list   list.Model
	input  textinput.Model
	next   func(string) // what the open list item or input does with its value
	ask    string       // confirmation question
	yes    func()
	status string
	bad    bool
	width  int
	height int
	quit   bool
}

var gestures = []string{"press", "double", "hold", "down", "up"}

// gesturesOf lists the gestures a control has; dial turns and stick
// directions only press.
func gesturesOf(key string) []string {
	if config.PressOnly(key) {
		return gestures[:1]
	}
	return gestures
}

// New builds the editor model from the file text.
func New(opts Options) (*Model, error) {
	doc := tomledit.Parse(opts.Text)
	if doc.Layers() == 0 {
		// Only defaults: write the default layer out so it can be edited.
		i := strings.Index(config.Default, "[[layer]]")
		doc = tomledit.Parse(strings.TrimRight(opts.Text, "\n") + "\n\n" + config.Default[i:])
	}
	cfg, err := config.Parse(doc.String())
	if err != nil {
		return nil, fmt.Errorf("%s: %w; fix it first (rex-micro doctor)", opts.Path, err)
	}
	ti := textinput.New()
	ti.CharLimit = 400
	m := &Model{opts: opts, doc: doc, cfg: cfg, saved: opts.Text, input: ti, width: 100, height: 40}
	return m, nil
}

// Text is the edited file.
func (m *Model) Text() string { return m.doc.String() }

func (m *Model) dirty() bool { return m.doc.String() != m.saved }

type padMsg string
type padClosed struct{}

func (m *Model) waitPad() tea.Cmd {
	if m.opts.Pad == nil {
		return nil
	}
	ch := m.opts.Pad
	return func() tea.Msg {
		name, ok := <-ch
		if !ok {
			return padClosed{}
		}
		return padMsg(name)
	}
}

func (m *Model) Init() tea.Cmd { return m.waitPad() }

// edit applies change to a copy of the document and keeps it only when the
// result is a valid config that differs from the current one only where
// allowed says it may: a line edit gone wrong must not take other settings
// with it, even when what is left still parses.
func (m *Model) edit(what string, allowed func(path, before, after string) bool, change func(d *tomledit.Doc) error) bool {
	d := tomledit.Parse(m.doc.String())
	if err := change(d); err != nil {
		m.flash(err.Error(), true)
		return false
	}
	cfg, err := config.Parse(d.String())
	if err != nil {
		m.flash("not applied: "+err.Error(), true)
		return false
	}
	before, after := flatten(m.cfg), flatten(cfg)
	for path := range merge(before, after) {
		if before[path] != after[path] && !allowed(path, before[path], after[path]) {
			m.flash(fmt.Sprintf("not applied: the edit would also change %s; please report this", path), true)
			return false
		}
	}
	m.doc, m.cfg = d, cfg
	if m.layer >= len(cfg.Layers) {
		m.layer = len(cfg.Layers) - 1
	}
	m.flash(what+" (unsaved; s saves)", false)
	return true
}

func (m *Model) flash(s string, bad bool) { m.status, m.bad = s, bad }

// flatten lists every setting by path, layers by name, for edit's check.
func flatten(c *config.Config) map[string]string {
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) } // maps sorted, pointers followed
	out := map[string]string{
		"rex": js(c.Rex), "agents": js(c.Agents), "lighting": js(c.Lighting), "stick": js(c.Stick), "timing": js(c.Timing),
	}
	var names []string
	for _, l := range c.Layers {
		names = append(names, l.Name)
		p := "layer " + l.Name
		out[p+" color"] = l.Color
		if l.Match != nil {
			out[p+" match"] = l.Match.TOML()
		}
		for k, b := range l.Keys {
			out[p+" keys."+k] = b.TOML()
		}
	}
	out["layer order"] = strings.Join(names, ", ")
	return out
}

func merge(a, b map[string]string) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// only allows changes to exactly these paths.
func only(paths ...string) func(string, string, string) bool {
	return func(p, _, _ string) bool {
		for _, q := range paths {
			if p == q {
				return true
			}
		}
		return false
	}
}

// layerPaths allows any change to the named layers and the layer order.
func layerPaths(names ...string) func(string, string, string) bool {
	return func(p, _, _ string) bool {
		if p == "layer order" {
			return true
		}
		for _, n := range names {
			if strings.HasPrefix(p, "layer "+n+" ") {
				return true
			}
		}
		return false
	}
}

func (m *Model) key() string { return slots[m.sel].key }

// own is the selected layer's binding for key; from says where the effective
// binding comes from: "layer", "base", "agent" or "".
func (m *Model) binding(key string) (b *config.KeyBinding, from string) {
	if _, ag := agentKey(key); ag && m.cfg.Agents.Always {
		return agentBinding(m.cfg), "agent"
	}
	if b := m.cfg.Layers[m.layer].Keys[key]; b != nil {
		return b, "layer"
	}
	if b := m.cfg.Layers[0].Keys[key]; b != nil {
		return b, "base"
	}
	if _, ag := agentKey(key); ag {
		return agentBinding(m.cfg), "agent"
	}
	return nil, ""
}

func agentKey(key string) (int, bool) {
	if len(key) == 4 && strings.HasPrefix(key, "AG0") && key[3] >= '0' && key[3] <= '5' {
		return int(key[3] - '0'), true
	}
	return 0, false
}

func agentBinding(cfg *config.Config) *config.KeyBinding {
	act := func(v string) *config.Action {
		switch v {
		case "show":
			return &config.Action{Builtin: "agent.show"}
		case "show_zoom":
			return &config.Action{Builtin: "agent.show_zoom"}
		}
		return nil
	}
	return &config.KeyBinding{Press: act(cfg.Agents.Press), Double: act(cfg.Agents.Double)}
}

// explicit copies b with every gesture in its own field.
func explicit(b *config.KeyBinding) *config.KeyBinding {
	out := &config.KeyBinding{}
	if b == nil {
		return out
	}
	cp := func(a *config.Action) *config.Action {
		if a == nil {
			return nil
		}
		c := *a
		return &c
	}
	out.Press, out.Double, out.Hold, out.Down, out.Up = cp(b.Gesture("press")), cp(b.Double), cp(b.Hold), cp(b.Down), cp(b.Up)
	return out
}

func setGesture(b *config.KeyBinding, g string, a *config.Action) {
	switch g {
	case "press":
		b.Press = a
	case "double":
		b.Double = a
	case "hold":
		b.Hold = a
	case "down":
		b.Down = a
	case "up":
		b.Up = a
	}
}

// assign sets gesture g of the selected key on this layer.
func (m *Model) assign(g string, a *config.Action) {
	key := m.key()
	cur, _ := m.binding(key)
	b := explicit(cur) // start from what the key does now, so other gestures stay
	setGesture(b, g, a)
	empty := true
	for _, x := range gestures {
		if b.Gesture(x) != nil {
			empty = false
		}
	}
	text := ""
	if !empty {
		text = b.TOML()
	}
	what := fmt.Sprintf("%s %s: %s", key, g, describe(a))
	if a == nil {
		what = fmt.Sprintf("%s %s cleared", key, g)
	}
	// A key written as a [layer.keys.KEY] table of gestures is edited one
	// gesture at a time, so the others keep their comments.
	perGesture := !empty && m.cfg.Layers[m.layer].Keys[key] != nil && m.doc.GestureTable(m.layer, key)
	m.edit(what, only("layer "+m.cfg.Layers[m.layer].Name+" keys."+key), func(d *tomledit.Doc) error {
		if perGesture {
			val := ""
			if a != nil {
				val = a.TOML()
			}
			return d.SetGesture(m.layer, key, g, val)
		}
		return d.SetKey(m.layer, key, text)
	})
}

// describe names an action briefly for the screen.
func describe(a *config.Action) string {
	if a == nil {
		return "-"
	}
	switch a.Kind() {
	case "builtin":
		switch b := a.Builtin; {
		case b == "none":
			return "nothing"
		case b == "agent.show":
			return "show agent"
		case b == "agent.show_zoom":
			return "show agent, zoomed"
		case b == "agent.attention":
			return "most urgent agent"
		case b == "layer.next" || b == "layer.prev":
			return map[string]string{"layer.next": "next layer", "layer.prev": "previous layer"}[b]
		case strings.HasPrefix(b, "layer."):
			return "layer " + strings.TrimPrefix(b, "layer.")
		case strings.HasPrefix(b, "session."):
			return "session " + strings.TrimPrefix(b, "session.")
		}
	case "action":
		if len(a.Args) == 0 {
			return a.Action
		}
	}
	return engine.Summary(a)
}

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.mode == modeList {
			m.list.SetSize(min(m.width, 90), max(m.height-4, 8))
		}
		return m, nil
	case padMsg:
		if m.mode == modeMain {
			for i, s := range slots {
				if s.key == string(msg) {
					m.sel = i
				}
			}
			m.flash("selected "+string(msg)+" from the pad", false)
		}
		return m, m.waitPad()
	case padClosed:
		m.flash("the pad disconnected; use the arrow keys", true)
		return m, nil
	}
	switch m.mode {
	case modeList:
		return m.updateList(msg)
	case modeInput:
		return m.updateInput(msg)
	case modeConfirm:
		if k, ok := msg.(tea.KeyMsg); ok {
			m.mode = modeMain
			if k.String() == "y" && m.yes != nil {
				m.yes()
			} else {
				m.flash("cancelled", false)
			}
			if m.quit {
				return m, tea.Quit
			}
		}
		return m, nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	return m.updateMain(k)
}

func (m *Model) updateMain(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c", "q":
		if !m.dirty() {
			return m, tea.Quit
		}
		m.confirm("Quit without saving? y discards your changes", func() { m.quit = true })
		return m, nil
	case "s", "ctrl+s":
		m.save(false)
		return m, nil
	case "S":
		m.save(true)
		return m, nil
	case "tab", "]":
		m.layer = (m.layer + 1) % len(m.cfg.Layers)
		return m, nil
	case "shift+tab", "[":
		m.layer = (m.layer + len(m.cfg.Layers) - 1) % len(m.cfg.Layers)
		return m, nil
	case "l", "L":
		m.layerMenu()
		return m, nil
	case "A":
		on := !m.cfg.Agents.Always
		m.edit(fmt.Sprintf("agents.always = %v", on), only("agents"), func(d *tomledit.Doc) error {
			return d.SetGlobal("agents", "always", fmt.Sprint(on))
		})
		return m, nil
	case "r":
		key := m.key()
		if m.cfg.Layers[m.layer].Keys[key] == nil {
			m.flash(key+" has no binding of its own on this layer", false)
			return m, nil
		}
		m.edit(key+" reset to "+map[bool]string{true: "unbound", false: "the base layer"}[m.layer == 0],
			only("layer "+m.cfg.Layers[m.layer].Name+" keys."+key), func(d *tomledit.Doc) error { return d.SetKey(m.layer, key, "") })
		return m, nil
	}
	if m.focus == focusGestures {
		gs := gesturesOf(m.key())
		if m.ges >= len(gs) { // the pad picked a control with fewer gestures
			m.ges = 0
		}
		switch k.String() {
		case "esc", "left", "h":
			m.focus = focusPad
		case "up", "k":
			m.ges = (m.ges + len(gs) - 1) % len(gs)
		case "down", "j":
			m.ges = (m.ges + 1) % len(gs)
		case "enter", "right":
			m.picker(gs[m.ges])
		case "x", "backspace", "delete":
			m.assign(gs[m.ges], nil)
		}
		return m, nil
	}
	switch k.String() {
	case "right":
		m.sel = (m.sel + 1) % len(slots)
	case "left", "h":
		m.sel = (m.sel + len(slots) - 1) % len(slots)
	case "down", "j":
		m.sel = moveRow(m.sel, +1)
	case "up", "k":
		m.sel = moveRow(m.sel, -1)
	case "enter":
		m.focus, m.ges = focusGestures, 0
	case "x", "backspace", "delete":
		m.assign("press", nil)
	}
	return m, nil
}

func (m *Model) confirm(q string, yes func()) {
	m.mode, m.ask, m.yes = modeConfirm, q, yes
}

func (m *Model) save(force bool) {
	if !m.dirty() {
		m.flash("nothing to save", false)
		return
	}
	text := m.doc.String()
	err := m.opts.Save(text, force)
	if errors.Is(err, ErrChanged) {
		m.flash("not saved: "+err.Error()+". S overwrites those changes; q quits and keeps them", true)
		return
	}
	if err != nil {
		m.flash("save failed: "+err.Error(), true)
		return
	}
	m.saved = text
	m.flash("saved "+m.opts.Path, false)
}
