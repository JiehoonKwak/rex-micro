// Package agents decides which pane each Agent Key follows and how it lights.
package agents

import (
	"sort"
	"strings"

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

// Assigner keeps Agent Key assignments stable across refreshes, so a key does
// not jump to another pane when an unrelated agent starts or exits.
type Assigner struct {
	prev [Keys]string // block ID per key in auto mode
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
		sort.SliceStable(agents, func(i, j int) bool { return agents[i].SessionIndex < agents[j].SessionIndex })
		taken := map[string]bool{}
		for i, id := range a.prev {
			if p, ok := byBlock[id]; ok {
				slots[i] = Slot{Pane: p, State: p.State}
				taken[id] = true
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
