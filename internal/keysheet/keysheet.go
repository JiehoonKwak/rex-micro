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

// HTML renders the sheet as a whole page for the overlay window, drawn as
// the pad looks in a dark room: Agent Keys lit in their light's color, the
// dial as a knob and the stick with its four directions around it. The pad
// is dark whatever the system appearance, as the hardware is. On a layer
// other than the base, the layer's own bindings glow in its color and the
// keys it leaves to the base stay unlit.
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
		c := "key agent state-" + k.State
		style := ""
		if k.Light != "" {
			c += " lit"
			style = fmt.Sprintf(` style="--led:%s"`, esc(k.Light))
		}
		if len(k.More) > 0 {
			c += " gestures"
		}
		host := ""
		if k.Host != "" {
			host = `<span class="host">` + esc(k.Host) + `</span>`
		}
		fmt.Fprintf(&b, `<div class="%s"%s><div class="top"><span class="slot">%d</span><span class="state">%s</span>%s</div>`, cls(c, k), style, k.Slot, esc(stateWord(k.State, k.Kind)), host)
		fmt.Fprintf(&b, `<div class="title">%s</div>`, esc(k.Main))
		for _, m := range k.More {
			fmt.Fprintf(&b, `<div class="more">%s</div>`, esc(m))
		}
		fmt.Fprintf(&b, `<div class="sub">%s</div></div>`, esc(k.Sub))
	}
	// label is one control of the dial or stick: what it does, then the
	// shortcut behind it.
	label := func(c, name string) string {
		k := s.Keys[name]
		return fmt.Sprintf(`<div class="%s"><span class="label">%s</span>%s</div>`, cls(c, k), esc(k.Main), hint(k.Hint))
	}
	// extras are the dial's or stick's other gestures, under the control.
	extras := func(names ...string) string {
		var e strings.Builder
		for _, n := range names {
			k := s.Keys[n]
			glyph := strings.SplitN(k.Cap, " ", 2)[0]
			for _, m := range k.More {
				fmt.Fprintf(&e, `<div class="%s">%s %s</div>`, cls("more", k), esc(glyph), esc(m))
			}
		}
		if e.Len() == 0 {
			return ""
		}
		return `<div class="extras">` + e.String() + `</div>`
	}
	when := ""
	if s.When != "" {
		when = `<span class="when">on while ` + esc(s.When) + `</span>`
	}
	fmt.Fprintf(&b, `<!doctype html><html><head><meta charset="utf-8"><style>%s</style></head><body style="--accent:%s"><div class="card"><div class="head"><span class="leds">`, css, esc(s.Color))
	for i, l := range s.Layers {
		c := "led-bar"
		if i+1 == s.Index {
			c += " on"
		}
		fmt.Fprintf(&b, `<i class="%s" style="--c:%s" title="%s"></i>`, c, esc(l.Color), esc(l.Name))
	}
	fmt.Fprintf(&b, `</span><span class="layer">%s</span>%s</div><div class="grid">`, esc(s.Layer), when)
	fmt.Fprintf(&b, `<div class="control dial"><div class="knob-row">%s<div class="knob">%s</div>%s</div>%s</div>`,
		label("turn left", "ENC_CC"), label("press", "ENC_CLK"), label("turn right", "ENC_CW"), extras("ENC_CC", "ENC_CLK", "ENC_CW"))
	agent("AG00")
	agent("AG01")
	fmt.Fprintf(&b, `<div class="control stick"><div class="compass">%s%s<div class="nub"></div>%s%s</div>%s</div>`,
		label("dir n", "STICK_UP"), label("dir w", "STICK_LEFT"), label("dir e", "STICK_RIGHT"), label("dir s", "STICK_DOWN"),
		extras("STICK_UP", "STICK_RIGHT", "STICK_DOWN", "STICK_LEFT"))
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
	if len(k.More) > 0 {
		c += " gestures"
	}
	fmt.Fprintf(b, `<div class="%s"><div class="top"><span class="cap">%s</span>%s</div><div class="label">%s</div>`, cls(c, k), esc(k.Cap), hint(k.Hint), esc(k.Main))
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

func hint(h string) string {
	if h == "" {
		return ""
	}
	return `<span class="hint">` + html.EscapeString(h) + `</span>`
}

const css = `
:root {
  --pad: #0E0F12; --edge: rgba(255,255,255,.08); --cap-bg: #1E2026; --hi: rgba(255,255,255,.07); --chip: rgba(255,255,255,.08);
  --fg: #F2F3F5; --dim: #9097A3; --print: #7D8491; --faint: #5E6470;
}
* { box-sizing: border-box; margin: 0; }
html, body { background: transparent; height: 100%; overflow: hidden; }
body { font: 13px -apple-system, BlinkMacSystemFont, sans-serif; color: var(--fg); padding: 10px; -webkit-user-select: none; cursor: default; }
.card { height: 100%; background: var(--pad); border-radius: 22px; padding: 14px 18px 18px; border: 1px solid var(--edge); box-shadow: inset 0 1px 0 rgba(255,255,255,.06), 0 14px 44px rgba(0,0,0,.5); display: flex; flex-direction: column; gap: 12px; }
.head { display: flex; align-items: center; gap: 12px; height: 26px; min-width: 0; }
.leds { display: flex; gap: 5px; flex: none; }
.led-bar { width: 18px; height: 5px; border-radius: 3px; background: rgba(255,255,255,.14); }
.led-bar.on { background: var(--c); box-shadow: 0 0 10px var(--c); }
.layer { font-size: 16px; font-weight: 700; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; flex: 0 1 auto; }
.when { color: var(--dim); font-size: 13px; margin-left: auto; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; flex: 0 1 auto; }
.grid { flex: 1; display: grid; grid-template-columns: 1.25fr 1fr 1fr 1.25fr; grid-template-rows: 1.3fr 1.15fr 1fr .85fr; gap: 10px; min-height: 0; }
.key { background: var(--cap-bg); border-radius: 12px; padding: 9px 11px; overflow: hidden; display: flex; flex-direction: column; min-width: 0; min-height: 0;
  box-shadow: inset 0 1px 0 var(--hi), inset 0 -3px 0 rgba(0,0,0,.55); }
.wide { grid-column: span 3; }
.top { display: flex; align-items: center; gap: 7px; min-height: 16px; min-width: 0; }
.cap { font: 700 10px ui-monospace, SFMono-Regular, Menlo, monospace; letter-spacing: .12em; text-transform: uppercase; color: var(--print); flex: 1; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.hint { font: 600 11.5px ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--dim); background: var(--chip); border-radius: 5px; padding: 1px 6px; white-space: nowrap; flex: none; }
.action .label { margin-top: auto; font-size: 16px; font-weight: 650; line-height: 1.2; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.gestures .label, .agent.gestures .title { -webkit-line-clamp: 1; }
.more { color: var(--dim); font-size: 12px; line-height: 1.3; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
.action .more:first-of-type { margin-top: 3px; }

/* Agent Keys, lit from below in their light's color; idle glows least. */
.agent { --glow: 58%; }
.agent.state-idle { --glow: 26%; }
.agent.state-blocked, .agent.state-error { --glow: 70%; }
.agent.lit { background: radial-gradient(120% 120% at 50% 112%, color-mix(in srgb, var(--led) var(--glow), transparent), color-mix(in srgb, var(--led) 16%, var(--cap-bg)) 72%);
  box-shadow: inset 0 1px 0 rgba(255,255,255,.13), inset 0 -3px 0 rgba(0,0,0,.55), 0 0 20px color-mix(in srgb, var(--led) calc(var(--glow) / 2), transparent); }
.agent:not(.lit) { background: #17181D; box-shadow: inset 0 1px 0 rgba(255,255,255,.05); }
.agent:not(.lit) .state { color: var(--faint); font-weight: 500; }
.slot { font: 700 11px ui-monospace, SFMono-Regular, Menlo, monospace; color: rgba(255,255,255,.62); flex: none; }
.agent:not(.lit) .slot { color: var(--faint); }
.state { font-size: 12.5px; font-weight: 750; flex: 1 1 auto; min-width: 3em; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.host { font-size: 11.5px; font-weight: 650; color: rgba(255,255,255,.75); background: rgba(0,0,0,.28); border-radius: 5px; padding: 1px 6px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; max-width: 50%; }
.title { margin-top: auto; font-size: 14.5px; font-weight: 650; line-height: 1.22; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.sub { color: rgba(255,255,255,.66); font-size: 11.5px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-height: 1.2em; }
.agent:not(.lit) .sub { color: var(--dim); }
.agent .more { color: rgba(255,255,255,.7); }

/* The dial and the stick, drawn as the controls they are. */
.control { display: flex; flex-direction: column; justify-content: center; gap: 6px; min-width: 0; min-height: 0; overflow: hidden; }
.control .label { font-size: 12.5px; font-weight: 650; line-height: 1.15; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.control .hint { font-size: 10.5px; padding: 0 5px; margin-top: 2px; display: inline-block; }
.knob-row { display: grid; grid-template-columns: 1fr 70px 1fr; align-items: center; gap: 6px; min-width: 0; }
.knob-row > div { min-width: 0; }
.turn.right { text-align: right; }
.turn::before { display: block; font: 600 10px ui-monospace, Menlo, monospace; color: var(--print); }
.turn.left::before { content: "⟲"; } .turn.right::before { content: "⟳"; }
.knob { width: 70px; height: 70px; border-radius: 50%; display: grid; place-items: center; text-align: center; padding: 8px;
  background: repeating-conic-gradient(rgba(255,255,255,.07) 0 3deg, transparent 3deg 9deg), radial-gradient(circle at 40% 35%, #5A5F6A, #24262B 70%); box-shadow: 0 4px 10px rgba(0,0,0,.55), inset 0 1px 0 rgba(255,255,255,.2); }
.knob .label { font-size: 11px; -webkit-line-clamp: 3; }
.knob .hint { display: none; }
.compass { display: grid; grid-template-columns: 1fr 30px 1fr; grid-template-rows: auto 30px auto; gap: 4px 6px; align-items: center; text-align: center; min-width: 0; }
.compass > div { min-width: 0; }
.dir.n { grid-column: 1 / -1; } .dir.s { grid-column: 1 / -1; grid-row: 3; }
.dir.w { text-align: left; } .dir.e { text-align: right; }
.nub { width: 30px; height: 30px; border-radius: 50%; background: radial-gradient(circle at 40% 35%, #60656F, #202227 75%); box-shadow: 0 3px 8px rgba(0,0,0,.6); }
.extras { display: flex; flex-direction: column; gap: 2px; text-align: center; min-height: 0; overflow: hidden; }
.extras .more { font-size: 11px; }

/* A layer's own bindings glow in its color; what it leaves to the base is unlit. */
.own { background: radial-gradient(130% 130% at 50% 120%, color-mix(in srgb, var(--accent) 42%, transparent), var(--cap-bg) 72%);
  box-shadow: inset 0 1px 0 rgba(255,255,255,.11), inset 0 -3px 0 rgba(0,0,0,.55), 0 0 0 1px color-mix(in srgb, var(--accent) 60%, transparent); }
.inherited { opacity: .45; }
.key.inherited { box-shadow: inset 0 1px 0 rgba(255,255,255,.04); }
`
