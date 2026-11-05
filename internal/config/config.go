// Package config loads and validates rex-micro's TOML configuration.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/JiehoonKwak/rex-micro/internal/macos"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
)

//go:embed default.toml
var Default string

// Config is the whole file.
type Config struct {
	Rex      RexConfig      `toml:"rex"`
	Agents   AgentsConfig   `toml:"agents"`
	Lighting LightingConfig `toml:"lighting"`
	Stick    StickConfig    `toml:"stick"`
	Timing   TimingConfig   `toml:"timing"`
	Layers   []Layer        `toml:"layer"`
}

type RexConfig struct {
	App  string   `toml:"app"`
	Poll Duration `toml:"poll"`
}

type AgentsConfig struct {
	Mode   string   `toml:"mode"`
	Slots  []string `toml:"slots"`
	Press  string   `toml:"press"`
	Double string   `toml:"double"`
	// Always keeps the Agent Keys on their agents in every layer, ignoring
	// layer bindings for AG00-AG05.
	Always bool `toml:"always"`
}

type LightingConfig struct {
	Brightness float64          `toml:"brightness"`
	Backlight  string           `toml:"backlight"`
	States     map[string]Light `toml:"states"`
}

// Light is a lighting look in the config file.
type Light struct {
	Color      string   `toml:"color"`
	Effect     string   `toml:"effect"`
	Brightness *float64 `toml:"brightness"`
	Speed      float64  `toml:"speed"`
}

type StickConfig struct {
	Threshold   float64 `toml:"threshold"`
	AngleOffset float64 `toml:"angle_offset"`
}

type TimingConfig struct {
	DoublePress Duration `toml:"double_press"`
	Hold        Duration `toml:"hold"`
}

type Layer struct {
	Name  string                 `toml:"name"`
	Color string                 `toml:"color"`
	Match *Match                 `toml:"match"`
	Keys  map[string]*KeyBinding `toml:"keys"`
}

// Match selects a layer automatically: when App is in front and, if set,
// Program runs in the pane the Rex app shows.
type Match struct {
	App     string `toml:"app"`
	Program string `toml:"program"`
}

// RexApp is the app name a match uses for Rex in any edition (Rex, Rex Beta).
const RexApp = "Rex"

// AppName folds Rex editions into RexApp so `app = "Rex"` matches them all.
func AppName(front string) string {
	if front == RexApp || strings.HasPrefix(front, RexApp+" ") {
		return RexApp
	}
	return front
}

// Matches reports whether the match holds for the front app and program.
func (m *Match) Matches(app, program string) bool {
	if m == nil {
		return false
	}
	want := m.App
	if want == "" {
		want = RexApp // a program runs in a Rex pane
	}
	if !strings.EqualFold(AppName(want), AppName(app)) {
		return false
	}
	return m.Program == "" || strings.EqualFold(m.Program, program)
}

// Action is one thing a key does. Exactly one field is set.
type Action struct {
	Action  string            `toml:"action"`
	Args    map[string]string `toml:"args"`
	Key     StringList        `toml:"key"`
	Target  string            `toml:"target"`
	Text    string            `toml:"text"`
	Rex     []string          `toml:"rex"`
	Shell   string            `toml:"shell"`
	Detach  bool              `toml:"detach"`
	Builtin string            `toml:"builtin"`
	// Shortcut is a key combination sent to the frontmost app, e.g. "cmd+shift+]".
	Shortcut string `toml:"shortcut"`
}

// KeyBinding is either a single Action (the press) or per-gesture actions.
type KeyBinding struct {
	Action
	Press  *Action `toml:"press"`
	Double *Action `toml:"double"`
	Hold   *Action `toml:"hold"`
	Down   *Action `toml:"down"`
	Up     *Action `toml:"up"`
}

// UsesShortcut reports whether any binding sends a shortcut, which needs
// Accessibility.
func (c *Config) UsesShortcut() bool {
	for _, l := range c.Layers {
		for _, b := range l.Keys {
			for _, g := range gestureNames {
				if a := b.Gesture(g); a != nil && a.Shortcut != "" {
					return true
				}
			}
		}
	}
	return false
}

// Describe says when a layer turns on by itself, or "" when it does not.
func (m *Match) Describe() string {
	switch {
	case m == nil:
		return ""
	case m.Program == "":
		return m.App + " in front"
	default:
		return m.Program + " in the Rex pane"
	}
}

// Kind names the one thing an Action does, or "" when it is empty.
func (a *Action) Kind() string {
	if a == nil {
		return ""
	}
	var kinds []string
	if a.Action != "" {
		kinds = append(kinds, "action")
	}
	if len(a.Key) > 0 {
		kinds = append(kinds, "key")
	}
	if a.Text != "" {
		kinds = append(kinds, "text")
	}
	if len(a.Rex) > 0 {
		kinds = append(kinds, "rex")
	}
	if a.Shell != "" {
		kinds = append(kinds, "shell")
	}
	if a.Builtin != "" {
		kinds = append(kinds, "builtin")
	}
	if a.Shortcut != "" {
		kinds = append(kinds, "shortcut")
	}
	return strings.Join(kinds, "+")
}

// Gesture returns the action for press, double, hold, down or up.
func (k *KeyBinding) Gesture(name string) *Action {
	if k == nil {
		return nil
	}
	switch name {
	case "press":
		if k.Press != nil {
			return k.Press
		}
		if k.Action.Kind() != "" {
			return &k.Action
		}
	case "double":
		return k.Double
	case "hold":
		return k.Hold
	case "down":
		return k.Down
	case "up":
		return k.Up
	}
	return nil
}

// StringList accepts a string or an array of strings.
type StringList []string

func (s *StringList) UnmarshalTOML(v any) error {
	switch t := v.(type) {
	case string:
		*s = []string{t}
	case []any:
		for _, e := range t {
			str, ok := e.(string)
			if !ok {
				return fmt.Errorf("key list must hold strings, got %T", e)
			}
			*s = append(*s, str)
		}
	default:
		return fmt.Errorf("key must be a string or a list of strings, got %T", v)
	}
	return nil
}

// Duration parses "300ms"-style strings.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("invalid duration %q (use e.g. \"300ms\")", b)
	}
	d.Duration = v
	return nil
}

// Keys every layer may bind.
var KeyNames = []string{
	"AG00", "AG01", "AG02", "AG03", "AG04", "AG05",
	"ACT06", "ACT07", "ACT08", "ACT09", "MIC", "ACT12",
	"ENC_CLK", "ENC_CW", "ENC_CC",
	"STICK_UP", "STICK_DOWN", "STICK_LEFT", "STICK_RIGHT",
}

var States = []string{"working", "blocked", "error", "done", "idle", "empty"}

// Path returns the config file location.
func Path() string {
	if p := os.Getenv("REX_MICRO_CONFIG"); p != "" {
		return p
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "rex-micro", "config.toml")
}

// Load reads the file at path, or the built-in default when it does not exist.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Parse(Default)
	}
	if err != nil {
		return nil, err
	}
	return Parse(string(data))
}

// Parse decodes and validates a config. Missing settings take default values.
func Parse(text string) (*Config, error) {
	cfg := &Config{}
	if _, err := toml.Decode(Default, cfg); err != nil {
		return nil, fmt.Errorf("built-in default: %w", err)
	}
	defaults := *cfg
	cfg.Layers = nil
	cfg.Lighting.States = map[string]Light{}
	md, err := toml.Decode(text, cfg)
	if err != nil {
		var pe toml.ParseError
		if errors.As(err, &pe) {
			return nil, fmt.Errorf("line %d: %s", pe.Position.Line, pe.Message)
		}
		return nil, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		var names []string
		for _, k := range und {
			names = append(names, k.String())
		}
		return nil, fmt.Errorf("unknown setting %s", strings.Join(names, ", "))
	}
	for name, l := range defaults.Lighting.States {
		if _, ok := cfg.Lighting.States[name]; !ok {
			cfg.Lighting.States[name] = l
		}
	}
	if len(cfg.Layers) == 0 {
		cfg.Layers = defaults.Layers
	}
	return cfg, cfg.Validate()
}

var (
	gestureNames = []string{"press", "double", "hold", "down", "up"}
	agentActions = map[string]bool{"show": true, "show_zoom": true, "none": true}
	targets      = map[string]bool{"": true, "focused": true, "agent": true}
)

// Validate checks every setting and names the first bad one by its key path.
func (c *Config) Validate() error {
	switch c.Agents.Mode {
	case "auto", "sessions":
		if len(c.Agents.Slots) > 0 {
			return fmt.Errorf("agents.slots: only used with mode = \"slots\"")
		}
	case "slots":
		if len(c.Agents.Slots) == 0 || len(c.Agents.Slots) > 6 {
			return fmt.Errorf("agents.slots: give 1-6 pane labels or block IDs")
		}
	default:
		return fmt.Errorf("agents.mode: %q is not auto, sessions or slots", c.Agents.Mode)
	}
	if !agentActions[c.Agents.Press] {
		return fmt.Errorf("agents.press: %q is not show, show_zoom or none", c.Agents.Press)
	}
	if !agentActions[c.Agents.Double] {
		return fmt.Errorf("agents.double: %q is not show, show_zoom or none", c.Agents.Double)
	}
	if !unit(c.Lighting.Brightness) {
		return fmt.Errorf("lighting.brightness: must be 0-1")
	}
	if c.Lighting.Backlight != "layer" && c.Lighting.Backlight != "off" {
		return fmt.Errorf("lighting.backlight: %q is not layer or off", c.Lighting.Backlight)
	}
	for name, l := range c.Lighting.States {
		if !contains(States, name) {
			return fmt.Errorf("lighting.states.%s: unknown state (use %s)", name, strings.Join(States, ", "))
		}
		if _, err := l.Resolve(1); err != nil {
			return fmt.Errorf("lighting.states.%s: %w", name, err)
		}
	}
	if !(c.Stick.Threshold > 0 && c.Stick.Threshold < 1) {
		return fmt.Errorf("stick.threshold: must be between 0 and 1")
	}
	if !(c.Stick.AngleOffset >= -1 && c.Stick.AngleOffset <= 1) {
		return fmt.Errorf("stick.angle_offset: must be between -1 and 1 (a full turn)")
	}
	if c.Timing.DoublePress.Duration <= 0 || c.Timing.Hold.Duration <= 0 || c.Rex.Poll.Duration <= 0 {
		return fmt.Errorf("timing and rex.poll durations must be positive")
	}
	seen := map[string]bool{}
	for i, layer := range c.Layers {
		where := fmt.Sprintf("layer[%d]", i+1)
		if layer.Name == "" {
			return fmt.Errorf("%s: needs a name", where)
		}
		if seen[layer.Name] {
			return fmt.Errorf("%s: duplicate layer name %q", where, layer.Name)
		}
		seen[layer.Name] = true
		where = fmt.Sprintf("layer %q", layer.Name)
		if m := layer.Match; m != nil {
			switch {
			case i == 0:
				return fmt.Errorf("%s.match: the first layer is the base, used whenever no other layer matches; give match to a later layer", where)
			case m.App == "" && m.Program == "":
				return fmt.Errorf("%s.match: give app (e.g. \"Google Chrome\") and/or program (e.g. \"lazygit\")", where)
			case m.Program != "" && m.App != "" && AppName(m.App) != RexApp:
				return fmt.Errorf("%s.match: program only applies inside Rex; drop app or set app = \"Rex\"", where)
			}
		}
		if layer.Color != "" {
			if _, err := protocol.ParseColor(layer.Color); err != nil {
				return fmt.Errorf("%s.color: %w", where, err)
			}
		}
		names := make([]string, 0, len(layer.Keys))
		for k := range layer.Keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, key := range names {
			if !contains(KeyNames, key) {
				hint := ""
				if key == "ACT10" || key == "ACT11" {
					hint = " (the wide key is MIC)"
				}
				return fmt.Errorf("%s.keys.%s: unknown key%s; keys are %s", where, key, hint, strings.Join(KeyNames, ", "))
			}
			if err := layer.Keys[key].validate(seen, c.Layers); err != nil {
				return fmt.Errorf("%s.keys.%s: %w", where, key, err)
			}
			if err := layer.Keys[key].pressOnly(key); err != nil {
				return fmt.Errorf("%s.keys.%s: %w", where, key, err)
			}
		}
	}
	return nil
}

// PressOnly reports controls that fire once per move: dial turns and stick
// directions have no down, up, hold or double.
func PressOnly(key string) bool {
	return key == "ENC_CW" || key == "ENC_CC" || strings.HasPrefix(key, "STICK_")
}

func (k *KeyBinding) pressOnly(key string) error {
	if !PressOnly(key) {
		return nil
	}
	for _, g := range gestureNames {
		if g != "press" && k.Gesture(g) != nil {
			return fmt.Errorf("%s: a dial turn or stick direction only has press", g)
		}
	}
	return nil
}

func (k *KeyBinding) validate(_ map[string]bool, layers []Layer) error {
	if k == nil {
		return errors.New("empty binding")
	}
	direct := k.Action.Kind() != ""
	any := direct
	if direct && k.Press != nil {
		return errors.New("give either a single binding or press = {...}, not both")
	}
	for _, g := range gestureNames {
		a := k.Gesture(g)
		if a == nil {
			continue
		}
		any = true
		if err := a.validate(layers); err != nil {
			if g == "press" && direct {
				return err
			}
			return fmt.Errorf("%s: %w", g, err)
		}
	}
	if !any {
		return errors.New("binding does nothing; use { builtin = \"none\" } to disable a key")
	}
	return nil
}

func (a *Action) validate(layers []Layer) error {
	kind := a.Kind()
	switch {
	case kind == "":
		return errors.New("binding does nothing")
	case strings.Contains(kind, "+"):
		return fmt.Errorf("binding sets %s; set exactly one of action, key, shortcut, text, rex, shell, builtin", strings.ReplaceAll(kind, "+", " and "))
	}
	if len(a.Args) > 0 && kind != "action" {
		return errors.New("args only applies to action")
	}
	if !targets[a.Target] {
		return fmt.Errorf("target %q is not focused or agent", a.Target)
	}
	if a.Target != "" && kind != "key" {
		if kind == "text" {
			return errors.New("text always goes to the selected agent pane; drop target, or use key for the focused pane")
		}
		return errors.New("target only applies to key")
	}
	if a.Detach && kind != "shell" {
		return errors.New("detach only applies to shell")
	}
	switch kind {
	case "builtin":
		return validBuiltin(a.Builtin, layers)
	case "shortcut":
		_, err := macos.ParseShortcut(a.Shortcut)
		return err
	}
	return nil
}

func validBuiltin(b string, layers []Layer) error {
	switch b {
	case "none", "layer.next", "layer.prev", "agent.show", "agent.show_zoom", "agent.attention":
		return nil
	}
	if name, ok := strings.CutPrefix(b, "layer."); ok {
		for _, l := range layers {
			if l.Name == name {
				return nil
			}
		}
		return fmt.Errorf("builtin %q: no layer named %q", b, name)
	}
	if n, ok := strings.CutPrefix(b, "session."); ok && len(n) == 1 && n[0] >= '1' && n[0] <= '9' {
		return nil
	}
	return fmt.Errorf("unknown builtin %q (use none, layer.next, layer.prev, layer.<name>, session.<1-9>, agent.show, agent.show_zoom, agent.attention)", b)
}

// Resolve turns a config look into wire lighting, scaled by master brightness.
func (l Light) Resolve(master float64) (protocol.Light, error) {
	effect := l.Effect
	if effect == "" {
		effect = "solid"
	}
	e, ok := protocol.Effects[effect]
	if !ok {
		return protocol.Light{}, fmt.Errorf("unknown effect %q", effect)
	}
	out := protocol.Light{Effect: e, Speed: l.Speed, Brightness: 1}
	if l.Brightness != nil {
		out.Brightness = *l.Brightness
	}
	if !unit(out.Brightness) || !unit(l.Speed) {
		return protocol.Light{}, errors.New("brightness and speed must be 0-1")
	}
	out.Brightness *= master
	if e > protocol.Effects["solid"] && out.Speed == 0 {
		// Speed 0 freezes an animation, often at its dark point. ChatGPT
		// breathes its Agent Keys at 0.4, so that is the default.
		out.Speed = 0.4
	}
	if e == 0 {
		out.Brightness, out.Speed = 0, 0
	} else {
		if l.Color == "" {
			return protocol.Light{}, errors.New("needs a color")
		}
		c, err := protocol.ParseColor(l.Color)
		if err != nil {
			return protocol.Light{}, err
		}
		out.Color = c
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// unit reports whether v is in [0, 1]; NaN is not.
func unit(v float64) bool { return v >= 0 && v <= 1 }
