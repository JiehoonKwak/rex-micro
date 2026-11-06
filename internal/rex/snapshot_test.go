package rex

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// luaRex runs the snapshot script the way `rex do -` would, against a fake
// server: two sessions whose focused panes run zsh and lazygit.
type luaRex struct{ t *testing.T }

var fakeServer = map[string]any{
	"client.list": map[string]any{"clients": []any{
		map[string]any{"client_id": "client:app", "info": map[string]any{"kind": "app", "label": "my-mac"}, "principal": map[string]any{"transport": "unix"}, "connected_at": "2026-11-06T00:00:00Z"},
		map[string]any{"client_id": "client:old", "info": map[string]any{"kind": "app", "label": "my-mac"}, "principal": map[string]any{"transport": "unix"}, "connected_at": "2026-11-05T00:00:00Z"},
		map[string]any{"client_id": "client:phone", "info": map[string]any{"kind": "app", "label": "phone"}, "principal": map[string]any{"transport": "tcp"}, "connected_at": "2026-11-06T00:00:09Z"},
	}},
	"session.list": map[string]any{"sessions": []any{
		map[string]any{"session_id": "sA", "label": "a"},
		map[string]any{"session_id": "sB", "label": "b"},
	}},
	"view:sA":     view("sA", "bA"),
	"view:sB":     view("sB", "bB"),
	"process:bA":  map[string]any{"foreground": map[string]any{"argv0": "-zsh", "invoked_path": "/bin/zsh", "name": "zsh"}},
	"process:bA2": map[string]any{"foreground": map[string]any{"argv0": "vim"}}, // not focused: never asked
	"process:bB":  map[string]any{"foreground": map[string]any{"argv0": "lazygit", "name": "lazygit"}},
	// The Rex app on this Mac shows A (sized last at :10). Another client
	// sized B later, which must not count.
	"size:bA": map[string]any{"desired": []any{map[string]any{"client_id": "client:app", "updated_at": "2026-11-06T00:00:10Z"}}},
	"size:bB": map[string]any{"desired": []any{
		map[string]any{"client_id": "client:phone", "updated_at": "2026-11-06T00:00:11Z"},
		map[string]any{"client_id": "client:app", "updated_at": "2026-11-06T00:00:05Z"},
	}},
	// Claude's own record says working while a subagent (a child record
	// without app) waits on the user: the pane must show blocked.
	"program_status:bB": map[string]any{"records": []any{
		map[string]any{"app": "claude-code", "state": "working"},
		map[string]any{"id": "agent-1", "state": "blocked"},
	}},
}

func view(sid, block string) map[string]any {
	return map[string]any{"active_window_id": "w" + sid, "windows": []any{map[string]any{
		"window_id": "w" + sid, "focused_block_id": block,
		"layers": []any{map[string]any{"blocks": []any{
			map[string]any{"block_id": block, "creator_name": "com.superlogical.terminal"},
			map[string]any{"block_id": block + "2", "creator_name": "com.superlogical.terminal"},
		}}},
	}}}
}

func toLua(L *lua.LState, v any) lua.LValue {
	switch x := v.(type) {
	case map[string]any:
		t := L.NewTable()
		for k, e := range x {
			t.RawSetString(k, toLua(L, e))
		}
		return t
	case []any:
		t := L.NewTable()
		for _, e := range x {
			t.Append(toLua(L, e))
		}
		return t
	case string:
		return lua.LString(x)
	}
	return lua.LNil
}

func fromLua(v lua.LValue) any {
	switch x := v.(type) {
	case *lua.LTable:
		if x.MaxN() > 0 {
			var out []any
			for i := 1; i <= x.MaxN(); i++ {
				out = append(out, fromLua(x.RawGetInt(i)))
			}
			return out
		}
		out := map[string]any{}
		x.ForEach(func(k, e lua.LValue) { out[k.String()] = fromLua(e) })
		return out
	case lua.LString:
		return string(x)
	case lua.LNumber:
		return float64(x)
	case lua.LBool:
		return bool(x)
	}
	return nil
}

func (f luaRex) Run(_ context.Context, stdin []byte, args ...string) ([]byte, error) {
	if strings.Join(args, " ") != "do -" {
		return nil, fmt.Errorf("unexpected rex %v", args)
	}
	r := runScript(f.t, stdin)
	return json.Marshal(r.result)
}

// scriptRun is what the snapshot script did against the fake server.
type scriptRun struct {
	result   any
	attached int
	events   []string // rex.on names
	handlers map[string]func()
	timers   []func() // rex.after and rex.every callbacks, in order
	printed  []string
}

func runScript(t *testing.T, script []byte) *scriptRun {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	run := &scriptRun{handlers: map[string]func(){}}
	get := func(key string) lua.LValue {
		if v, ok := fakeServer[key]; ok {
			return toLua(L, v)
		}
		return lua.LNil
	}
	rexT := L.NewTable()
	rexT.RawSetString("call", L.NewFunction(func(L *lua.LState) int {
		method := L.CheckString(1)
		switch method {
		case "session.view":
			L.Push(get("view:" + L.CheckTable(2).RawGetString("session_id").String()))
		case "session.attach":
			run.attached++
			L.Push(lua.LNil)
		default:
			L.Push(get(method))
		}
		return 1
	}))
	block := L.NewTable()
	block.RawSetString("call", L.NewFunction(func(L *lua.LState) int {
		L.Push(get(L.CheckString(2) + ":" + L.CheckTable(3).RawGetString("block_id").String()))
		return 1
	}))
	rexT.RawSetString("block", block)
	rexT.RawSetString("on", L.NewFunction(func(L *lua.LState) int {
		name, fn := L.CheckString(1), L.CheckFunction(2)
		run.events = append(run.events, name)
		run.handlers[name] = func() {
			if err := L.CallByParam(lua.P{Fn: fn, Protect: true}, L.NewTable(), L.NewTable()); err != nil {
				t.Fatalf("handler %s: %v", name, err)
			}
		}
		return 0
	}))
	timer := L.NewTable()
	timer.RawSetString("cancel", L.NewFunction(func(*lua.LState) int { return 0 }))
	addTimer := L.NewFunction(func(L *lua.LState) int {
		fn := L.CheckFunction(2)
		run.timers = append(run.timers, func() {
			if err := L.CallByParam(lua.P{Fn: fn, Protect: true}); err != nil {
				t.Fatalf("timer: %v", err)
			}
		})
		L.Push(timer)
		return 1
	})
	rexT.RawSetString("after", addTimer)
	rexT.RawSetString("every", addTimer)
	jsonT := L.NewTable()
	jsonT.RawSetString("encode", L.NewFunction(func(L *lua.LState) int {
		b, _ := json.Marshal(fromLua(L.CheckAny(1)))
		L.Push(lua.LString(b))
		return 1
	}))
	rexT.RawSetString("json", jsonT)
	L.SetGlobal("rex", rexT)
	stdout := L.NewTable()
	stdout.RawSetString("write", L.NewFunction(func(L *lua.LState) int {
		for i := 2; i <= L.GetTop(); i++ {
			run.printed = append(run.printed, L.CheckString(i))
		}
		return 0
	}))
	stdout.RawSetString("flush", L.NewFunction(func(*lua.LState) int { return 0 }))
	ioT := L.NewTable()
	ioT.RawSetString("stdout", stdout)
	L.SetGlobal("io", ioT)
	if err := L.DoString(string(script)); err != nil {
		t.Fatalf("snapshot.lua: %v", err)
	}
	run.result = fromLua(L.Get(-1))
	return run
}

func TestWatchScriptStaysAttachedAndPrintsOnChange(t *testing.T) {
	one := runScript(t, (&Client{}).script(0))
	if one.attached == 0 {
		t.Fatal("a one-shot snapshot must attach to read sessions")
	}
	c := &Client{}
	w := runScript(t, c.script(10*time.Second))
	for _, want := range []string{"session_view_changed", "terminal.program_status_changed", "terminal.size_changed", "terminal.process_changed", "session_moved"} {
		if !slices.Contains(w.events, want) {
			t.Fatalf("watcher does not listen for %s: %v", want, w.events)
		}
	}
	if slices.Contains(w.events, "terminal.title_changed") {
		t.Fatal("watcher rebuilds on every spinner frame")
	}
	for _, fn := range w.timers {
		fn() // the initial snapshot and two fallback ticks, nothing changed
		fn()
	}
	if w.attached != 0 {
		t.Fatalf("the watcher attached %d times; rex do --all already hears every session", w.attached)
	}
	if len(w.printed) != 2 || w.printed[1] != "\n" {
		t.Fatalf("printed %q, want one snapshot line", w.printed)
	}
	panes, err := parsePanes([]byte(w.printed[0]))
	if err != nil || len(panes) != 4 {
		t.Fatalf("panes %v err %v", panes, err)
	}
	// Sessions reordered: the event's handler must lead to a new snapshot.
	list := fakeServer["session.list"]
	defer func() { fakeServer["session.list"] = list }()
	fakeServer["session.list"] = map[string]any{"sessions": []any{
		map[string]any{"session_id": "sB", "label": "b"},
		map[string]any{"session_id": "sA", "label": "a"},
	}}
	before := len(w.timers)
	w.handlers["session_moved"]()
	if len(w.timers) != before+1 {
		t.Fatal("session_moved started no refresh")
	}
	w.timers[before]()
	if len(w.printed) != 4 {
		t.Fatalf("no snapshot after session_moved: %q", w.printed)
	}
	if panes, _ = parsePanes([]byte(w.printed[2])); panes[0].Session != "sB" || panes[0].SessionIndex != 1 {
		t.Fatalf("after move: %+v", panes[0])
	}
}

func TestSnapshotScriptFindsShownPaneAndPrograms(t *testing.T) {
	for _, app := range []string{"", "client:app", "my-mac", "phone-less-label"} {
		c := &Client{Runner: luaRex{t}, App: app}
		panes, err := c.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if cur := Current(panes); app != "phone-less-label" && (cur == nil || cur.Block != "bA") {
			t.Fatalf("app %q: current = %+v", app, cur)
		}
	}
	// A configured label names the app on this Mac; the phone's later
	// sizing of B still must not count.
	c := &Client{Runner: luaRex{t}, App: "phone"}
	if p, _ := c.Snapshot(); Current(p) == nil || Current(p).Block != "bB" {
		t.Fatalf("label phone: current = %+v", Current(p))
	}
	c = &Client{Runner: luaRex{t}}
	panes, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 4 {
		t.Fatalf("panes = %+v", panes)
	}
	cur := Current(panes)
	if cur == nil || cur.Block != "bA" || cur.Program != "zsh" {
		t.Fatalf("current = %+v", cur)
	}
	for _, p := range panes {
		if p.Block == "bB" && (p.Program != "lazygit" || p.App != "claude-code" || p.State != "blocked" || p.SessionIndex != 2) {
			t.Fatalf("bB = %+v", p)
		}
		if strings.HasSuffix(p.Block, "2") && (p.Focused || p.Program != "") {
			t.Fatalf("unfocused pane reports a program: %+v", p)
		}
	}
}
