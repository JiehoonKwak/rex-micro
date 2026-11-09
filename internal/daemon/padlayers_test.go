package daemon

import (
	"errors"
	"log"
	"testing"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	"github.com/JiehoonKwak/rex-micro/internal/micro"
)

// deafPad takes keymap reads and writes but never confirms a layer notice.
type deafPad struct{}

func (deafPad) ReadFile(string) (string, error)            { return "", nil }
func (deafPad) WriteFile(string, string) error             { return nil }
func (deafPad) Status(time.Duration) (micro.Status, error) { return micro.Status{LayerIndex: 1}, nil }
func (deafPad) FocusApp(string) error                      { return errors.New("no reply") }

func padDaemon(t *testing.T) *daemon {
	t.Helper()
	cfg, err := config.Parse("[pad]\nlayers = \"sync\"\n[[layer]]\nname = \"agents\"\n[[layer]]\nname = \"layout\"\n[[layer]]\nname = \"review\"\n")
	if err != nil {
		t.Fatal(err)
	}
	d := &daemon{logger: log.New(testWriter{t}, "", 0), cfg: cfg, padTell: make(chan int, 1), padSeen: make(chan int, 1)}
	d.eng = engine.New(cfg, nil, nil, nil)
	d.eng.OnLayer = d.tellPad
	d.padSynced, d.status.Connected = true, true
	d.updatePadOn()
	return d
}

// The touch sensor picks a layer: the pad reports it (1-based) and the engine
// follows, as if the layer were picked by hand.
func TestTouchSensorPicksTheLayer(t *testing.T) {
	d := padDaemon(t)
	d.onPadLayer(3)
	if got := d.eng.Layer(); got != 2 {
		t.Fatalf("engine layer = %d, want 2 (the pad's third)", got)
	}
	d.onPadLayer(9) // a pad layer rex-micro did not store
	if got := d.eng.Layer(); got != 2 {
		t.Fatalf("an unknown pad layer moved the engine to %d", got)
	}
}

// A layer rex-micro turns on is sent to the pad, and a poll that still shows
// the old layer (the switch is on its way) must not switch the engine back.
func TestLayerSentToThePadIsNotUndoneByAnOldPoll(t *testing.T) {
	d := padDaemon(t)
	d.eng.SelectLayer(1) // e.g. a match rule or the dial
	select {
	case i := <-d.padTell:
		if i != 1 {
			t.Fatalf("told the pad layer %d, want 1", i)
		}
	default:
		t.Fatal("the new layer was not sent to the pad")
	}
	d.onPadLayer(1) // polled before the pad switched
	if got := d.eng.Layer(); got != 1 {
		t.Fatalf("an old poll switched the engine back to %d", got)
	}
	d.padToldAt = time.Now().Add(-2 * padSettle)
	d.onPadLayer(1) // long after: the user touched back to the first layer
	if got := d.eng.Layer(); got != 0 {
		t.Fatalf("engine layer = %d, want 0 after the touch", got)
	}
}

// Two quick touches: the second is a touch too, not a stale poll, or the keys
// would run the previous layer's bindings (review of PR 9).
func TestQuickTouchesFollowTheLastOne(t *testing.T) {
	d := padDaemon(t)
	d.onPadLayer(2)
	d.onPadLayer(3)
	if got := d.eng.Layer(); got != 2 {
		t.Fatalf("engine layer = %d after touching to the third layer, want 2", got)
	}
}

// A layer the pad never took must not be undone by polls of the layer it still
// shows: rex-micro stops following the pad instead (review of PR 9).
func TestFailedNoticeStopsFollowingThePad(t *testing.T) {
	d := padDaemon(t)
	d.padIO = deafPad{}
	d.requests = make(chan request)
	stop := make(chan struct{})
	defer close(stop)
	go d.padWorker(stop)
	d.eng.SelectLayer(2) // e.g. an app match
	select {
	case r := <-d.requests:
		if r.padLost == nil {
			t.Fatalf("worker sent %+v, want a lost notice", r)
		}
		d.onPadLost(r.padLost)
	case <-time.After(3 * time.Second):
		t.Fatal("the failed notice was not reported")
	}
	d.padToldAt = time.Time{}
	d.onPadLayer(1) // the pad still shows its first layer
	if got := d.eng.Layer(); got != 2 {
		t.Fatalf("a poll undid the app's layer: engine on %d", got)
	}
	if d.padOn.Load() {
		t.Fatal("still polling the pad")
	}
}

// While paused (`rex-micro test`) the pad's layer is neither polled nor followed.
func TestPausedDaemonIgnoresThePadLayer(t *testing.T) {
	d := padDaemon(t)
	d.pauses = 1
	d.updatePadOn()
	d.onPadLayer(2)
	if d.padOn.Load() || d.eng.Layer() != 0 {
		t.Fatalf("paused daemon followed the pad: polling=%v layer=%d", d.padOn.Load(), d.eng.Layer())
	}
}
