package keysheet

import (
	"strings"
	"testing"

	"github.com/JiehoonKwak/rex-micro/internal/agents"
	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

// The sheet shows the active layer as the keys behave: its own bindings
// marked, the rest fallen through from the base, labels with their shortcut,
// and Agent Keys by the agent they show.
func TestSheetShowsTheActiveLayer(t *testing.T) {
	cfg, err := config.Parse(`[[layer]]
name = "agents"
[layer.keys]
ACT06 = { action = "pane.zoom", label = "Zoom pane" }
ACT07 = { shortcut = "cmd+p" }

[[layer]]
name = "browser"
color = "#06B6D4"
match = { app = "Brave Browser" }
[layer.keys]
ACT06 = { shortcut = "cmd+shift+z", label = "Focus <mode>" }
ENC_CLK = { press = { shortcut = "cmd+shift+a", label = "Tab search" }, hold = { builtin = "keys.show" } }
`)
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(cfg, nil, nil, nil)
	pane := rex.Pane{Block: "b1", Session: "s1", SessionLabel: "ops", App: "claude-code"}
	var slots [agents.Keys]agents.Slot
	slots[0].Pane = &pane
	e.SetPanes([]rex.Pane{pane}, slots)
	e.SetContext("Brave Browser", "")

	s := Build(e, cfg)
	if s.Layer != "browser" || s.Index != 2 || s.Count != 2 || s.When != "Brave Browser in front" {
		t.Fatalf("header = %+v", s)
	}
	check := func(name string, want Key) {
		t.Helper()
		got := s.Keys[name]
		if got.Main != want.Main || got.Hint != want.Hint || got.Own != want.Own || strings.Join(got.More, "|") != strings.Join(want.More, "|") {
			t.Errorf("%s = %+v, want %+v", name, got, want)
		}
	}
	check("ACT06", Key{Main: "Focus <mode>", Hint: "⌘⇧Z", Own: true})
	check("ACT07", Key{Main: "⌘P"}) // from the base layer
	check("ENC_CLK", Key{Main: "Tab search", Hint: "⌘⇧A", More: []string{"hold: show these keys"}, Own: true})
	check("AG00", Key{Main: "ops", More: []string{"claude-code"}})
	check("AG01", Key{Main: "no agent"})

	page := HTML(s)
	if !strings.Contains(page, "Focus &lt;mode&gt;") || strings.Contains(page, "Focus <mode>") {
		t.Error("a label is not escaped")
	}
	if !strings.Contains(page, "--accent:#06B6D4") {
		t.Error("the layer color is missing")
	}
}
