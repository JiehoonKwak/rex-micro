package rex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

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
	"program_status:bB": map[string]any{"records": []any{map[string]any{"app": "claude-code", "state": "working"}}},
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
	switch strings.Join(args, " ") {
	case "do -":
	default:
		return nil, fmt.Errorf("unexpected rex %v", args)
	}
	L := lua.NewState()
	defer L.Close()
	get := func(key string) lua.LValue {
		if v, ok := fakeServer[key]; ok {
			return toLua(L, v)
		}
		return lua.LNil
	}
	rexT := L.NewTable()
	rexT.RawSetString("call", L.NewFunction(func(L *lua.LState) int {
		method := L.CheckString(1)
		if method == "session.view" {
			L.Push(get("view:" + L.CheckTable(2).RawGetString("session_id").String()))
		} else {
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
	L.SetGlobal("rex", rexT)
	if err := L.DoString(string(stdin)); err != nil {
		f.t.Fatalf("snapshot.lua: %v", err)
	}
	return json.Marshal(fromLua(L.Get(-1)))
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
		if p.Block == "bB" && (p.Program != "lazygit" || p.App != "claude-code" || p.State != "working" || p.SessionIndex != 2) {
			t.Fatalf("bB = %+v", p)
		}
		if strings.HasSuffix(p.Block, "2") && (p.Focused || p.Program != "") {
			t.Fatalf("unfocused pane reports a program: %+v", p)
		}
	}
}
