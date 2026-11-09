package daemon

import (
	"strings"
	"testing"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
)

// `rex-micro press` must reach Rex the way a pad press does: an Agent Key
// shows its agent's pane once its gesture has fired, and the reply names it.
func TestPressShowsTheAgentLikeThePad(t *testing.T) {
	rx := &fakeRex{}
	_, _, stop := startDaemon(t, "", rx)
	defer stop()
	eventually(t, "agent assigned", func() bool { var s Status; return Call("status", &s) == nil && s.Slots[0].Block == "b1" })

	out, err := Press("ag00", "")
	if err != nil || out.Error != "" {
		t.Fatalf("press: %v %q", err, out.Error)
	}
	// The reply comes after the effect ran, so the caller can check Rex at once.
	if !rx.inOrder("focus -s s1 b1", "-C client:app do session.select session_id=s1 window_id=w1") {
		t.Fatalf("pane not shown before the reply; calls %v", rx.calls)
	}
	if out.Agent == nil || out.Agent.Block != "b1" || !strings.Contains(out.Binding, "agent.show") {
		t.Fatalf("reply = %+v", out)
	}
	if rx.has("zoom b1 --on") {
		t.Fatal("a single press zoomed")
	}

	if out, err := Press("AG00", "double"); err != nil || out.Error != "" {
		t.Fatalf("double: %v %q", err, out.Error)
	}
	if !rx.has("zoom b1 --on") {
		t.Fatalf("double press did not zoom; calls %v", rx.calls)
	}

	if out, _ := Press("ENC_CW", "hold"); out.Error == "" {
		t.Fatal("a dial turn accepted hold")
	}
	if out, _ := Press("ACT10", ""); out.Error == "" {
		t.Fatal("unknown key accepted")
	}
}

// Every stick direction's synthesized angle lands in that direction, whatever
// the configured offset.
func TestPressStickAngles(t *testing.T) {
	for _, off := range []float64{0, 0.1, -0.2} {
		for _, dir := range []string{"UP", "DOWN", "LEFT", "RIGHT"} {
			steps, err := pressSteps("STICK_"+dir, "press", config.TimingConfig{}, off)
			if err != nil {
				t.Fatal(err)
			}
			if got := engine.Direction(steps[0].in.Angle, off); got != dir || steps[0].in.Kind != protocol.InputStick {
				t.Fatalf("offset %v: STICK_%s reads as %s", off, dir, got)
			}
		}
	}
}
