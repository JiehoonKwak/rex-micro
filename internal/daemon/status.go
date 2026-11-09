package daemon

import (
	"fmt"

	"github.com/JiehoonKwak/rex-micro/internal/agents"
	"github.com/JiehoonKwak/rex-micro/internal/hid"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

// SlotStatus describes one Agent Key in Status.
type SlotStatus struct {
	Key     string `json:"key"`
	State   string `json:"state"`
	Block   string `json:"block,omitempty"`
	Host    string `json:"host,omitempty"`    // "" for this Mac
	Session string `json:"session,omitempty"` // its label
	// SessionID and Window locate the pane for `rex [-S host] session inspect`.
	SessionID string `json:"session_id,omitempty"`
	Window    string `json:"window,omitempty"`
	Label     string `json:"label,omitempty"`
	App       string `json:"app,omitempty"`
}

func slotStatus(key string, sl agents.Slot) SlotStatus {
	ss := SlotStatus{Key: key, State: sl.State}
	if p := sl.Pane; p != nil {
		ss.Block, ss.Host, ss.Session, ss.SessionID, ss.Window, ss.Label, ss.App = p.Block, p.Host, p.SessionLabel, p.Session, p.Window, p.Label, p.App
	}
	return ss
}

// ShownPane locates a pane in Rex: `rex [-S host] session inspect session`.
type ShownPane struct {
	Host    string `json:"host,omitempty"` // "" for this Mac
	Session string `json:"session"`
	Label   string `json:"session_label"`
	Window  string `json:"window"`
	Block   string `json:"block"`
}

// Status is what `rex-micro status` prints.
type Status struct {
	PID        int          `json:"pid"`
	Executable string       `json:"executable"`
	Access     string       `json:"input_monitoring"`
	Connected  bool         `json:"connected"`
	Transport  string       `json:"transport,omitempty"`
	Firmware   string       `json:"firmware,omitempty"`
	Battery    float64      `json:"battery,omitempty"`
	Layer      string       `json:"layer"`
	LayerIndex int          `json:"layer_index"`
	Layers     []string     `json:"layers"`
	Slots      []SlotStatus `json:"agent_keys"`
	Selected   string       `json:"selected_agent,omitempty"`
	Paused     bool         `json:"paused"`
	Config     string       `json:"config"`
	ConfigErr  string       `json:"config_error,omitempty"`
	RexErr     string       `json:"rex_error,omitempty"`
	LastErr    string       `json:"last_error,omitempty"`
	// Foreign is when another app last changed the pad's lights (RFC 3339).
	Foreign string `json:"foreign_lighting,omitempty"`
	// Accessibility is granted or denied; shortcut bindings need it.
	Accessibility string `json:"accessibility"`
	FrontApp      string `json:"front_app,omitempty"`
	// Shown is the pane the Rex app most likely shows (rex.Current).
	Shown   *ShownPane `json:"shown_pane,omitempty"`
	Program string     `json:"program,omitempty"` // in the pane Rex shows
	// PadLayers is the pad's own layers: off, syncing, sync or "error: ...".
	PadLayers string `json:"pad_layers,omitempty"`
}

func (d *daemon) snapshotStatus() Status {
	s := d.status
	s.Access = hid.CheckAccess().String()
	s.Accessibility = "denied"
	if d.trusted() {
		s.Accessibility = "granted"
	}
	s.LayerIndex = d.eng.Layer()
	s.Layer = d.cfg.Layers[s.LayerIndex].Name
	s.Layers = nil
	for _, l := range d.cfg.Layers {
		s.Layers = append(s.Layers, l.Name)
	}
	s.Slots = nil
	for i, sl := range d.slots {
		s.Slots = append(s.Slots, slotStatus(fmt.Sprintf("AG%02d", i), sl))
	}
	if p := rex.Current(d.panes); p != nil {
		s.Shown = &ShownPane{Host: p.Host, Session: p.Session, Label: p.SessionLabel, Window: p.Window, Block: p.Block}
	}
	s.Selected = d.eng.Selected()
	s.Paused = d.pauses > 0
	s.ConfigErr = d.cfgErr
	return s
}
