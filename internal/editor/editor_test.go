package editor

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/tomledit"
)

func newModel(t *testing.T, text string) (*Model, *string) {
	t.Helper()
	var saved string
	m, err := New(Options{Path: "config.toml", Text: text, Save: func(s string, _ bool) error { saved = s; return nil },
		Apps:    func() []string { return []string{"Finder", "Google Chrome", "Rex Beta"} },
		Actions: []RexAction{{Name: "client.shortcuts.edit", Title: "Change Shortcut…", Category: "App"}, {Name: "pane.zoom", Title: "Zoom Pane", Category: "Pane"}}})
	if err != nil {
		t.Fatal(err)
	}
	return m, &saved
}

func send(m *Model, keys ...string) {
	for _, k := range keys {
		var msg tea.Msg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		default:
			if strings.HasPrefix(k, "pad:") {
				msg = padMsg(strings.TrimPrefix(k, "pad:"))
			} else {
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
			}
		}
		run(m, msg)
	}
}

// run delivers msg and then the messages its commands produce, the way the
// bubbletea runtime would; commands that wait (cursor blinks) are dropped.
func run(m *Model, msg tea.Msg) {
	_, cmd := m.Update(msg)
	for _, next := range results(cmd) {
		run(m, next)
	}
}

func results(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		if batch, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range batch {
				out = append(out, results(c)...)
			}
			return out
		}
		if msg == nil {
			return nil
		}
		return []tea.Msg{msg}
	case <-time.After(20 * time.Millisecond):
		return nil
	}
}

// search filters the open list and picks the first match.
func search(m *Model, q string) {
	send(m, "/")
	for _, r := range q {
		send(m, string(r))
	}
	send(m, "enter", "enter")
}

func changed(a, b string) []string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out []string
	for i := range bl {
		if i >= len(al) || al[i] != bl[i] {
			out = append(out, bl[i])
		}
	}
	if len(al) != len(bl) {
		out = append(out, "(line count changed)")
	}
	return out
}

func TestAssignFromPadAndSave(t *testing.T) {
	m, saved := newModel(t, config.Default)
	send(m, "pad:ENC_CW", "enter") // select the dial's clockwise turn, edit its gestures
	send(m, "enter")               // press -> action list
	if m.mode != modeList {
		t.Fatalf("mode = %v, status %q", m.mode, m.status)
	}
	search(m, "Shortcut")
	if m.mode != modeInput {
		t.Fatalf("mode = %v after picking, status %q", m.mode, m.status)
	}
	send(m, "cmd+shift+]", "enter", "s")
	got := changed(config.Default, *saved)
	want := `ENC_CW  = { shortcut = "cmd+shift+]" }`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("changed lines = %q (want only %q); status %q", got, want, m.status)
	}
	if m.dirty() {
		t.Fatal("still dirty after save")
	}
}

func TestLayerWithMatchAndAgentKey(t *testing.T) {
	m, saved := newModel(t, config.Default)
	send(m, "l")
	search(m, "Add a")
	send(m, "browser", "enter")
	if len(m.cfg.Layers) != 2 || m.layer != 1 {
		t.Fatalf("layers = %d, layer = %d, status %q", len(m.cfg.Layers), m.layer, m.status)
	}
	send(m, "l")
	search(m, "Switch auto")
	search(m, "Google Chrome")
	// Rebind Agent 1 on this layer only.
	send(m, "pad:AG00", "enter", "enter")
	search(m, "Rex: Zoom")
	send(m, "s")
	cfg, err := config.Parse(*saved)
	if err != nil {
		t.Fatalf("%v\n%s", err, *saved)
	}
	l := cfg.Layers[1]
	if l.Match == nil || l.Match.App != "Google Chrome" || l.Keys["AG00"].Gesture("press").Action != "pane.zoom" {
		t.Fatalf("layer = %+v\n%s", l, *saved)
	}
	// The Agent Key keeps its double-press from the default (show zoomed).
	if d := l.Keys["AG00"].Double; d == nil || d.Builtin != "agent.show_zoom" {
		t.Fatalf("double lost: %+v", l.Keys["AG00"])
	}
	if cfg.Layers[0].Keys["AG00"] != nil {
		t.Fatal("base layer changed")
	}
	if !strings.HasPrefix(*saved, config.Default[:strings.Index(config.Default, "# Example second layer")]) {
		t.Fatal("the existing text before the new layer changed")
	}
}

func TestInvalidEditIsRefused(t *testing.T) {
	m, _ := newModel(t, config.Default)
	send(m, "l")
	search(m, "Rename")
	send(m, "esc") // nothing typed yet: cancel
	before := m.Text()
	send(m, "pad:ACT06", "enter", "enter")
	search(m, "Shortcut")
	send(m, "cmd+nokey", "enter")
	if m.Text() != before || !m.bad {
		t.Fatalf("bad shortcut applied; status %q", m.status)
	}
}

func TestEditThatTouchesMoreIsRefused(t *testing.T) {
	m, _ := newModel(t, config.Default)
	before := m.Text()
	ok := m.edit("ACT06", only("layer agents keys.ACT06"), func(d *tomledit.Doc) error {
		_ = d.SetKey(0, "ACT06", `{ key = "a" }`)
		return d.SetKey(0, "ACT07", "") // a line edit that went wrong
	})
	if ok || m.Text() != before || !strings.Contains(m.status, "keys.ACT07") {
		t.Fatalf("ok=%v status=%q", ok, m.status)
	}
}

func TestGestureTableAndRenameKeepUnrelatedText(t *testing.T) {
	text := "[[layer]]\nname = \"base\"\n\n[layer.keys]\nACT07 = { text = \"layer.base\" }\nACT08 = { builtin = \"layer.base\" }\n\n[layer.keys.ACT06]\n# hold closes the pane\nhold = { action = \"pane.close\" }\npress = { action = \"pane.zoom\" }\n"
	m, saved := newModel(t, text)
	send(m, "pad:ACT06", "enter", "enter") // press
	search(m, "Next layer")
	send(m, "esc", "l")
	search(m, "Rename")
	for i := 0; i < 4; i++ {
		send(m, "backspace")
	}
	send(m, "main", "enter", "s")
	got := changed(text, *saved)
	want := []string{`name = "main"`, `ACT08 = { builtin = "layer.main" }`, `press = { builtin = "layer.next" }`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("changed = %q; status %q", got, m.status)
	}
}

func TestPadPickAfterLaterGestureDoesNotCrash(t *testing.T) {
	m, _ := newModel(t, "[[layer]]\nname = \"a\"\n")
	send(m, "pad:ACT06", "enter", "down") // ACT06's double gesture
	send(m, "pad:STICK_UP", "enter")      // the stick only has press
	_ = m.View()
}
