package engine

import (
	"slices"
	"testing"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/protocol"
	"github.com/JiehoonKwak/rex-micro/internal/usage"
)

// Each use counts once, under the layer whose binding it is and by what it
// does.
func TestUsesCountOncePerPadUse(t *testing.T) {
	e, _ := setup(t, `[[layer]]
name = "base"
[layer.keys]
ACT06 = { action = "pane.zoom", label = "Zoom" }
ACT07 = { press = { key = "a" }, double = { key = "b" } }
MIC = { shortcut = "right_option", held = true, label = "Dictation" }
ACT12 = { down = { shell = "start" }, up = { shell = "stop" } }
ENC_CW = { key = "page_down" }

[[layer]]
name = "browser"
match = { app = "Brave Browser" }
[layer.keys]
ACT09 = { shortcut = "cmd+w", label = "Close tab" }
`)
	var got []string
	e.OnUse = func(u usage.Use) { got = append(got, u.Layer+" "+u.Key+" "+u.Gesture+" "+u.Does) }
	e.SetContext("Brave Browser", "")
	ms := time.Millisecond
	key := func(k string, down bool, at time.Duration) {
		e.Input(protocol.Input{Kind: protocol.InputKey, Key: k, Down: down}, t0.Add(at))
	}
	key("ACT06", true, 0) // a base binding, used while browser is on
	key("ACT06", false, 30*ms)
	key("ACT09", true, 100*ms)
	key("ACT09", false, 130*ms)
	key("ACT10", true, 200*ms) // MIC: held shortcut, one press
	key("ACT10", false, 900*ms)
	key("ACT12", true, 1000*ms) // down and up, one use
	key("ACT12", false, 1100*ms)
	e.Input(protocol.Input{Kind: protocol.InputDial, Key: "ENC_CW"}, t0.Add(1200*ms))
	e.Input(protocol.Input{Kind: protocol.InputDial, Key: "ENC_CW"}, t0.Add(1210*ms))
	// A press settled later by the double window's timer counts too.
	key("ACT07", true, 4000*ms)
	key("ACT07", false, 4030*ms)
	e.Advance(t0.Add(4500 * ms))
	want := []string{
		"base ACT06 press Zoom",
		"browser ACT09 press Close tab",
		"base MIC press Dictation",
		"base ACT12 down shell start",
		"base ENC_CW press key page_down",
		"base ENC_CW press key page_down",
		"base ACT07 press key a",
	}
	if !slices.Equal(got, want) {
		t.Errorf("uses\n got %q\nwant %q", got, want)
	}
	// What a report lists is what counting records, unused ones too.
	listed := map[usage.Use]bool{}
	for i := range e.cfg.Layers {
		for _, u := range e.Uses(i) {
			listed[u] = true
		}
	}
	for _, s := range append(want, "base ACT07 double key b", "base AG00 press show agent") {
		found := false
		for u := range listed {
			if u.Layer+" "+u.Key+" "+u.Gesture+" "+u.Does == s {
				found = true
			}
		}
		if !found {
			t.Errorf("usage does not list %q; it lists %v", s, listed)
		}
	}
	for u := range listed {
		if u.Gesture == "up" || (u.Key == "MIC" && u.Gesture != "press") {
			t.Errorf("listed a use that is never counted: %+v", u)
		}
	}
}

// A binding is known by what it does, not its label: a key rebound under the
// same label is a new binding, a relabelled one is the same, and a
// down/up binding changes when only its release does.
func TestUseIsKnownByItsAction(t *testing.T) {
	use := func(cfg string) usage.Use {
		e, _ := setup(t, "[[layer]]\nname = \"base\"\n[layer.keys]\n"+cfg)
		for _, u := range e.Uses(0) {
			if u.Key == "ACT07" {
				return u
			}
		}
		t.Fatalf("no ACT07 in %s", cfg)
		return usage.Use{}
	}
	id := func(u usage.Use) usage.Use { u.Does = ""; return u }
	enter := use(`ACT07 = { key = "enter", label = "Approve" }`)
	if id(enter) == id(use(`ACT07 = { key = "y", label = "Approve" }`)) {
		t.Error("a rebound key kept its identity")
	}
	if id(enter) != id(use(`ACT07 = { key = "enter", label = "OK" }`)) {
		t.Error("a new label made a new binding")
	}
	if id(use(`ACT07 = { down = { shell = "start" }, up = { shell = "stop" } }`)) == id(use(`ACT07 = { down = { shell = "start" }, up = { shell = "halt" } }`)) {
		t.Error("changing the release kept the identity")
	}
}
