// Package agents decides which pane each Agent Key follows and how it lights.
package agents

import (
	"sort"
	"strings"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

// Keys is the number of Agent Keys.
const Keys = 6

// Slot is what one Agent Key stands for. Pane is nil for an empty key.
type Slot struct {
	Pane  *rex.Pane
	State string // working, blocked, error, done, idle, or empty
}

// urgency orders states for "most urgent" choices; lower is more urgent.
var urgency = map[string]int{"blocked": 0, "error": 0, "done": 1, "working": 2, "idle": 3}

func rank(state string) int {
	if r, ok := urgency[state]; ok {
		return r
	}
	return 4
}

// sidebarRank orders states the way the Rex sidebar's Activity by Priority
// groups them (observed in Rex Beta 1886): Blocked, Failed, Working, then Done,
// which also holds idle programs.
func sidebarRank(state string) int {
	switch state {
	case "blocked":
		return 0
	case "error":
		return 1
	case "working":
		return 2
	case "done", "idle":
		return 3
	}
	return 4
}

// entry is when a pane entered its current state.
type entry struct {
	state string
	at    time.Time
}

// MostUrgent picks the agent pane needing attention first: blocked or error,
// then done, then working, then idle; the longest-waiting first within a rank.
func MostUrgent(panes []rex.Pane) *rex.Pane {
	var best *rex.Pane
	for i := range panes {
		p := &panes[i]
		if !p.Agent() {
			continue
		}
		if best == nil || rank(p.State) < rank(best.State) ||
			(rank(p.State) == rank(best.State) && p.Since.Before(best.Since)) {
			best = p
		}
	}
	return best
}

// Assigner maps agents onto the keys. With order = "stable" it keeps each
// assignment across refreshes, so a key does not jump to another pane when an
// unrelated agent starts or exits; the sidebar orders re-sort every refresh.
type Assigner struct {
	prev [Keys]string // block ID per key in auto mode
	// entered is when each agent pane entered its state. The sidebar lists the
	// latest entry first within a group; Since moves on every report, so a
	// working agent that keeps reporting would otherwise jump ahead.
	entered map[string]entry
}

// enteredAt records state changes and returns when each pane entered its
// current state. The first sighting of a state takes the record's time: Rex
// keeps only updated_at, so right after rex-micro starts, an agent that
// reported again since entering its state can sit earlier than in the sidebar
// until its next state change.
func (a *Assigner) enteredAt(agents []*rex.Pane) map[string]time.Time {
	seen := make(map[string]entry, len(agents))
	out := make(map[string]time.Time, len(agents))
	for _, p := range agents {
		e, ok := a.entered[p.Block]
		if !ok || e.state != p.State {
			e = entry{p.State, p.Since}
		}
		seen[p.Block], out[p.Block] = e, e.at
	}
	a.entered = seen
	return out
}

// Assign maps the current panes onto the six keys.
func (a *Assigner) Assign(panes []rex.Pane, cfg config.AgentsConfig) [Keys]Slot {
	var slots [Keys]Slot
	switch cfg.Mode {
	case "sessions":
		bySession := map[int][]rex.Pane{}
		for _, p := range panes {
			bySession[p.SessionIndex] = append(bySession[p.SessionIndex], p)
		}
		for i := range slots {
			group := bySession[i+1]
			if len(group) == 0 {
				continue
			}
			if p := MostUrgent(group); p != nil {
				slots[i] = Slot{Pane: p, State: p.State}
				continue
			}
			p := group[0]
			for j := range group {
				if group[j].Focused {
					p = group[j]
				}
			}
			slots[i] = Slot{Pane: &p, State: "empty"}
		}
	case "slots":
		for i, want := range cfg.Slots {
			if i >= Keys {
				break
			}
			for j := range panes {
				p := &panes[j]
				if p.Label == want || p.Block == want || (len(want) >= 6 && strings.HasSuffix(p.Block, want)) {
					slots[i] = Slot{Pane: p, State: stateOf(p)}
					break
				}
			}
		}
	default: // auto
		var agents []*rex.Pane
		byBlock := map[string]*rex.Pane{}
		for i := range panes {
			if panes[i].Agent() {
				agents = append(agents, &panes[i])
				byBlock[panes[i].Block] = &panes[i]
			}
		}
		// The snapshot lists panes in session, then layout order.
		sort.SliceStable(agents, func(i, j int) bool { return agents[i].SessionIndex < agents[j].SessionIndex })
		at := a.enteredAt(agents)
		if cfg.Order == "priority" {
			sort.SliceStable(agents, func(i, j int) bool {
				ri, rj := sidebarRank(agents[i].State), sidebarRank(agents[j].State)
				if ri != rj {
					return ri < rj
				}
				return at[agents[i].Block].After(at[agents[j].Block])
			})
		}
		taken := map[string]bool{}
		if cfg.Order == "stable" {
			for i, id := range a.prev {
				if p, ok := byBlock[id]; ok {
					slots[i] = Slot{Pane: p, State: p.State}
					taken[id] = true
				}
			}
		}
		next := 0
		for _, p := range agents {
			if taken[p.Block] {
				continue
			}
			for next < Keys && slots[next].Pane != nil {
				next++
			}
			if next == Keys {
				break
			}
			slots[next] = Slot{Pane: p, State: p.State}
			taken[p.Block] = true
		}
		for i := range a.prev {
			a.prev[i] = ""
			if slots[i].Pane != nil {
				a.prev[i] = slots[i].Pane.Block
			}
		}
	}
	for i := range slots {
		if slots[i].Pane == nil {
			slots[i].State = "empty"
		}
	}
	return slots
}

func stateOf(p *rex.Pane) string {
	if p.Agent() {
		return p.State
	}
	return "empty"
}

// Lights renders the slots with the configured looks.
func Lights(slots [Keys]Slot, cfg config.LightingConfig) [Keys]protocol.Light {
	var out [Keys]protocol.Light
	for i, s := range slots {
		if s.State == "" {
			s.State = "empty" // nothing known yet
		}
		look, ok := cfg.States[s.State]
		if !ok {
			look = cfg.States["idle"]
		}
		l, err := look.Resolve(cfg.Brightness)
		if err == nil {
			out[i] = l
		}
	}
	return out
}
