package engine

import (
	"fmt"
	"strings"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/macos"
)

// Brief names an action for people, for the editor and the keys overlay: its
// label when it has one, else a short form of what it does.
func Brief(a *config.Action) string {
	if a == nil {
		return "-"
	}
	if a.Label != "" {
		return a.Label
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
		case b == "keys.show":
			return "show these keys"
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
	case "shortcut":
		s := Glyphs(a.Shortcut)
		if a.Held {
			s += " (held)"
		}
		return s
	}
	return Summary(a)
}

var glyphs = map[string]string{
	"cmd": "⌘", "command": "⌘", "shift": "⇧", "alt": "⌥", "opt": "⌥", "option": "⌥",
	"ctrl": "⌃", "control": "⌃", "fn": "fn ",
	"enter": "↩", "return": "↩", "tab": "⇥", "space": "Space", "escape": "⎋", "esc": "⎋",
	"backspace": "⌫", "delete": "⌫", "forward_delete": "⌦",
	"up": "↑", "down": "↓", "left": "←", "right": "→",
	"page_up": "PgUp", "page_down": "PgDn", "home": "Home", "end": "End",
	"right_cmd": "right ⌘", "right_command": "right ⌘", "right_shift": "right ⇧",
	"right_alt": "right ⌥", "right_opt": "right ⌥", "right_option": "right ⌥",
	"right_ctrl": "right ⌃", "right_control": "right ⌃",
}

// Glyphs writes a shortcut the way macOS menus do: "cmd+shift+z" is ⌘⇧Z. It
// reads the shortcut as it is posted (macos.Fields), so "cmd++" is ⌘⇧=.
func Glyphs(combo string) string {
	var b strings.Builder
	for _, p := range macos.Fields(combo) {
		if g, ok := glyphs[p]; ok {
			b.WriteString(g)
		} else {
			b.WriteString(strings.ToUpper(p))
		}
	}
	return b.String()
}

// Describe explains what a key does in the active layer, for `rex-micro keys`.
func (e *Engine) Describe(name string) string {
	b := e.binding(name)
	if b == nil {
		return "-"
	}
	var parts []string
	for _, g := range []string{"press", "double", "hold", "down", "up"} {
		if a := b.Gesture(g); a != nil {
			s := Summary(a)
			if a.Label != "" {
				s = a.Label + " (" + s + ")"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", g, s))
		}
	}
	return strings.Join(parts, "; ")
}

// Summary renders one action as text.
func Summary(a *config.Action) string {
	switch a.Kind() {
	case "builtin":
		return a.Builtin
	case "action":
		if len(a.Args) == 0 {
			return "rex " + a.Action
		}
		return fmt.Sprintf("rex %s %v", a.Action, a.Args)
	case "key":
		t := ""
		if a.Target == "agent" {
			t = " → agent"
		}
		return "key " + strings.Join(a.Key, " ") + t
	case "text":
		return fmt.Sprintf("text %q", a.Text)
	case "rex":
		return "rex " + strings.Join(a.Rex, " ")
	case "shell":
		return "shell " + a.Shell
	case "shortcut":
		if a.Held {
			return "shortcut " + a.Shortcut + " (held)"
		}
		return "shortcut " + a.Shortcut
	}
	return "?"
}
