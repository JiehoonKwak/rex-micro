package config

import (
	"fmt"
	"sort"
	"strings"
)

// Quote writes s as a TOML basic string.
func Quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func bareKey(k string) string {
	for _, r := range k {
		if !(r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return Quote(k)
		}
	}
	if k == "" {
		return `""`
	}
	return k
}

func list(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = Quote(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func inline(fields []string) string {
	if len(fields) == 0 {
		return "{}"
	}
	return "{ " + strings.Join(fields, ", ") + " }"
}

// TOML writes the action as an inline table.
func (a *Action) TOML() string {
	var f []string
	if a.Action != "" {
		f = append(f, "action = "+Quote(a.Action))
	}
	if len(a.Args) > 0 {
		keys := make([]string, 0, len(a.Args))
		for k := range a.Args {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var args []string
		for _, k := range keys {
			args = append(args, bareKey(k)+" = "+Quote(a.Args[k]))
		}
		f = append(f, "args = "+inline(args))
	}
	switch len(a.Key) {
	case 0:
	case 1:
		f = append(f, "key = "+Quote(a.Key[0]))
	default:
		f = append(f, "key = "+list(a.Key))
	}
	if a.Target != "" {
		f = append(f, "target = "+Quote(a.Target))
	}
	if a.Shortcut != "" {
		f = append(f, "shortcut = "+Quote(a.Shortcut))
	}
	if a.Text != "" {
		f = append(f, "text = "+Quote(a.Text))
	}
	if len(a.Rex) > 0 {
		f = append(f, "rex = "+list(a.Rex))
	}
	if a.Shell != "" {
		f = append(f, "shell = "+Quote(a.Shell))
	}
	if a.Detach {
		f = append(f, "detach = true")
	}
	if a.Builtin != "" {
		f = append(f, "builtin = "+Quote(a.Builtin))
	}
	return inline(f)
}

// TOML writes the binding as an inline table: the action itself when only
// press is set, else one table per gesture.
func (k *KeyBinding) TOML() string {
	var f []string
	only := true
	for _, g := range gestureNames {
		a := k.Gesture(g)
		if a == nil {
			continue
		}
		if g != "press" {
			only = false
		}
		f = append(f, g+" = "+a.TOML())
	}
	if only && len(f) == 1 {
		return k.Gesture("press").TOML()
	}
	return inline(f)
}

// TOML writes the match as an inline table.
func (m *Match) TOML() string {
	var f []string
	if m.App != "" {
		f = append(f, "app = "+Quote(m.App))
	}
	if m.Program != "" {
		f = append(f, "program = "+Quote(m.Program))
	}
	return inline(f)
}
