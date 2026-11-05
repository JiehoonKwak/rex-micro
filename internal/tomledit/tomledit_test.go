package tomledit

import (
	"strings"
	"testing"

	"github.com/JiehoonKwak/rex-micro/internal/config"
)

// changed lists the lines of b that differ from a, assuming equal length.
func diff(t *testing.T, a, b string) []string {
	t.Helper()
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(al) != len(bl) {
		t.Fatalf("line count %d -> %d:\n%s", len(al), len(bl), b)
	}
	var out []string
	for i := range al {
		if al[i] != bl[i] {
			out = append(out, bl[i])
		}
	}
	return out
}

func mustParse(t *testing.T, text string) *config.Config {
	t.Helper()
	cfg, err := config.Parse(text)
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	return cfg
}

func TestReplaceOneKeyLeavesEveryOtherByte(t *testing.T) {
	d := Parse(config.Default)
	if err := d.SetKey(0, "ENC_CW", `{ shortcut = "cmd+shift+]" }`); err != nil {
		t.Fatal(err)
	}
	if err := d.SetKey(0, "ENC_CW", `{ shortcut = "cmd+shift+]" }`); err != nil {
		t.Fatal(err)
	}
	if err := d.SetKey(0, "ACT06", `{ action = "pane.split.right" }`); err != nil {
		t.Fatal(err)
	}
	got := diff(t, config.Default, d.String())
	if len(got) != 2 || got[1] != `ENC_CW  = { shortcut = "cmd+shift+]" }` {
		t.Fatalf("changed lines = %q", got)
	}
	got = got[:1]
	want := `ACT06 = { action = "pane.split.right" }   # FAST cap: zoom the focused pane`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("changed lines = %q, want [%q]", got, want)
	}
	cfg := mustParse(t, d.String())
	if cfg.Layers[0].Keys["ACT06"].Action.Action != "pane.split.right" {
		t.Fatal("binding not applied")
	}
}

func TestAddRemoveKeyAndFields(t *testing.T) {
	d := Parse(config.Default)
	if err := d.SetKey(0, "AG00", `{ shortcut = "cmd+t" }`); err != nil {
		t.Fatal(err)
	}
	if err := d.SetKey(0, "MIC", ""); err != nil {
		t.Fatal(err)
	}
	if err := d.SetField(0, "color", `"#22C55E"`); err != nil {
		t.Fatal(err)
	}
	cfg := mustParse(t, d.String())
	l := cfg.Layers[0]
	if l.Keys["AG00"].Action.Shortcut != "cmd+t" || l.Keys["MIC"] != nil || l.Color != "#22C55E" {
		t.Fatalf("layer = %+v", l)
	}
	// The added binding joins the keys table, before the commented example.
	if !strings.Contains(d.String(), "STICK_RIGHT = { action = \"pane.focus.right\" }\nAG00 = { shortcut = \"cmd+t\" }\n\n# Example second layer") {
		t.Fatalf("placement:\n%s", d.String())
	}
}

func TestLayerTablesAndDottedForms(t *testing.T) {
	text := `[[layer]]
name = "base"   # the base

[layer.keys]
ACT06 = { key = "a" }

[layer.keys.ACT07]
press = { key = "b" }
hold = { key = "c" }

[[layer]]
name = "web"
keys.ACT06 = { key = "x" }
[layer.match]
app = "Safari"

[lighting]
brightness = 0.5
`
	d := Parse(text)
	if d.Layers() != 2 {
		t.Fatalf("layers = %d", d.Layers())
	}
	steps := []func() error{
		func() error { return d.SetKey(0, "ACT07", `{ key = "d" }`) },               // table form -> one line
		func() error { return d.SetKey(1, "ACT08", `{ key = "y" }`) },               // dotted layer keeps dotted
		func() error { return d.SetField(1, "match", `{ app = "Google Chrome" }`) }, // [layer.match] -> inline
		func() error { return d.SetField(0, "name", `"main"`) },
	}
	for _, s := range steps {
		if err := s(); err != nil {
			t.Fatal(err)
		}
	}
	d.RenameLayerRefs("base", "main")
	cfg := mustParse(t, d.String())
	if cfg.Layers[0].Name != "main" || cfg.Layers[0].Keys["ACT07"].Gesture("press").Key[0] != "d" || cfg.Layers[0].Keys["ACT07"].Hold != nil {
		t.Fatalf("layer 1 = %+v\n%s", cfg.Layers[0], d.String())
	}
	if cfg.Layers[1].Keys["ACT08"] == nil || cfg.Layers[1].Match.App != "Google Chrome" || cfg.Lighting.Brightness != 0.5 {
		t.Fatalf("layer 2 = %+v\n%s", cfg.Layers[1], d.String())
	}
	if !strings.Contains(d.String(), `name = "main"   # the base`) || !strings.Contains(d.String(), "keys.ACT08 = ") {
		t.Fatalf("comments or dotted form lost:\n%s", d.String())
	}

	if err := d.DeleteLayer(1); err != nil {
		t.Fatal(err)
	}
	cfg = mustParse(t, d.String())
	if len(cfg.Layers) != 1 || cfg.Lighting.Brightness != 0.5 {
		t.Fatalf("after delete: %+v\n%s", cfg, d.String())
	}
	d.AddLayer(`name = "new"`)
	if err := d.SetKey(1, "ACT06", `{ key = "z" }`); err != nil {
		t.Fatal(err)
	}
	cfg = mustParse(t, d.String())
	if len(cfg.Layers) != 2 || cfg.Layers[1].Keys["ACT06"] == nil {
		t.Fatalf("after add:\n%s", d.String())
	}
}

func TestMultiLineStringsAreNotHeaders(t *testing.T) {
	text := "[[layer]]\nname = \"a\"\n[layer.keys]\nACT06 = { shell = \"\"\"\n[not a header]\necho hi\"\"\" }\nACT07 = { key = \"b\" }\n"
	d := Parse(text)
	if err := d.SetKey(0, "ACT07", `{ key = "c" }`); err != nil {
		t.Fatal(err)
	}
	cfg := mustParse(t, d.String())
	if !strings.Contains(cfg.Layers[0].Keys["ACT06"].Action.Shell, "[not a header]") || cfg.Layers[0].Keys["ACT07"].Action.Key[0] != "c" {
		t.Fatalf("%s", d.String())
	}
}

func TestSetGlobal(t *testing.T) {
	d := Parse(config.Default)
	if err := d.SetGlobal("agents", "always", "true"); err != nil {
		t.Fatal(err)
	}
	got := diff(t, config.Default, d.String()) // replaces the existing line, comment kept
	if len(got) != 1 || !strings.HasPrefix(got[0], "always = true ") || !strings.Contains(got[0], "# true: layers") {
		t.Fatalf("changed = %q", got)
	}
	// A table without the key gets one inserted line.
	d = Parse("[agents]\nmode = \"auto\"\n\n[[layer]]\nname = \"a\"\n")
	_ = d.SetGlobal("agents", "always", "true")
	if got := diff2("[agents]\nmode = \"auto\"\n\n[[layer]]\nname = \"a\"\n", d.String()); got != "+always = true" {
		t.Fatalf("diff = %q", got)
	}
	d = Parse(config.Default)
	_ = d.SetGlobal("agents", "always", "true")
	if !mustParse(t, d.String()).Agents.Always {
		t.Fatal("not set")
	}
	_ = d.SetGlobal("agents", "always", "false")
	if mustParse(t, d.String()).Agents.Always {
		t.Fatal("not cleared")
	}
	d = Parse("[[layer]]\nname = \"a\"\n")
	_ = d.SetGlobal("agents", "always", "true")
	if !mustParse(t, d.String()).Agents.Always {
		t.Fatalf("new table:\n%s", d.String())
	}
}

// diff2 describes one inserted line as "+line".
func diff2(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range bl {
		if i >= len(al) || al[i] != bl[i] {
			if strings.Join(al[i:], "\n") == strings.Join(bl[i+1:], "\n") {
				return "+" + bl[i]
			}
			return "changed at line " + bl[i]
		}
	}
	return ""
}

func TestFourQuoteStringEnd(t *testing.T) {
	text := "[[layer]]\nname = \"a\"\n[layer.keys]\nACT06 = { shell = '''echo 'hello'''' }\nACT07 = { key = \"b\" }\n\n[[layer]]\nname = \"b\"\n[layer.keys]\nACT08 = { key = \"c\" }\n"
	d := Parse(text)
	if err := d.SetKey(0, "ACT06", `{ key = "z" }`); err != nil {
		t.Fatal(err)
	}
	got := diff(t, text, d.String())
	if len(got) != 1 || got[0] != `ACT06 = { key = "z" }` {
		t.Fatalf("changed = %q\n%s", got, d.String())
	}
}

func TestGestureTableKeepsOtherGestures(t *testing.T) {
	text := "[[layer]]\nname = \"a\"\n\n[layer.keys.ACT06]\n# zoom on press\npress = { action = \"pane.zoom\" }\n# close on hold, carefully\nhold = { action = \"pane.close\" }   # really\n"
	d := Parse(text)
	if !d.GestureTable(0, "ACT06") || d.GestureTable(0, "ACT07") {
		t.Fatal("GestureTable")
	}
	if err := d.SetGesture(0, "ACT06", "press", `{ key = "enter" }`); err != nil {
		t.Fatal(err)
	}
	if got := diff(t, text, d.String()); len(got) != 1 || got[0] != `press = { key = "enter" }` {
		t.Fatalf("changed = %q", got)
	}
	if err := d.SetGesture(0, "ACT06", "double", `{ key = "x" }`); err != nil {
		t.Fatal(err)
	}
	if err := d.SetGesture(0, "ACT06", "press", ""); err != nil {
		t.Fatal(err)
	}
	b := mustParse(t, d.String()).Layers[0].Keys["ACT06"]
	if b.Gesture("press") != nil || b.Double.Key[0] != "x" || b.Hold.Action != "pane.close" || !strings.Contains(d.String(), "# close on hold, carefully\nhold") {
		t.Fatalf("%+v\n%s", b, d.String())
	}
	if Parse("[[layer]]\nname = \"a\"\n[layer.keys.ACT06]\naction = \"pane.zoom\"\n").GestureTable(0, "ACT06") {
		t.Fatal("direct fields are not a gesture table")
	}
}

func TestDottedGesturesKeepOtherGestures(t *testing.T) {
	text := "[[layer]]\nname = \"a\"\n[layer.keys]\nACT06.press = { key = \"enter\" }\nACT06.hold = { key = \"escape\" } # explanation\nACT07 = { key = \"y\" }\n"
	d := Parse(text)
	if !d.GestureTable(0, "ACT06") || d.GestureTable(0, "ACT07") {
		t.Fatal("GestureTable")
	}
	if err := d.SetGesture(0, "ACT06", "press", `{ key = "tab" }`); err != nil {
		t.Fatal(err)
	}
	if got := diff(t, text, d.String()); len(got) != 1 || got[0] != `ACT06.press = { key = "tab" }` {
		t.Fatalf("changed = %q", got)
	}
	if err := d.SetGesture(0, "ACT06", "double", `{ key = "x" }`); err != nil {
		t.Fatal(err)
	}
	b := mustParse(t, d.String()).Layers[0].Keys["ACT06"]
	if b.Double.Key[0] != "x" || b.Hold.Key[0] != "escape" || !strings.Contains(d.String(), "# explanation") {
		t.Fatalf("%+v\n%s", b, d.String())
	}
}

func TestLayerDottedGesturesKeepOtherGestures(t *testing.T) {
	text := "[[layer]]\nname = \"a\"\nkeys.ACT06.press = { key = \"enter\" }\nkeys.ACT06.hold = { key = \"escape\" } # explanation\n"
	d := Parse(text)
	if !d.GestureTable(0, "ACT06") {
		t.Fatal("GestureTable")
	}
	if err := d.SetGesture(0, "ACT06", "press", `{ key = "tab" }`); err != nil {
		t.Fatal(err)
	}
	if got := diff(t, text, d.String()); len(got) != 1 || got[0] != `keys.ACT06.press = { key = "tab" }` {
		t.Fatalf("changed = %q", got)
	}
	if err := d.SetGesture(0, "ACT06", "double", `{ key = "x" }`); err != nil {
		t.Fatal(err)
	}
	b := mustParse(t, d.String()).Layers[0].Keys["ACT06"]
	if b.Double.Key[0] != "x" || b.Hold.Key[0] != "escape" || !strings.Contains(d.String(), "# explanation") {
		t.Fatalf("%+v\n%s", b, d.String())
	}
}

func TestRenameTouchesOnlyBuiltins(t *testing.T) {
	text := "[[layer]]\nname = \"base\"\n[layer.keys]\nACT06 = { builtin = \"layer.base\" }   # layer.base\nACT07 = { text = \"layer.base\" }\nACT08 = { shell = 'echo \"layer.base\"' }\nACT09 = { builtin = 'layer.basement' }\n"
	d := Parse(text)
	d.RenameLayerRefs("base", "main")
	got := diff(t, text, d.String())
	if len(got) != 1 || got[0] != `ACT06 = { builtin = "layer.main" }   # layer.base` {
		t.Fatalf("changed = %q", got)
	}
}

func TestRenameReadsTokens(t *testing.T) {
	text := "[[layer]]\nname = \"base\"\n[layer.keys]\nACT06 = { shell = 'echo builtin = \"layer.base\"' }\nACT07 = { text = 'builtin = \"layer.base\"' }\nACT08 = { \"builtin\" = \"layer.base\" }\nACT09 = { builtin = 'layer.base' }   # builtin = \"layer.base\"\n"
	d := Parse(text)
	d.RenameLayerRefs("base", "main")
	got := diff(t, text, d.String())
	want := []string{`ACT08 = { "builtin" = "layer.main" }`, `ACT09 = { builtin = "layer.main" }   # builtin = "layer.base"`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("changed = %q", got)
	}
}

func TestGestureSubtablesKeepComments(t *testing.T) {
	text := "[[layer]]\nname = \"a\"\n\n[layer.keys.ACT06]\npress = { action = \"pane.zoom\" }\n\n# hold closes the pane\n[layer.keys.ACT06.hold]\naction = \"pane.close\"   # careful\n"
	d := Parse(text)
	if !d.GestureTable(0, "ACT06") {
		t.Fatal("subtables of gestures are a gesture table")
	}
	if err := d.SetGesture(0, "ACT06", "press", `{ key = "enter" }`); err != nil {
		t.Fatal(err)
	}
	if got := diff(t, text, d.String()); len(got) != 1 || got[0] != `press = { key = "enter" }` {
		t.Fatalf("changed = %q", got)
	}
	// Only subtables, no [layer.keys.ACT06] yet: adding a gesture opens it.
	text = "[[layer]]\nname = \"a\"\n\n[layer.keys.ACT06.hold]\naction = \"pane.close\"   # careful\n"
	d = Parse(text)
	if err := d.SetGesture(0, "ACT06", "press", `{ key = "enter" }`); err != nil {
		t.Fatal(err)
	}
	b := mustParse(t, d.String()).Layers[0].Keys["ACT06"]
	if b.Gesture("press").Key[0] != "enter" || b.Hold.Action != "pane.close" || !strings.Contains(d.String(), "# careful") {
		t.Fatalf("%s", d.String())
	}
}
