package config

import (
	"regexp"
	"strings"
	"testing"
)

func TestDefaultParses(t *testing.T) {
	cfg, err := Parse(Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Layers) != 1 || cfg.Layers[0].Name != "agents" {
		t.Fatalf("layers = %+v", cfg.Layers)
	}
	if a := cfg.Layers[0].Keys["ACT07"].Gesture("press"); a == nil || a.Key[0] != "enter" {
		t.Fatalf("ACT07 press = %+v", a)
	}
}

func TestEmptyFileKeepsDefaults(t *testing.T) {
	cfg, err := Parse("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents.Mode != "auto" || len(cfg.Layers) != 1 || cfg.Lighting.States["blocked"].Color == "" {
		t.Fatalf("defaults lost: %+v", cfg)
	}
}

func TestOverrideOneState(t *testing.T) {
	cfg, err := Parse("[lighting.states]\ndone = { color = \"#00FF00\" }\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Lighting.States["done"].Color != "#00FF00" || cfg.Lighting.States["working"].Color == "" {
		t.Fatalf("states = %+v", cfg.Lighting.States)
	}
}

// Every example layer in the default config parses once uncommented, and so
// does every commented binding line elsewhere in it (the MIC dictation and
// blocked_permission examples).
func TestUncommentedExamplesParse(t *testing.T) {
	toml := regexp.MustCompile(`^# (\[|[A-Za-z0-9_]+ *=|# )`)
	var b strings.Builder
	in := false
	for _, line := range strings.Split(Default, "\n") {
		if strings.HasPrefix(line, "# Example layers") {
			in = true
		}
		switch {
		case in && toml.MatchString(line):
			line = strings.TrimPrefix(line, "# ")
		case strings.HasPrefix(line, "#   MIC = { shortcut"), strings.HasPrefix(line, "# blocked_permission = "):
			line = strings.TrimLeft(line, "# ")
		}
		b.WriteString(line + "\n")
	}
	text := strings.Replace(b.String(), "MIC   = { builtin = \"none\" }\n", "", 1)
	cfg, err := Parse(text)
	if err != nil {
		t.Fatalf("examples: %v\n%s", err, text)
	}
	var names []string
	for _, l := range cfg.Layers {
		names = append(names, l.Name)
	}
	if got := strings.Join(names, " "); got != "agents layout browser copy" {
		t.Fatalf("layers = %s", got)
	}
	if cfg.Layers[0].Keys["MIC"].Action.Shortcut != "right_option" || cfg.Lighting.States["blocked_permission"].Color == "" {
		t.Fatal("the MIC or blocked_permission example was not uncommented")
	}
	if a := cfg.Layers[3].Keys["STICK_UP"].Action; a.Action != "pane.copy_mode.move" || a.Args["to"] != "up" {
		t.Fatalf("copy layer STICK_UP = %+v", a)
	}
}

// layerList declares layers named l<from>..l<to>.
func layerList(from, to int) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		b.WriteString("[[layer]]\nname = \"l" + string(rune('0'+i)) + "\"\nmatch = { app = \"A" + string(rune('0'+i)) + "\" }\n")
	}
	return b.String()
}

func TestRejects(t *testing.T) {
	base := "[[layer]]\nname = \"a\"\n[layer.keys]\n"
	cases := map[string]struct{ text, want string }{
		"unknown key":       {base + "ACT10 = { key = \"enter\" }\n", "layer \"a\".keys.ACT10: unknown key (the wide key is MIC)"},
		"two kinds":         {base + "ACT07 = { key = \"enter\", shell = \"ls\" }\n", "layer \"a\".keys.ACT07: binding sets key and shell"},
		"unknown action":    {base + "ACT07 = { actoin = \"pane.zoom\" }\n", "unknown setting layer.keys.ACT07.actoin"},
		"duplicate binding": {base + "ACT07 = { key = \"enter\" }\nACT07 = { key = \"escape\" }\n", "line 5"},
		"bad builtin":       {base + "ENC_CLK = { builtin = \"layer.nope\" }\n", "no layer named \"nope\""},
		"bad mode":          {"[agents]\nmode = \"every\"\n", "agents.mode"},
		"bad color":         {"[lighting.states]\ndone = { color = \"green\" }\n", "lighting.states.done"},
		"target on action":  {base + "ACT06 = { action = \"pane.zoom\", target = \"agent\" }\n", "target only applies"},
		"empty":             {base + "ACT06 = { }\n", "binding does nothing"},
		"angle offset":      {"[stick]\nangle_offset = -1.5\n", "stick.angle_offset"},
		"nan brightness":    {"[lighting]\nbrightness = nan\n", "lighting.brightness"},
		"text target":       {base + "ACT06 = { text = \"x\", target = \"focused\" }\n", "text always goes"},
		"stick gesture":     {base + "STICK_UP = { down = { shell = \"a\" }, up = { shell = \"b\" } }\n", "only has press"},
		"held with hold":    {base + "ACT06 = { press = { shortcut = \"fn\", held = true }, hold = { key = \"a\" } }\n", "held shortcut is the whole binding"},
		"held on down":      {base + "MIC = { down = { shortcut = \"fn\", held = true } }\n", "held is the whole binding"},
		"held stick":        {base + "STICK_UP = { shortcut = \"fn\", held = true }\n", "never held down"},
		"held key":          {base + "ACT06 = { key = \"a\", held = true }\n", "held only applies to shortcut"},
		"dial gesture":      {base + "ENC_CW = { hold = { shell = \"a\" } }\n", "only has press"},
		"pad layers":        {"[pad]\nlayers = \"on\"\n", "pad.layers: \"on\" is not sync or off"},
		"seven pad layers":  {"[pad]\nlayers = \"sync\"\n" + strings.Repeat("[[layer]]\nname = \"x\"\n", 1) + layerList(2, 7), "the pad holds 6 layers and 7 are defined"},
	}
	for name, tc := range cases {
		_, err := Parse(tc.text)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want containing %q", name, err, tc.want)
		}
	}
}

func TestMatchAndShortcutValidation(t *testing.T) {
	ok := "[[layer]]\nname = \"base\"\n[[layer]]\nname = \"web\"\nmatch = { app = \"Google Chrome\" }\n[layer.keys]\nENC_CW = { shortcut = \"cmd+shift+]\" }\n"
	if _, err := Parse(ok); err != nil {
		t.Fatal(err)
	}
	for bad, msg := range map[string]string{
		"[[layer]]\nname = \"base\"\nmatch = { app = \"Rex\" }\n":                                             "first layer is the base",
		"[[layer]]\nname = \"a\"\n[[layer]]\nname = \"b\"\nmatch = {}\n":                                      "give app",
		"[[layer]]\nname = \"a\"\n[[layer]]\nname = \"b\"\nmatch = { app = \"Safari\", program = \"vim\" }\n": "only applies inside Rex",
		"[[layer]]\nname = \"a\"\n[layer.keys]\nACT06 = { shortcut = \"cmd+nope\" }\n":                        "unknown key",
		"[[layer]]\nname = \"a\"\n[layer.keys]\nACT06 = { shortcut = \"cmd+t\", key = \"x\" }\n":              "exactly one",
	} {
		if _, err := Parse(bad); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: err = %v, want %q", bad, err, msg)
		}
	}
}

func TestFormatRoundTrips(t *testing.T) {
	bindings := map[string]*KeyBinding{
		"ACT06": {Action: Action{Action: "pane.split", Args: map[string]string{"direction": "right", "odd key": "a\"b"}}},
		"ACT07": {Action: Action{Key: StringList{"ctrl+c", "enter"}, Target: "agent"}},
		"ACT08": {Press: &Action{Shortcut: "cmd+shift+]"}, Hold: &Action{Shell: "say \\hi\n", Detach: true}},
		"MIC":   {Down: &Action{Rex: []string{"run", "--", "lazygit"}}, Up: &Action{Text: "tab\there"}},
		"ACT12": {Action: Action{Builtin: "layer.next"}},
	}
	var b strings.Builder
	b.WriteString("[[layer]]\nname = \"a\"\nmatch = " + (&Match{}).TOML() + "\n")
	b.Reset()
	b.WriteString("[[layer]]\nname = \"a\"\n[[layer]]\nname = \"b\"\nmatch = " + (&Match{App: "Rex", Program: "lazygit"}).TOML() + "\n[layer.keys]\n")
	for k, v := range bindings {
		b.WriteString(k + " = " + v.TOML() + "\n")
	}
	cfg, err := Parse(b.String())
	if err != nil {
		t.Fatalf("%v\n%s", err, b.String())
	}
	for k, v := range bindings {
		if got := cfg.Layers[1].Keys[k].TOML(); got != v.TOML() {
			t.Errorf("%s: %s -> %s", k, v.TOML(), got)
		}
	}
	if m := cfg.Layers[1].Match; m.App != "Rex" || m.Program != "lazygit" {
		t.Errorf("match = %+v", m)
	}
	if got := bindings["ACT12"].TOML(); got != `{ builtin = "layer.next" }` {
		t.Errorf("single press form = %s", got)
	}
}
