package editor

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// slot is one bindable control, in the order the arrow keys walk them.
type slot struct {
	key, label string
	row        int // diagram row, for up/down
}

var slots = []slot{
	{"ENC_CC", "⟲ dial left", 0}, {"ENC_CLK", "● dial press", 0}, {"ENC_CW", "⟳ dial right", 0},
	{"AG00", "Agent 1", 0}, {"AG01", "Agent 2", 0},
	{"STICK_UP", "▲ stick up", 0}, {"STICK_RIGHT", "▶ stick right", 0}, {"STICK_DOWN", "▼ stick down", 0}, {"STICK_LEFT", "◀ stick left", 0},
	{"AG02", "Agent 3", 1}, {"AG03", "Agent 4", 1}, {"AG04", "Agent 5", 1}, {"AG05", "Agent 6", 1},
	{"ACT06", "FAST", 2}, {"ACT07", "APPROVE", 2}, {"ACT08", "REJECT", 2}, {"ACT09", "SPLIT", 2},
	{"MIC", "MIC", 3}, {"ACT12", "CODEX", 3},
}

func slotIndex(key string) int {
	for i, s := range slots {
		if s.key == key {
			return i
		}
	}
	return 0
}

// moveRow moves to the same position in the next or previous diagram row.
func moveRow(sel, dir int) int {
	row := slots[sel].row
	col := 0
	for i := sel - 1; i >= 0 && slots[i].row == row; i-- {
		col++
	}
	target := (row + dir + 4) % 4
	var in []int
	for i, s := range slots {
		if s.row == target {
			in = append(in, i)
		}
	}
	// Rows hold different numbers of controls; keep the relative position.
	n := 0
	for _, s := range slots {
		if s.row == row {
			n++
		}
	}
	return in[min(col*len(in)/max(n, 1), len(in)-1)]
}

var (
	dim    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"})
	bold   = lipgloss.NewStyle().Bold(true)
	errSt  = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
	okSt   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"})
	invert = lipgloss.NewStyle().Reverse(true)
)

const cell = 16 // inner width of a key box

func (m *Model) accent() lipgloss.Color {
	if c := m.cfg.Layers[m.layer].Color; c != "" {
		return lipgloss.Color(c)
	}
	return lipgloss.Color("#8B5CF6")
}

// short summarizes what a key's press (or first gesture) does.
func (m *Model) short(key string) (string, string) {
	b, from := m.binding(key)
	if b == nil {
		return "—", from
	}
	for _, g := range gestures {
		if a := b.Gesture(g); a != nil {
			s := describe(a)
			if g != "press" {
				s = g + ": " + s
			}
			return s, from
		}
	}
	return "—", from
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// box draws one key: its cap, what it does, and where that comes from.
func (m *Model) box(key, label string, width int) string {
	sum, from := m.short(key)
	origin := map[string]string{"layer": "", "base": "from base", "agent": "default", "": ""}[from]
	lines := []string{bold.Render(trunc(label, width)), trunc(sum, width), dim.Render(origin)}
	st := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Width(width).Height(3).Padding(0, 0)
	if from == "layer" {
		st = st.BorderForeground(m.accent())
	} else {
		st = st.BorderForeground(lipgloss.AdaptiveColor{Light: "#D1D5DB", Dark: "#4B5563"})
	}
	if slots[m.sel].key == key {
		st = st.Border(lipgloss.ThickBorder()).BorderForeground(m.accent())
		lines[0] = invert.Render(" " + trunc(label, width-2) + " ")
	}
	return st.Render(strings.Join(lines, "\n"))
}

// group draws the dial or the stick: one line per direction.
func (m *Model) group(title string, keys []string, width int) string {
	lines := []string{bold.Render(title)}
	selected := false
	for _, k := range keys {
		s := slots[slotIndex(k)]
		sum, from := m.short(k)
		text := trunc(strings.SplitN(s.label, " ", 2)[0]+" "+sum, width)
		switch {
		case slots[m.sel].key == k:
			text = invert.Render(text)
			selected = true
		case from == "layer":
			text = lipgloss.NewStyle().Foreground(m.accent()).Render(text)
		}
		lines = append(lines, text)
	}
	st := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Width(width).Height(len(keys) + 1).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#D1D5DB", Dark: "#4B5563"})
	if selected {
		st = st.BorderForeground(m.accent())
	}
	return st.Render(strings.Join(lines, "\n"))
}

func (m *Model) pad() string {
	dial := m.group("DIAL", []string{"ENC_CC", "ENC_CLK", "ENC_CW"}, cell)
	stick := m.group("STICK", []string{"STICK_UP", "STICK_RIGHT", "STICK_DOWN", "STICK_LEFT"}, cell)
	top := lipgloss.JoinHorizontal(lipgloss.Top, dial, m.box("AG00", "Agent 1", cell), m.box("AG01", "Agent 2", cell), stick)
	row := func(keys ...string) string {
		var bs []string
		for _, k := range keys {
			bs = append(bs, m.box(k, slots[slotIndex(k)].label, cell))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, bs...)
	}
	bottom := lipgloss.JoinHorizontal(lipgloss.Top, m.box("MIC", "MIC (wide)", cell*3+4), m.box("ACT12", "CODEX", cell))
	return lipgloss.JoinVertical(lipgloss.Left, top, row("AG02", "AG03", "AG04", "AG05"), row("ACT06", "ACT07", "ACT08", "ACT09"), bottom)
}

func (m *Model) tabs() string {
	var out []string
	for i, l := range m.cfg.Layers {
		name := fmt.Sprintf(" %d %s ", i+1, l.Name)
		if d := l.Match.Describe(); d != "" {
			name = fmt.Sprintf(" %d %s · %s ", i+1, l.Name, d)
		}
		c := lipgloss.Color("#8B5CF6")
		if l.Color != "" {
			c = lipgloss.Color(l.Color)
		}
		if i == m.layer {
			out = append(out, lipgloss.NewStyle().Background(c).Foreground(lipgloss.Color("#000000")).Bold(true).Render(name))
		} else {
			out = append(out, lipgloss.NewStyle().Foreground(c).Render(name))
		}
	}
	return strings.Join(out, " ")
}

func (m *Model) details() string {
	key := m.key()
	s := slots[m.sel]
	b, from := m.binding(key)
	var lines []string
	lines = append(lines, bold.Render(fmt.Sprintf("%s · %s", s.label, key)))
	switch from {
	case "base":
		lines = append(lines, dim.Render("not bound on this layer; the base layer's binding applies"))
	case "agent":
		why := "the Agent Key default ([agents] press/double)"
		if m.cfg.Agents.Always {
			why = "agents.always is on, so layers cannot rebind it"
		}
		lines = append(lines, dim.Render(why))
	case "":
		lines = append(lines, dim.Render("unbound"))
	default:
		lines = append(lines, dim.Render("bound on this layer"))
	}
	lines = append(lines, "")
	for i, g := range gesturesOf(key) {
		val := "—"
		if b != nil {
			if a := b.Gesture(g); a != nil {
				val = describe(a)
			}
		}
		line := fmt.Sprintf("%-7s %s", g, trunc(val, 44))
		if m.focus == focusGestures && i == m.ges {
			line = invert.Render(line)
		}
		lines = append(lines, line)
	}
	if _, ag := agentKey(key); ag && from == "layer" {
		lines = append(lines, "", dim.Render("This layer rebinds the Agent Key; it lights in the layer color."))
	}
	if key == "MIC" || key == "ACT07" {
		lines = append(lines, "", dim.Render("down/up suit push-to-talk; hold and double wait to tell gestures apart."))
	}
	return lipgloss.NewStyle().Padding(0, 2).Width(56).Render(strings.Join(lines, "\n"))
}

func (m *Model) help() string {
	var h string
	switch {
	case m.mode == modeInput:
		h = "enter apply · esc cancel"
	case m.focus == focusGestures:
		h = "↑↓ gesture · enter choose action · x clear · r reset key · esc back"
	default:
		h = "press a control on the pad or use arrows · enter edit · tab layer · l layer menu · A agents.always · s save · q quit"
	}
	if m.opts.Pad == nil && m.opts.PadNote != "" {
		h += "\n" + m.opts.PadNote
	}
	return dim.Render(h)
}

// View draws the screen.
func (m *Model) View() string {
	if m.mode == modeList {
		return m.list.View()
	}
	head := bold.Render("rex-micro edit") + "  " + dim.Render(m.opts.Path)
	if m.dirty() {
		head += "  " + errSt.Render("● unsaved")
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.pad(), m.details())
	if m.width < 130 {
		body = lipgloss.JoinVertical(lipgloss.Left, m.pad(), m.details())
	}
	status := ""
	if m.status != "" {
		if m.bad {
			status = errSt.Render(m.status)
		} else {
			status = okSt.Render(m.status)
		}
	}
	var foot string
	switch m.mode {
	case modeInput:
		foot = m.input.View()
	case modeConfirm:
		foot = bold.Render(m.ask)
	}
	return strings.Join([]string{head, m.tabs(), body, status, foot, m.help()}, "\n")
}
