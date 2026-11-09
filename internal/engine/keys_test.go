package engine

import (
	"testing"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/protocol"
)

func keysEngine(t *testing.T) (*Engine, *[]bool, *recorder) {
	t.Helper()
	e, r := setup(t, `[[layer]]
name = "base"
[layer.keys]
ENC_CLK = { press = { shortcut = "cmd+shift+a" }, hold = { builtin = "keys.show" } }
ENC_CW = { builtin = "keys.show" }
ACT06 = { shortcut = "cmd+e" }
ACT07 = { builtin = "keys.show" }
`)
	var seen []bool
	e.OnKeys = func(show bool) { seen = append(seen, show) }
	return e, &seen, r
}

func key(e *Engine, name string, down bool, at time.Time) {
	e.Input(protocol.Input{Kind: protocol.InputKey, Key: name, Down: down}, at)
}

// Holding the dial shows the keys for as long as it is held, even while other
// keys are pressed (they still act); letting go hides them, and the press the
// dial would otherwise send does not go out.
func TestHoldShowsKeysUntilRelease(t *testing.T) {
	e, seen, r := keysEngine(t)
	key(e, "ENC_CLK", true, t0)
	e.Advance(t0.Add(600 * time.Millisecond))
	if !e.KeysShown() {
		t.Fatal("holding the dial did not show the keys")
	}
	press(e, "ACT06", t0.Add(time.Second)) // try a key while looking
	e.Advance(t0.Add(time.Minute))         // still held: no timeout
	if !e.KeysShown() {
		t.Fatal("the keys went away while the dial was still held")
	}
	key(e, "ENC_CLK", false, t0.Add(time.Minute))
	if e.KeysShown() || len(*seen) != 2 || (*seen)[1] {
		t.Fatalf("release: shown=%v calls=%v", e.KeysShown(), *seen)
	}
	want(t, r, "shortcut cmd+e")
}

// MIC is two switches under one cap; the overlay follows the one MIC gesture,
// not either switch (review of PR 10).
func TestMICShowsKeysAsOneKey(t *testing.T) {
	for _, tc := range []struct {
		binding  string
		advance  bool // wait for the hold
		afterOne bool // shown after the first switch is let go
		afterAll bool // shown after both are
	}{
		{`{ builtin = "keys.show" }`, false, true, true},
		{`{ press = { builtin = "none" }, hold = { builtin = "keys.show" } }`, true, true, false},
	} {
		e, _ := setup(t, "[[layer]]\nname = \"base\"\n[layer.keys]\nMIC = "+tc.binding+"\n")
		key(e, "ACT10", true, t0)
		key(e, "ACT11", true, t0.Add(10*time.Millisecond))
		if tc.advance {
			e.Advance(t0.Add(time.Second))
		}
		if !e.KeysShown() {
			t.Fatalf("%s: the second switch going down hid the keys", tc.binding)
		}
		key(e, "ACT10", false, t0.Add(2*time.Second))
		if e.KeysShown() != tc.afterOne {
			t.Fatalf("%s: shown=%v after one switch was let go", tc.binding, e.KeysShown())
		}
		key(e, "ACT11", false, t0.Add(2*time.Second+10*time.Millisecond))
		if e.KeysShown() != tc.afterAll {
			t.Fatalf("%s: shown=%v after MIC was let go", tc.binding, e.KeysShown())
		}
	}
}

// A press shows the keys for KeysFor: the release of that press does not end
// them, any other input does (and still acts).
func TestPressShowsKeysForAWhile(t *testing.T) {
	e, _, r := keysEngine(t)
	press(e, "ACT07", t0) // fires on down; its release follows
	if !e.KeysShown() {
		t.Fatal("the press's own release hid the keys")
	}
	if d := e.NextDeadline(); !d.Equal(t0.Add(KeysFor)) {
		t.Fatalf("next deadline %v, want the overlay's end", d)
	}
	e.Advance(t0.Add(KeysFor - time.Millisecond))
	if !e.KeysShown() {
		t.Fatal("gone too early")
	}
	e.Advance(t0.Add(KeysFor))
	if e.KeysShown() {
		t.Fatal("still shown after KeysFor")
	}

	e.Input(protocol.Input{Kind: protocol.InputDial, Key: "ENC_CW"}, t0)
	if !e.KeysShown() {
		t.Fatal("a dial turn did not show the keys")
	}
	press(e, "ACT06", t0.Add(time.Second))
	if e.KeysShown() {
		t.Fatal("another key left the keys up")
	}
	want(t, r, "shortcut cmd+e")
}
