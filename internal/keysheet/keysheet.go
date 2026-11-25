// Package keysheet draws the active layer's keys as a page shaped like the
// pad, for the keys overlay (builtin keys.show).
package keysheet

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/JiehoonKwak/rex-micro/internal/agents"
	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	"github.com/JiehoonKwak/rex-micro/internal/macos"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
)

// Size is the overlay's size in points; the page is laid out to fill it.
const Width, Height = 760, 520

// Key is one control as the sheet shows it.
type Key struct {
	Cap  string   // as printed on the pad, e.g. FAST
	Main string   // what a press does; for an Agent Key, the agent's session
	Hint string   // the shortcut behind a labelled press, e.g. ⌘⇧Z
	More []string // other gestures, e.g. "hold: Tab search"
	Own  bool     // bound by the active layer itself, not fallen through

	// An Agent Key that shows its agent: the agent as the key's light shows
	// it. Slot is 1-6; State is "" for a key bound to something else.
	Slot        int
	State, Kind string // working, blocked (waiting for Kind), error, done, idle, empty
	Light       string // the state's color in [lighting.states]; "" when its light is off
	Host        string // the Rex host the agent runs on, "" for this Mac
	Sub         string // its status message, else its app
}

// Layer is one layer in the sheet's header.
type Layer struct{ Name, Color string }

// Sheet is the active layer and its keys.
type Sheet struct {
	Layer, Color, When string
	Index, Count       int
	Layers             []Layer
	Keys               map[string]Key
}

const defaultColor = "#8B5CF6"

// Build reads the active layer's keys from the engine.
func Build(e *engine.Engine, cfg *config.Config) Sheet {
	l := e.LayerConfig()
	s := Sheet{Layer: l.Name, Color: l.Color, When: l.Match.Describe(), Index: e.Layer() + 1, Count: len(cfg.Layers), Keys: map[string]Key{}}
	s.Color = cssColor(s.Color)
	for _, cl := range cfg.Layers {
		s.Layers = append(s.Layers, Layer{Name: cl.Name, Color: cssColor(cl.Color)})
	}
	for _, name := range config.KeyNames {
		b, from := e.Binding(name)
		// from is the active layer or the base (0); on the base nothing is
		// marked, since nothing falls through to it.
		k := Key{Cap: config.Caps[name], Main: "—", Own: from > 0}
		if agent, ok := agentKey(name); ok && showsAgent(b) {
			// The key's destination and its light, as the pad has them: in
			// sessions or slots mode a pane without an agent still opens.
			slot := e.AgentSlot(agent)
			k.Slot, k.State, k.Main = agent+1, slot.State, ""
			if k.State == "" {
				k.State = "empty"
			}
			if p := slot.Pane; p != nil {
				k.Kind, k.Host, k.Main = p.Kind, p.Host, p.SessionLabel
				if k.Main == "" {
					k.Main = p.Session
				}
				k.Sub = p.App
				if p.Msg != "" {
					k.Sub = p.Msg // as Rex's Activity shows it
				}
			}
			if l, err := agents.Look(k.State, k.Kind, cfg.Lighting).Resolve(cfg.Lighting.Brightness); err == nil && l.Brightness > 0 {
				k.Light = fmt.Sprintf("#%06X", l.Color)
			}
			// What the key does, unless it is the usual show, double to zoom.
			if !standardAgent(b) {
				for _, g := range []string{"press", "double", "hold", "down", "up"} {
					if a := b.Gesture(g); a != nil {
						k.More = append(k.More, g+": "+engine.Brief(a))
					}
				}
			}
			s.Keys[name] = k
			continue
		}
		for _, g := range []string{"press", "double", "hold", "down", "up"} {
			a := b.Gesture(g)
			if a == nil {
				continue
			}
			text, hint := engine.Brief(a), ""
			if a.Label != "" && a.Shortcut != "" && !plumbing(a.Shortcut) {
				hint = engine.Glyphs(a.Shortcut) // the keys too, to learn them
			}
			if g == "press" {
				k.Main, k.Hint = text, hint
			} else {
				if hint != "" {
					text += " " + hint
				}
				k.More = append(k.More, g+": "+text)
			}
		}
		if k.Main == "—" && len(k.More) > 0 {
			k.Main, k.More = k.More[0], k.More[1:]
		}
		s.Keys[name] = k
	}
	return s
}

// plumbing reports whether a shortcut is a key no keyboard has (F13-F24),
// which a config binds only to reach something else, such as a Rex keymap;
// showing it teaches nothing.
func plumbing(combo string) bool {
	f := macos.Fields(combo)
	if len(f) != 1 || !strings.HasPrefix(f[0], "f") {
		return false
	}
	n, err := strconv.Atoi(f[0][1:])
	return err == nil && n >= 13 && n <= 24
}

// standardAgent reports whether b is exactly press to show the agent and
// double-press to show it zoomed, which the tile needs no words for.
func standardAgent(b *config.KeyBinding) bool {
	for _, g := range []string{"hold", "down", "up"} {
		if b.Gesture(g) != nil {
			return false
		}
	}
	p, d := b.Gesture("press"), b.Gesture("double")
	return p != nil && d != nil && p.Builtin == "agent.show" && d.Builtin == "agent.show_zoom"
}

// cssColor is a config color ("#RRGGBB" or "RRGGBB") as CSS takes it.
func cssColor(c string) string {
	if v, err := protocol.ParseColor(c); err == nil {
		return fmt.Sprintf("#%06X", v)
	}
	return defaultColor
}

func agentKey(name string) (int, bool) {
	if len(name) == 4 && strings.HasPrefix(name, "AG0") && name[3] >= '0' && name[3] <= '5' {
		return int(name[3] - '0'), true
	}
	return 0, false
}

// showsAgent reports whether every gesture of b only shows the key's agent.
func showsAgent(b *config.KeyBinding) bool {
	any := false
	for _, g := range []string{"press", "double", "hold", "down", "up"} {
		if a := b.Gesture(g); a != nil {
			if !strings.HasPrefix(a.Builtin, "agent.show") {
				return false
			}
			any = true
		}
	}
	return any
}

// HTML renders the sheet as a whole page for the overlay window, laid out
// like the pad. It follows the system's light or dark appearance. On a layer
// other than the base, the keys it leaves to the base recede.
func HTML(s Sheet) string {
	var b strings.Builder
	esc := html.EscapeString
	layered := s.Index > 1
	cls := func(base string, k Key) string {
		// An Agent Key shows its agent on every layer; only bindings recede.
		if layered && !k.Own && k.Slot == 0 {
			base += " inherited"
		}
		return base
	}
	agent := func(name string) {
		k := s.Keys[name]
		if k.Slot == 0 {
			action(&b, s, name, cls)
			return
		}
		c := cls("key agent state-"+k.State, k)
		if len(k.More) > 0 {
			c += " gestures"
		}
		dot := `<span class="led off"></span>`
		if k.Light != "" {
			dot = fmt.Sprintf(`<span class="led" style="--led:%s"></span>`, esc(k.Light))
		}
		word := stateWord(k.State, k.Kind)
		host := ""
		if k.Host != "" {
			host = `<span class="host">` + esc(k.Host) + `</span>`
		}
		fmt.Fprintf(&b, `<div class="%s" style="--led:%s"><div class="top">%s<span class="state">%s</span>%s<span class="slot">%d</span></div>`, c, esc(orDim(k.Light)), dot, esc(word), host, k.Slot)
		fmt.Fprintf(&b, `<div class="title">%s</div>`, esc(k.Main))
		for _, m := range k.More {
			fmt.Fprintf(&b, `<div class="more">%s</div>`, esc(m))
		}
		fmt.Fprintf(&b, `<div class="sub">%s</div></div>`, esc(k.Sub))
	}
	well := func(title string, names ...string) {
		b.WriteString(`<div class="well">`)
		fmt.Fprintf(&b, `<div class="cap">%s</div>`, esc(title))
		for _, n := range names {
			k := s.Keys[n]
			glyph := strings.SplitN(k.Cap, " ", 2)[0]
			fmt.Fprintf(&b, `<div class="%s"><span class="g">%s</span><span class="label">%s</span>%s</div>`, cls("row", k), esc(glyph), esc(k.Main), hint(k.Hint))
		}
		// Other gestures after every control's press, so that a crowded well
		// fades these out rather than cutting a control.
		b.WriteString(`<div class="extras">`)
		for _, n := range names {
			k := s.Keys[n]
			glyph := strings.SplitN(k.Cap, " ", 2)[0]
			for _, m := range k.More {
				fmt.Fprintf(&b, `<div class="%s"><span class="g">%s</span><span class="more">%s</span></div>`, cls("row extra", k), esc(glyph), esc(m))
			}
		}
		b.WriteString(`</div></div>`)
	}
	when := ""
	if s.When != "" {
		when = `<span class="when">on while ` + esc(s.When) + `</span>`
	}
	fmt.Fprintf(&b, `<!doctype html><html><head><meta charset="utf-8"><style>%s</style></head><body style="--accent:%s"><div class="card"><div class="head"><div class="layers">`, css, esc(s.Color))
	for i, l := range s.Layers {
		c := "layer"
		if i+1 == s.Index {
			c += " active"
		}
		fmt.Fprintf(&b, `<span class="%s"><span class="dot" style="background:%s"></span><span class="lname">%s</span></span>`, c, esc(l.Color), esc(l.Name))
	}
	fmt.Fprintf(&b, `</div>%s</div><div class="grid">`, when)
	well("Dial", "ENC_CC", "ENC_CLK", "ENC_CW")
	agent("AG00")
	agent("AG01")
	well("Stick", "STICK_UP", "STICK_RIGHT", "STICK_DOWN", "STICK_LEFT")
	for _, n := range []string{"AG02", "AG03", "AG04", "AG05"} {
		agent(n)
	}
	for _, n := range []string{"ACT06", "ACT07", "ACT08", "ACT09", "MIC", "ACT12"} {
		action(&b, s, n, cls)
	}
	b.WriteString(`</div></div></body></html>`)
	return b.String()
}

// action draws a key that does one thing: its printed cap, the shortcut
// behind it, and what it does, largest.
func action(b *strings.Builder, s Sheet, name string, cls func(string, Key) string) {
	esc := html.EscapeString
	k := s.Keys[name]
	c := "key action"
	if name == "MIC" {
		c += " wide"
	}
	if k.Own && s.Index > 1 {
		c += " own"
	}
	h := ""
	if k.Hint != "" {
		h = `<span class="hint">` + esc(k.Hint) + `</span>`
	}
	if len(k.More) > 0 {
		c += " gestures"
	}
	fmt.Fprintf(b, `<div class="%s"><div class="top"><span class="cap">%s</span>%s</div><div class="label">%s</div>`, cls(c, k), esc(k.Cap), h, esc(k.Main))
	for _, m := range k.More {
		fmt.Fprintf(b, `<div class="more">%s</div>`, esc(m))
	}
	b.WriteString(`</div>`)
}

// stateWord names a state as the overlay prints it; a blocked agent by what
// it waits for, when it said.
func stateWord(state, kind string) string {
	if state == "blocked" && kind != "" {
		state = kind
	}
	if state == "empty" {
		return "No agent"
	}
	return strings.ToUpper(state[:1]) + state[1:]
}

func orDim(color string) string {
	if color == "" {
		return "var(--dim)"
	}
	return color
}

func hint(h string) string {
	if h == "" {
		return ""
	}
	return `<span class="hint">` + html.EscapeString(h) + `</span>`
}

const css = `
:root {
  --deck: #F2F3F5; --edge: #D8DCE2; --key: #FFFFFF; --line: #E0E3E9; --well: #E7E9EE;
  --fg: #14171F; --dim: #5A6170; --cap: #7B8394; --mix: 0%;
}
@media (prefers-color-scheme: dark) {
  :root { --deck: #16171A; --edge: #2C2E35; --key: #222429; --line: #32353E; --well: #111215;
    --fg: #F3F4F6; --dim: #9CA3AF; --cap: #767E8D; }
}
* { box-sizing: border-box; margin: 0; }
html, body { background: transparent; height: 100%; overflow: hidden; }
body { font: 13px -apple-system, BlinkMacSystemFont, sans-serif; color: var(--fg); padding: 10px; -webkit-user-select: none; cursor: default; }
.card { height: 100%; background: var(--deck); border-radius: 18px; padding: 14px 18px 18px; border: 1px solid var(--edge); box-shadow: 0 10px 40px rgba(0,0,0,.35); display: flex; flex-direction: column; gap: 12px; }
.head { display: flex; align-items: center; justify-content: space-between; gap: 12px; height: 28px; }
.layers { display: flex; align-items: center; gap: 14px; min-width: 0; overflow: hidden; }
.layer { display: flex; align-items: center; gap: 7px; color: var(--dim); font-size: 14px; white-space: nowrap; min-width: 0; flex: 0 1 auto; overflow: hidden; text-overflow: ellipsis; }
.layer .dot { flex: none; }
.layer:not(.active) { min-width: 3.6em; }
.lname { overflow: hidden; text-overflow: ellipsis; min-width: 0; }
.extras { flex: 1 1 0; min-height: 0; overflow: hidden; display: flex; flex-direction: column; gap: 3px; -webkit-mask-image: linear-gradient(to bottom, #000 calc(100% - 12px), transparent); }
.layer .dot { width: 7px; height: 7px; border-radius: 50%; opacity: .7; }
.layer.active { color: var(--fg); font-size: 18px; font-weight: 700; flex: 0 1 auto; min-width: 5em; }
.layer.active .dot { width: 10px; height: 10px; opacity: 1; }
.when { color: var(--dim); font-size: 13px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; flex: 0 1 auto; }
.grid { flex: 1; display: grid; grid-template-columns: 1.22fr 1fr 1fr 1.22fr; grid-template-rows: 1.35fr 1.2fr 1fr 1fr; gap: 10px; min-height: 0; }
.key { background: var(--key); border: 1px solid var(--line); border-radius: 12px; padding: 10px 12px; overflow: hidden; display: flex; flex-direction: column; min-width: 0; box-shadow: 0 1px 2px rgba(0,0,0,.06); }
.well { background: var(--well); border-radius: 12px; padding: 10px 12px; overflow: hidden; display: flex; flex-direction: column; gap: 3px; min-width: 0; min-height: 0; }
.row { flex: none; }
.row.extra .g { visibility: hidden; }
.wide { grid-column: span 3; }
.top { display: flex; align-items: center; gap: 7px; min-height: 16px; }
.cap { font-size: 10.5px; font-weight: 600; letter-spacing: .07em; text-transform: uppercase; color: var(--cap); flex: 1; }
.well .cap { flex: none; margin-bottom: 4px; }
.hint { color: var(--dim); font-size: 12.5px; font-weight: 500; white-space: nowrap; margin-left: auto; padding-left: 6px; }
.action .label { margin-top: auto; font-size: 16px; font-weight: 600; line-height: 1.2; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.more { color: var(--dim); font-size: 12.5px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
.gestures .label { -webkit-line-clamp: 1; }
.row { display: flex; align-items: baseline; gap: 8px; min-width: 0; }
.row .g { width: 14px; flex: none; text-align: center; color: var(--cap); font-size: 12px; }
.row .label { font-size: 14px; font-weight: 600; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.row .hint { font-size: 11.5px; padding-left: 4px; }
.led { width: 9px; height: 9px; border-radius: 50%; background: var(--led); flex: none; box-shadow: inset 0 0 0 1px rgba(127,127,127,.45); }
.led.off { background: none; box-shadow: inset 0 0 0 1.5px var(--cap); }
.state { font-size: 13px; font-weight: 600; color: var(--fg); color: color-mix(in srgb, var(--led) 78%, var(--fg)); flex: 1 0 auto; min-width: 3.5em; max-width: 100%; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.state-idle .state, .state-empty .state { color: var(--dim); font-weight: 500; }
.agent .more { margin-top: 4px; white-space: normal; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
.agent.gestures .title { -webkit-line-clamp: 1; }
.slot { font-size: 12px; color: var(--cap); font-variant-numeric: tabular-nums; }
.title { margin-top: 8px; font-size: 15px; font-weight: 600; line-height: 1.3; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.host { color: var(--dim); font-size: 12px; font-weight: 600; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; flex: 0 1 auto; min-width: 0; max-width: 45%; }
.slot { flex: none; }
.sub { margin-top: auto; color: var(--dim); font-size: 13px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.state-blocked, .state-error { border-color: var(--led); background: color-mix(in srgb, var(--led) 9%, var(--key)); border-color: color-mix(in srgb, var(--led) 40%, var(--line)); }
.own { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 8%, var(--key)); border-color: color-mix(in srgb, var(--accent) 45%, var(--line)); }
.inherited { opacity: .5; }
.key.inherited { box-shadow: none; }
`
