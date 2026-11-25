package keysheet

import (
	"regexp"
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
	cfg, err := config.Parse(`[lighting.states]
blocked = { color = "#F59E0B" }
blocked_permission = { color = "#F97316" }
done = { color = "22C55E" }
idle = { color = "#FFFFFF", brightness = 0 }
empty = { effect = "off" }

[[layer]]
name = "agents"
[layer.keys]
ACT06 = { action = "pane.zoom", label = "Zoom pane" }
ACT07 = { shortcut = "cmd+p" }
STICK_UP = { shortcut = " F13 ", label = "Pane up" }

[[layer]]
name = "browser"
color = "06B6D4"
match = { app = "Brave Browser" }
[layer.keys]
ACT06 = { shortcut = "cmd+shift+z", label = "Focus <mode>" }
ENC_CLK = { press = { shortcut = "cmd+shift+a", label = "Tab search" }, hold = { builtin = "keys.show" } }
AG03 = { builtin = "agent.show_zoom" }
AG05 = { builtin = "agent.show" }
`)
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(cfg, nil, nil, nil)
	ops := rex.Pane{Block: "b1", Session: "s1", SessionLabel: "ops", App: "claude-code", State: "blocked", Kind: "permission", Msg: "Allow Bash: go test?"}
	far := rex.Pane{Block: "b2", Session: "s2", SessionLabel: "api", Host: "BV", App: "codex", State: "done"}
	shell := rex.Pane{Block: "b3", Session: "s3", SessionLabel: "dotfiles"} // sessions mode: no agent, still opens
	quiet := rex.Pane{Block: "b4", Session: "s4", SessionLabel: "notes", App: "codex", State: "idle"}
	slots := [agents.Keys]agents.Slot{{Pane: &ops, State: "blocked"}, {Pane: &far, State: "done"}, {Pane: &shell, State: "empty"}, {Pane: &quiet, State: "idle"}}
	e.SetPanes([]rex.Pane{ops, far, shell, quiet}, slots)
	e.SetContext("Brave Browser", "")

	s := Build(e, cfg)
	if s.Layer != "browser" || s.Index != 2 || s.Count != 2 || s.When != "Brave Browser in front" || s.Color != "#06B6D4" ||
		len(s.Layers) != 2 || s.Layers[0].Color != "#8B5CF6" || s.Layers[1].Color != "#06B6D4" {
		t.Fatalf("header = %+v", s)
	}
	check := func(name string, want Key) {
		t.Helper()
		got := s.Keys[name]
		if got.Main != want.Main || got.Hint != want.Hint || got.Own != want.Own || strings.Join(got.More, "|") != strings.Join(want.More, "|") ||
			got.Slot != want.Slot || got.State != want.State || got.Light != want.Light || got.Host != want.Host || got.Sub != want.Sub {
			t.Errorf("%s = %+v, want %+v", name, got, want)
		}
	}
	check("ACT06", Key{Main: "Focus <mode>", Hint: "⌘⇧Z", Own: true})
	check("ACT07", Key{Main: "⌘P"}) // from the base layer
	check("ENC_CLK", Key{Main: "Tab search", Hint: "⌘⇧A", More: []string{"hold: show these keys"}, Own: true})
	// F13-F24 only reach a keymap; the label says what the key does.
	check("STICK_UP", Key{Main: "Pane up"})
	// Agent Keys look as their lights do: a blocked agent by what it waits for.
	check("AG00", Key{Main: "ops", Slot: 1, State: "blocked", Light: "#F97316", Sub: "Allow Bash: go test?"})
	check("AG01", Key{Main: "api", Slot: 2, State: "done", Light: "#22C55E", Host: "BV", Sub: "codex"})
	check("AG02", Key{Main: "dotfiles", Slot: 3, State: "empty"}) // its light is off
	// A light at brightness 0 is off; a layer's own binding says what it does.
	check("AG03", Key{Main: "notes", Slot: 4, State: "idle", Sub: "codex", Own: true, More: []string{"press: " + engine.Brief(&config.Action{Builtin: "agent.show_zoom"})}})
	check("AG04", Key{Slot: 5, State: "empty"})
	// A layer that keeps only the press drops the zoom: say so, too.
	check("AG05", Key{Slot: 6, State: "empty", Own: true, More: []string{"press: " + engine.Brief(&config.Action{Builtin: "agent.show"})}})

	// [agents] can make every Agent Key do something else: say what.
	odd, err := config.Parse("[agents]\npress = \"none\"\ndouble = \"show\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if k := Build(engine.New(odd, nil, nil, nil), odd).Keys["AG00"]; strings.Join(k.More, "|") != "double: "+engine.Brief(&config.Action{Builtin: "agent.show"}) {
		t.Errorf("[agents] double = show: AG00 says %q", k.More)
	}

	page := HTML(s)
	if !strings.Contains(page, "Focus &lt;mode&gt;") || strings.Contains(page, "Focus <mode>") {
		t.Error("a label is not escaped")
	}
	if !strings.Contains(page, "--accent:#06B6D4") {
		t.Error("the layer color is missing")
	}
	if !strings.Contains(page, `>Permission<`) || !strings.Contains(page, `class="host">BV<`) {
		t.Error("an agent's state or host is missing")
	}
	// On a layer above the base, the bindings it leaves to the base recede;
	// Agent Keys, which show agents on every layer, do not.
	if !regexp.MustCompile(`class="key action inherited"><div class="top"><span class="cap">APPROVE`).MatchString(page) ||
		strings.Contains(page, `class="key agent state-blocked inherited"`) || strings.Contains(page, `own inherited`) {
		t.Error("fallen-through keys are not told apart")
	}
	if base := HTML(Build(engine.New(cfg, nil, nil, nil), cfg)); strings.Contains(base, ` inherited"`) {
		t.Error("the base layer marks keys as fallen through")
	}
}
