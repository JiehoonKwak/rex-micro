package agents

import (
	"strings"
	"testing"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

func cfg(t *testing.T, text string) *config.Config {
	t.Helper()
	c, err := config.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func agent(block, state string, session int) rex.Pane {
	return rex.Pane{Block: block, App: "claude-code", State: state, SessionIndex: session}
}

func blocks(s [Keys]Slot) [Keys]string {
	var out [Keys]string
	for i, x := range s {
		if x.Pane != nil {
			out[i] = x.Pane.Block
		}
	}
	return out
}

func TestAutoAssignmentIsStable(t *testing.T) {
	c := cfg(t, "")
	var a Assigner
	shell := rex.Pane{Block: "sh", SessionIndex: 1}
	got := blocks(a.Assign([]rex.Pane{shell, agent("a", "working", 1), agent("b", "idle", 2)}, c.Agents))
	if got != [Keys]string{"a", "b"} {
		t.Fatalf("first = %v", got)
	}
	// a exits, c starts in an earlier session: b keeps key 2, c takes the free key 1.
	got = blocks(a.Assign([]rex.Pane{agent("c", "working", 1), agent("b", "idle", 2)}, c.Agents))
	if got != [Keys]string{"c", "b"} {
		t.Fatalf("after change = %v", got)
	}
}

func TestSessionsModeShowsMostUrgent(t *testing.T) {
	c := cfg(t, "[agents]\nmode = \"sessions\"\n")
	var a Assigner
	now := time.Now()
	panes := []rex.Pane{
		agent("w", "working", 1), agent("x", "blocked", 1),
		{Block: "plain", SessionIndex: 2, Focused: true},
	}
	panes[1].Since = now
	s := a.Assign(panes, c.Agents)
	if s[0].Pane.Block != "x" || s[0].State != "blocked" {
		t.Fatalf("session 1 = %+v", s[0])
	}
	if s[1].Pane.Block != "plain" || s[1].State != "empty" || s[2].State != "empty" {
		t.Fatalf("session 2/3 = %+v %+v", s[1], s[2])
	}
}

func TestSlotsMode(t *testing.T) {
	c := cfg(t, "[agents]\nmode = \"slots\"\nslots = [\"api\", \"nope\"]\n")
	var a Assigner
	s := a.Assign([]rex.Pane{{Block: "b9", Label: "api", App: "codex", State: "done"}}, c.Agents)
	if s[0].Pane == nil || s[0].State != "done" || s[1].State != "empty" {
		t.Fatalf("slots = %+v", s)
	}
}

// Each agent state lights its key with the configured look (check 2 of the brief).
func TestStateLights(t *testing.T) {
	c := cfg(t, "")
	var slots [Keys]Slot
	states := []string{"working", "blocked", "error", "done", "idle", "empty"}
	for i, st := range states {
		p := agent("p", st, 1)
		slots[i] = Slot{Pane: &p, State: st}
	}
	got := Lights(slots, c.Lighting)
	wantColor := []int{0x3B82F6, 0xF59E0B, 0xEF4444, 0x22C55E, 0xFFFFFF, 0}
	wantEffect := []int{4, 4, 4, 1, 1, 0}
	for i := range states {
		if got[i].Color != wantColor[i] || got[i].Effect != wantEffect[i] {
			t.Errorf("%s: got %+v", states[i], got[i])
		}
	}
	if got[4].Brightness != 0.5*0.8 {
		t.Errorf("idle brightness = %v, want state 0.5 x master 0.8", got[4].Brightness)
	}
	// Hardware check 2026-11-05: breath at speed 0 left the working key dark.
	for i := 0; i < 3; i++ {
		if got[i].Speed == 0 {
			t.Errorf("%s breathes at speed 0, which freezes it", states[i])
		}
	}
	msg := string(protocol.AgentKeys(got))
	if want := `{"b":0.8,"c":16096779,"e":4,"id":1,"s":0.8}`; !strings.Contains(msg, want) {
		t.Errorf("blocked entry missing from %s", msg)
	}
}
