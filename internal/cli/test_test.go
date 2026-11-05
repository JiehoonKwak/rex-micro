package cli

import (
	"strings"
	"testing"

	"github.com/JiehoonKwak/rex-micro/internal/engine"
)

func TestStickCentersMatchTheEngine(t *testing.T) {
	for dir, a := range stickCenters {
		if got := "STICK_" + engine.Direction(a, 0); got != dir {
			t.Errorf("center %.2f reads as %s, want %s", a, got, dir)
		}
	}
}

// Readings from the 2026-11-05 hardware test: correct directions, and a stray
// dial tick recorded on the UP step. No offset should be suggested.
func TestStickHintIgnoresCorrectReadings(t *testing.T) {
	steps := []*step{
		{Expect: []string{"STICK_UP"}, Got: []string{"ENC_CC"}},
		{Expect: []string{"STICK_RIGHT"}, Got: []string{"STICK_RIGHT (a=0.01)"}, Angle: 0.01},
		{Expect: []string{"STICK_DOWN"}, Got: []string{"STICK_DOWN (a=0.23)"}, Angle: 0.23},
		{Expect: []string{"STICK_LEFT"}, Got: []string{"STICK_LEFT (a=0.49)"}, Angle: 0.49},
	}
	if h := stickHint(steps); h != "" {
		t.Errorf("suggested %q for correct readings", h)
	}
	for _, s := range steps[1:] {
		s.Angle += 0.25 // a stick mounted a quarter turn off
		s.Got[0] = strings.Replace(s.Got[0], "STICK_", "STICK_X", 1)
	}
	if h := stickHint(steps); !strings.Contains(h, "= 0.7") {
		t.Errorf("rotated stick: hint %q", h)
	}
}
