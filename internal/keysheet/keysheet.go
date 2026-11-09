// Package keysheet draws the active layer's keys as a page shaped like the
// pad, for the keys overlay (builtin keys.show).
package keysheet

import (
	"fmt"
	"html"
	"strings"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
)

// Size is the overlay's size in points; the page is laid out to fill it.
const Width, Height = 760, 520

// Key is one control as the sheet shows it.
type Key struct {
	Cap  string   // as printed on the pad, e.g. FAST
	Main string   // what a press does
	Hint string   // the shortcut behind a labelled press, e.g. ⌘⇧Z
	More []string // other gestures, e.g. "hold: show these keys"
	Own  bool     // bound by the active layer itself, not fallen through
}

// Sheet is the active layer and its keys.
type Sheet struct {
	Layer, Color, When string
	Index, Count       int
	Keys               map[string]Key
}

// Build reads the active layer's keys from the engine.
func Build(e *engine.Engine, cfg *config.Config) Sheet {
	l := e.LayerConfig()
	s := Sheet{Layer: l.Name, Color: l.Color, When: l.Match.Describe(), Index: e.Layer() + 1, Count: len(cfg.Layers), Keys: map[string]Key{}}
	if s.Color == "" {
		s.Color = "#8B5CF6"
	}
	for _, name := range config.KeyNames {
		b, from := e.Binding(name)
		// from is the active layer or the base (0); on the base nothing is
		// marked, since nothing falls through to it.
		k := Key{Cap: config.Caps[name], Main: "—", Own: from > 0}
		if agent, ok := agentKey(name); ok && showsAgent(b) {
			// An Agent Key that shows its agent: name the agent instead.
			k.Main = "no agent"
			if p := e.AgentPane(agent); p != nil {
				k.Main = p.SessionLabel
				if k.Main == "" {
					k.Main = p.Session
				}
				if p.App != "" {
					k.More = []string{p.App}
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
			if a.Label != "" && a.Shortcut != "" {
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

// HTML renders the sheet as a whole page for the overlay window. It follows
// the system's light or dark appearance.
func HTML(s Sheet) string {
	var b strings.Builder
	esc := html.EscapeString
	box := func(name, class string) {
		k := s.Keys[name]
		cls := "key " + class
		if k.Own {
			cls += " own"
		}
		fmt.Fprintf(&b, `<div class="%s"><div class="cap">%s</div><div class="main">%s%s</div>`, cls, esc(k.Cap), esc(k.Main), hint(k.Hint))
		for _, m := range k.More {
			fmt.Fprintf(&b, `<div class="more">%s</div>`, esc(m))
		}
		b.WriteString(`</div>`)
	}
	group := func(title string, names ...string) {
		b.WriteString(`<div class="key group">`)
		fmt.Fprintf(&b, `<div class="cap">%s</div>`, esc(title))
		for _, n := range names {
			k := s.Keys[n]
			cls := "row"
			if k.Own {
				cls += " own"
			}
			glyph := strings.SplitN(k.Cap, " ", 2)[0]
			fmt.Fprintf(&b, `<div class="%s"><span class="g">%s</span><span class="main">%s%s</span></div>`, cls, esc(glyph), esc(k.Main), hint(k.Hint))
			for _, m := range k.More {
				fmt.Fprintf(&b, `<div class="more indent">%s</div>`, esc(m))
			}
		}
		b.WriteString(`</div>`)
	}
	when := ""
	if s.When != "" {
		when = " · on when " + s.When
	}
	fmt.Fprintf(&b, `<!doctype html><html><head><meta charset="utf-8"><style>%s</style></head><body style="--accent:%s"><div class="card">`, css, esc(s.Color))
	fmt.Fprintf(&b, `<div class="head"><span class="dot"></span><span class="name">%s</span><span class="sub">layer %d of %d%s</span></div><div class="grid">`, esc(s.Layer), s.Index, s.Count, esc(when))
	group("DIAL", "ENC_CC", "ENC_CLK", "ENC_CW")
	box("AG00", "")
	box("AG01", "")
	group("STICK", "STICK_UP", "STICK_RIGHT", "STICK_DOWN", "STICK_LEFT")
	for _, n := range []string{"AG02", "AG03", "AG04", "AG05", "ACT06", "ACT07", "ACT08", "ACT09"} {
		box(n, "")
	}
	box("MIC", "wide")
	box("ACT12", "")
	b.WriteString(`</div></div></body></html>`)
	return b.String()
}

func hint(h string) string {
	if h == "" {
		return ""
	}
	return ` <span class="hint">` + html.EscapeString(h) + `</span>`
}

const css = `
:root { --bg: #F9FAFB; --fg: #111827; --dim: #6B7280; --line: #D1D5DB; --keybg: #FFFFFF; }
@media (prefers-color-scheme: dark) {
  :root { --bg: #18181B; --fg: #F4F4F5; --dim: #A1A1AA; --line: #3F3F46; --keybg: #27272A; }
}
* { box-sizing: border-box; margin: 0; }
html, body { background: transparent; height: 100%; overflow: hidden; }
body { font: 13px -apple-system, BlinkMacSystemFont, sans-serif; color: var(--fg); padding: 10px; -webkit-user-select: none; cursor: default; }
.card { height: 100%; background: var(--bg); border-radius: 18px; padding: 16px 18px; border: 1px solid var(--line); box-shadow: 0 10px 40px rgba(0,0,0,.35); display: flex; flex-direction: column; }
.head { display: flex; align-items: baseline; gap: 10px; margin-bottom: 12px; }
.dot { width: 12px; height: 12px; border-radius: 50%; background: var(--accent); align-self: center; }
.name { font-size: 18px; font-weight: 700; }
.sub { color: var(--dim); }
.grid { flex: 1; display: grid; grid-template-columns: repeat(4, 1fr); grid-template-rows: 1.35fr 1fr 1fr 1fr; gap: 8px; min-height: 0; }
.key { background: var(--keybg); border: 1px solid var(--line); border-radius: 10px; padding: 7px 10px; overflow: hidden; }
.key.own { border-color: var(--accent); box-shadow: inset 0 0 0 1px var(--accent); }
.wide { grid-column: span 3; }
.cap { font-size: 10px; letter-spacing: .06em; text-transform: uppercase; color: var(--dim); margin-bottom: 3px; }
.main { font-weight: 600; font-size: 14px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.more { color: var(--dim); font-size: 11.5px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.group .row { display: flex; gap: 6px; align-items: baseline; }
.group .row .main { font-size: 13px; }
.group .row.own .g { color: var(--accent); }
.g { width: 14px; color: var(--dim); flex: none; text-align: center; }
.indent { padding-left: 20px; }
.hint { color: var(--dim); font-weight: 400; font-size: 12px; margin-left: 4px; }
`
