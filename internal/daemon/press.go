package daemon

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/agents"
	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
)

// A press request makes the daemon handle a key as if it came from the pad:
// the same inputs go through the same engine, so layers, gestures, Agent Key
// resolution and every effect behave as for a physical press. It lets agents
// and scripts exercise a binding in the real Rex app without the pad.

// Pressed is the reply to a press request.
type Pressed struct {
	Key     string      `json:"key"`
	Gesture string      `json:"gesture"`
	Layer   string      `json:"layer"`
	Binding string      `json:"binding"`         // what the key does in that layer
	Agent   *SlotStatus `json:"agent,omitempty"` // an Agent Key's pane when pressed
	Log     []string    `json:"log,omitempty"`   // what the engine logged meanwhile (errors, layer changes)
	Error   string      `json:"error,omitempty"`
}

// tap is how long a synthesized key stays down for a press or a double.
const tap = 30 * time.Millisecond

// step is one synthesized input, sent wait after the previous one.
type step struct {
	wait time.Duration
	in   protocol.Input
}

// pressSteps turns a key and gesture into the inputs the pad would send.
func pressSteps(key, gesture string, t config.TimingConfig, stickOffset float64) ([]step, error) {
	if !contains(config.KeyNames, key) {
		return nil, fmt.Errorf("unknown key %q; keys are %s", key, strings.Join(config.KeyNames, ", "))
	}
	if gesture != "press" && gesture != "double" && gesture != "hold" {
		return nil, fmt.Errorf("unknown gesture %q (press, double or hold)", gesture)
	}
	switch {
	case key == "ENC_CW" || key == "ENC_CC":
		if gesture != "press" {
			return nil, fmt.Errorf("%s is a dial turn; it only has press", key)
		}
		return []step{{in: protocol.Input{Kind: protocol.InputDial, Key: key}}}, nil
	case strings.HasPrefix(key, "STICK_"):
		if gesture != "press" {
			return nil, fmt.Errorf("%s is a stick direction; it only has press", key)
		}
		// The angle at the middle of the direction's quadrant (see engine.Direction).
		center := map[string]float64{"STICK_UP": 0.75, "STICK_RIGHT": 0, "STICK_DOWN": 0.25, "STICK_LEFT": 0.5}[key]
		a := math.Mod(center-stickOffset+2, 1)
		return []step{
			{in: protocol.Input{Kind: protocol.InputStick, Angle: a, Dist: 1}},
			{wait: tap, in: protocol.Input{Kind: protocol.InputStick, Angle: a, Dist: 0}},
		}, nil
	}
	if key == "MIC" {
		key = "ACT10" // the wide keycap; the engine merges ACT10 and ACT11 into MIC
	}
	down := protocol.Input{Kind: protocol.InputKey, Key: key, Down: true}
	up := protocol.Input{Kind: protocol.InputKey, Key: key}
	switch gesture {
	case "double":
		return []step{{in: down}, {wait: tap, in: up}, {wait: tap, in: down}, {wait: tap, in: up}}, nil
	case "hold":
		return []step{{in: down}, {wait: t.Hold.Duration + 50*time.Millisecond, in: up}}, nil
	}
	return []step{{in: down}, {wait: tap, in: up}}, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// pressLog collects engine log lines while a press runs.
type pressLog struct {
	mu    sync.Mutex
	on    bool
	lines []string
}

func (l *pressLog) add(line string) {
	l.mu.Lock()
	if l.on {
		l.lines = append(l.lines, line)
	}
	l.mu.Unlock()
}

// start begins collecting; false when a press is already running.
func (l *pressLog) start() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.on {
		return false
	}
	l.on, l.lines = true, nil
	return true
}

func (l *pressLog) stop() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.on = false
	return l.lines
}

// onPress runs in the main loop: it plans the press, then feeds its inputs
// back into the loop from a goroutine so timing never blocks the loop.
func (d *daemon) onPress(r request) {
	key, gesture := strings.ToUpper(r.Key), r.Gesture
	if gesture == "" {
		gesture = "press"
	}
	out := Pressed{Key: key, Gesture: gesture}
	steps, err := pressSteps(key, gesture, d.cfg.Timing, d.cfg.Stick.AngleOffset)
	switch {
	case err != nil:
	case d.pauses > 0:
		err = errors.New("paused while `rex-micro test` runs")
	case !d.presses.start():
		err = errors.New("another press is still running")
	}
	if err != nil {
		out.Error = err.Error()
		r.reply <- out
		return
	}
	out.Layer = d.cfg.Layers[d.eng.Layer()].Name
	out.Binding = d.eng.Describe(key)
	if i, ok := agentKey(key); ok {
		if d.slots[i].Pane != nil {
			ss := slotStatus(key, d.slots[i])
			out.Agent = &ss
		}
	}
	d.logger.Printf("%s %s from rex-micro press", key, gesture)
	settleFor := d.cfg.Timing.DoublePress.Duration + d.cfg.Timing.Hold.Duration + time.Second
	go func() {
		for _, s := range steps {
			time.Sleep(s.wait)
			in := s.in
			d.requests <- request{input: &in}
		}
		// Wait for a single press that waits out the double-press window,
		// then for its effects, so the caller sees their result in Rex.
		deadline := time.Now().Add(settleFor)
		for time.Now().Before(deadline) {
			idle := make(chan bool, 1)
			d.requests <- request{idle: idle}
			if <-idle {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !d.effects.drain(shellTimeout + 3*time.Second) {
			out.Error = "its actions were still running when the wait ended; check Rex before judging the result"
		}
		out.Log = d.presses.stop()
		r.reply <- out
	}()
}

func agentKey(name string) (int, bool) {
	if len(name) == 4 && strings.HasPrefix(name, "AG0") && name[3] >= '0' && name[3] < '0'+agents.Keys {
		return int(name[3] - '0'), true
	}
	return 0, false
}
