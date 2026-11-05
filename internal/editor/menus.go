package editor

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/macos"
	"github.com/JiehoonKwak/rex-micro/internal/tomledit"
)

// item is one choice in a list.
type item struct {
	title, desc string
	do          func()
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title + "\x00" + i.desc }

func (m *Model) openList(title string, items []item) {
	li := make([]list.Item, len(items))
	for i, it := range items {
		li[i] = it
	}
	l := list.New(li, list.NewDefaultDelegate(), min(m.width, 90), max(m.height-4, 8))
	l.Title = title
	l.Filter = filter
	l.SetShowHelp(true)
	l.DisableQuitKeybindings()
	m.list, m.mode = l, modeList
}

// filter keeps items containing every word of term, titles starting with it
// first, then other title matches, then description matches.
func filter(term string, targets []string) []list.Rank {
	words := strings.Fields(strings.ToLower(term))
	var groups [3][]list.Rank
	for i, t := range targets {
		low := strings.ToLower(t)
		title := strings.SplitN(low, "\x00", 2)[0]
		ok := true
		var idx []int
		for _, w := range words {
			j := strings.Index(low, w)
			if j < 0 {
				ok = false
				break
			}
			for k := range w {
				if j+k < len(title) {
					idx = append(idx, j+k)
				}
			}
		}
		if !ok {
			continue
		}
		g := 2
		switch {
		case len(words) > 0 && strings.HasPrefix(title, words[0]):
			g = 0
		case len(words) > 0 && strings.Contains(title, words[0]):
			g = 1
		}
		groups[g] = append(groups[g], list.Rank{Index: i, MatchedIndexes: idx})
	}
	return append(append(groups[0], groups[1]...), groups[2]...)
}

func (m *Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && m.list.FilterState() != list.Filtering {
		switch k.String() {
		case "esc", "q":
			if m.list.FilterState() == list.FilterApplied {
				m.list.ResetFilter()
				return m, nil
			}
			m.mode = modeMain
			return m, nil
		case "enter":
			if it, ok := m.list.SelectedItem().(item); ok {
				m.mode = modeMain
				it.do()
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// prompt asks for one line of text, then calls done with it.
func (m *Model) prompt(label, placeholder, value string, done func(string)) {
	m.input.Prompt = label + " "
	m.input.Placeholder = placeholder
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.input.Focus()
	m.next, m.mode = done, modeInput
}

func (m *Model) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			m.mode = modeMain
			return m, nil
		case "enter":
			m.mode = modeMain
			m.next(strings.TrimSpace(m.input.Value()))
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// picker lists every action gesture g of the selected key can do.
func (m *Model) picker(g string) {
	key := m.key()
	set := func(a *config.Action) func() { return func() { m.assign(g, a) } }
	ask := func(label, ph string, build func(string) (*config.Action, error)) func() {
		return func() {
			m.prompt(label, ph, "", func(v string) {
				if v == "" {
					m.flash("cancelled", false)
					return
				}
				a, err := build(v)
				if err != nil {
					m.flash(err.Error(), true)
					return
				}
				m.assign(g, a)
			})
		}
	}
	keys := func(target string) func(string) (*config.Action, error) {
		return func(v string) (*config.Action, error) {
			return &config.Action{Key: config.StringList(strings.Fields(v)), Target: target}, nil
		}
	}
	items := []item{
		{"Shortcut to the front app…", "e.g. cmd+shift+] (next browser tab); needs Accessibility", ask("shortcut:", "cmd+shift+]", func(v string) (*config.Action, error) {
			if _, err := macos.ParseShortcut(v); err != nil {
				return nil, err
			}
			return &config.Action{Shortcut: v}, nil
		})},
		{"Keys to the focused Rex pane…", "space-separated, e.g. enter  or  ctrl+c", ask("keys:", "enter", keys(""))},
		{"Keys to the agent pane…", "to the selected agent, wherever it is", ask("keys:", "y enter", keys("agent"))},
		{"Type text into the agent pane…", "e.g. continue", ask("text:", "continue", func(v string) (*config.Action, error) {
			return &config.Action{Text: strings.ReplaceAll(v, `\n`, "\n")}, nil
		})},
		{"Shell command…", "runs in order, waits up to 10s", ask("shell:", "open -a Spotify", func(v string) (*config.Action, error) {
			return &config.Action{Shell: v}, nil
		})},
		{"Shell command, detached…", "for long-running commands", ask("shell:", "say done", func(v string) (*config.Action, error) {
			return &config.Action{Shell: v, Detach: true}, nil
		})},
		{"rex CLI arguments…", "e.g. run -- lazygit", ask("rex", "run -- lazygit", func(v string) (*config.Action, error) {
			return &config.Action{Rex: strings.Fields(v)}, nil
		})},
		{"Do nothing", "disable this gesture on this layer", set(&config.Action{Builtin: "none"})},
		{"Next layer", "builtin layer.next", set(&config.Action{Builtin: "layer.next"})},
		{"Previous layer", "builtin layer.prev", set(&config.Action{Builtin: "layer.prev"})},
	}
	for _, l := range m.cfg.Layers {
		items = append(items, item{"Layer: " + l.Name, "builtin layer." + l.Name, set(&config.Action{Builtin: "layer." + l.Name})})
	}
	items = append(items,
		item{"Show the agent", "this Agent Key's agent, or the selected one", set(&config.Action{Builtin: "agent.show"})},
		item{"Show the agent, zoomed", "builtin agent.show_zoom", set(&config.Action{Builtin: "agent.show_zoom"})},
		item{"Most urgent agent", "jump to an agent waiting for you", set(&config.Action{Builtin: "agent.attention"})},
	)
	for n := 1; n <= 9; n++ {
		items = append(items, item{fmt.Sprintf("Session %d", n), fmt.Sprintf("builtin session.%d", n), set(&config.Action{Builtin: fmt.Sprintf("session.%d", n)})})
	}
	for _, ra := range m.opts.Actions {
		ra := ra
		do := set(&config.Action{Action: ra.Name})
		if len(ra.Args) > 0 {
			do = func() {
				m.prompt("args:", strings.Join(ra.Args, "=… ")+"=…  (blank for none)", "", func(v string) {
					a := &config.Action{Action: ra.Name}
					for _, kv := range strings.Fields(v) {
						k, val, ok := strings.Cut(kv, "=")
						if !ok {
							m.flash("args are name=value pairs", true)
							return
						}
						if a.Args == nil {
							a.Args = map[string]string{}
						}
						a.Args[k] = val
					}
					m.assign(g, a)
				})
			}
		}
		desc := ra.Name
		if ra.Category != "" {
			desc += " · " + ra.Category
		}
		items = append(items, item{"Rex: " + ra.Title, desc, do})
	}
	m.openList(fmt.Sprintf("%s %s on layer %q — type / to search", key, g, m.cfg.Layers[m.layer].Name), items)
}

var palette = []struct{ name, hex string }{
	{"violet", "#8B5CF6"}, {"cyan", "#06B6D4"}, {"green", "#22C55E"}, {"amber", "#F59E0B"},
	{"red", "#EF4444"}, {"pink", "#EC4899"}, {"blue", "#3B82F6"}, {"white", "#FFFFFF"},
}

func (m *Model) layerMenu() {
	i := m.layer
	l := m.cfg.Layers[i]
	items := []item{
		{"Rename layer…", "now " + l.Name, func() {
			m.prompt("name:", l.Name, l.Name, func(v string) {
				if v == "" || v == l.Name {
					return
				}
				renamed := func(p, before, after string) bool {
					// The layer itself, and bindings whose only change is the new name.
					return layerPaths(l.Name, v)(p, before, after) ||
						strings.ReplaceAll(before, config.Quote("layer."+l.Name), config.Quote("layer."+v)) == after
				}
				m.edit("renamed to "+v, renamed, func(d *tomledit.Doc) error {
					d.RenameLayerRefs(l.Name, v)
					return d.SetField(i, "name", config.Quote(v))
				})
			})
		}},
		{"Color…", "now " + orNone(l.Color), m.colorMenu},
	}
	if i > 0 {
		items = append(items, item{"Switch automatically…", "now " + orNone(l.Match.Describe()), m.matchMenu})
	}
	items = append(items,
		item{"Add a layer…", "a new layer after the others", func() {
			m.prompt("new layer name:", "browser", "", func(v string) {
				if v == "" {
					return
				}
				color := palette[len(m.cfg.Layers)%len(palette)].hex
				if m.edit("added layer "+v, layerPaths(v), func(d *tomledit.Doc) error {
					d.AddLayer("name = "+config.Quote(v), "color = "+config.Quote(color))
					return nil
				}) {
					m.layer = len(m.cfg.Layers) - 1
				}
			})
		}},
		item{"Agent Keys always show agents: " + onOff(m.cfg.Agents.Always), "on ignores layer bindings for AG00-AG05 (A toggles)", func() {
			on := !m.cfg.Agents.Always
			m.edit(fmt.Sprintf("agents.always = %v", on), only("agents"), func(d *tomledit.Doc) error { return d.SetGlobal("agents", "always", fmt.Sprint(on)) })
		}},
	)
	if len(m.cfg.Layers) > 1 {
		items = append(items, item{"Delete this layer", "removes " + l.Name + " and its bindings", func() {
			m.confirm(fmt.Sprintf("Delete layer %q? y deletes", l.Name), func() {
				m.edit("deleted layer "+l.Name, layerPaths(l.Name), func(d *tomledit.Doc) error { return d.DeleteLayer(i) })
			})
		}})
	}
	m.openList(fmt.Sprintf("Layer %d: %s", i+1, l.Name), items)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func onOff(b bool) string { return map[bool]string{true: "on", false: "off"}[b] }

func (m *Model) colorMenu() {
	i := m.layer
	set := func(v string) func() {
		return func() {
			val := ""
			if v != "" {
				val = config.Quote(v)
			}
			m.edit("color "+orNone(v), only("layer "+m.cfg.Layers[i].Name+" color"), func(d *tomledit.Doc) error { return d.SetField(i, "color", val) })
		}
	}
	var items []item
	for _, c := range palette {
		items = append(items, item{lipgloss.NewStyle().Foreground(lipgloss.Color(c.hex)).Render("■ ") + c.name, c.hex, set(c.hex)})
	}
	items = append(items,
		item{"Custom…", "#RRGGBB", func() { m.prompt("color:", "#RRGGBB", m.cfg.Layers[i].Color, func(v string) { set(v)() }) }},
		item{"No color", "the backlight stays off on this layer", set("")},
	)
	m.openList("Color of "+m.cfg.Layers[i].Name, items)
}

func (m *Model) matchMenu() {
	i := m.layer
	set := func(mt *config.Match) func() {
		return func() {
			val := ""
			if mt != nil {
				val = mt.TOML()
			}
			m.edit("switch "+orNone(mt.Describe()), only("layer "+m.cfg.Layers[i].Name+" match"), func(d *tomledit.Doc) error { return d.SetField(i, "match", val) })
		}
	}
	items := []item{
		{"Only by hand", "no automatic switching", set(nil)},
		{"A program in the Rex pane…", "e.g. lazygit, nvim, claude; seen: " + strings.Join(m.opts.Programs, ", "), func() {
			m.prompt("program:", "lazygit", "", func(v string) {
				if v != "" {
					set(&config.Match{Program: v})()
				}
			})
		}},
	}
	if m.opts.Apps != nil {
		for _, app := range m.opts.Apps() {
			items = append(items, item{app, "when " + app + " is in front", set(&config.Match{App: config.AppName(app)})})
		}
	}
	items = append(items, item{"Another app…", "type its name as macOS shows it", func() {
		m.prompt("app:", "Google Chrome", "", func(v string) {
			if v != "" {
				set(&config.Match{App: v})()
			}
		})
	}})
	m.openList("Turn on "+m.cfg.Layers[i].Name+" automatically when…", items)
}
