---
name: rex-micro
description: Configure and troubleshoot rex-micro, which turns a Work Louder Codex Micro macro pad into a controller for the Rex terminal. Use when the user wants to change what a pad key, dial, stick or layer does, add a layer, change Agent Key colors or modes, bind push-to-talk, or fix the pad not responding.
---

# rex-micro

rex-micro is a background daemon plus CLI. The daemon reads the pad, lights
the six Agent Keys from the agent state Rex reports for each pane, and turns
presses into Rex actions. Everything the user can change lives in one TOML
file; the daemon reloads it on save.

## Find the current state first

```sh
rex-micro config path      # the config file (absent = built-in default)
rex-micro keys             # what every key does, per layer, after fall-through
rex-micro status --json    # pad, active layer, which pane each Agent Key follows
rex-micro doctor           # Rex, pad, permission and config checks
```

If the config file does not exist, create it with `rex-micro config init`
before editing. Never overwrite an existing config wholesale; edit the keys
the user asked about.

## Change a binding

Key names: `AG00`-`AG05` (Agent Keys), `ACT06`-`ACT09`, `MIC` (the wide key,
both of its switches), `ACT12`, `ENC_CLK` (dial press), `ENC_CW`/`ENC_CC`
(dial turns), `STICK_UP`/`STICK_DOWN`/`STICK_LEFT`/`STICK_RIGHT`. The caps are
swappable, so ask which position the user means when a cap name is ambiguous;
factory caps are FAST, APPROVE, REJECT, SPLIT on `ACT06`-`ACT09`, MIC, and
CODEX on `ACT12`.

A binding does exactly one thing:

| Binding | Effect |
| --- | --- |
| `{ action = "pane.zoom" }` | a Rex action; add `args = { direction = "right" }` when it takes arguments |
| `{ key = "enter" }`, `{ key = ["ctrl+c"] }` | key presses to the pane focused in the Rex app |
| `{ key = "y", target = "agent" }` | key presses to the selected agent pane (last one shown with an Agent Key, else the most urgent) |
| `{ shortcut = "cmd+shift+]" }` | a keyboard shortcut in the app in front (names the key, so `]` with shift); a lone modifier such as `fn` or `right_option` taps that key alone; needs Accessibility |
| `{ text = "continue" }` | types text into the selected agent pane |
| `{ rex = ["run", "--", "lazygit"] }` | any `rex` CLI arguments |
| `{ shell = "open -a Typeless" }` | a shell command; effects run in order, so it waits up to 10s; add `detach = true` for long ones |
| `{ builtin = "layer.next" }` | `layer.next`, `layer.prev`, `layer.<name>`, `session.<1-9>`, `agent.show`, `agent.show_zoom`, `agent.attention`, `none` |

List the Rex actions a binding can use with `rex-micro actions`.
Actions without a dot are the user's own `rex.action` definitions in
`~/.config/rex/init.lua`; a complex behaviour belongs there, bound with
`{ action = "name" }`.

Gestures replace the single binding with one table per gesture:

```toml
ACT06 = { press = { action = "pane.zoom" }, double = { action = "pane.balance" }, hold = { action = "pane.close" } }
MIC = { down = { shell = "..." }, up = { shell = "..." } }   # push-to-talk
```

A press waits `timing.double_press` only when the key also has `double`.

## Layers

`[[layer]]` tables, in order; the first is the base, and keys a later layer
leaves out fall through to it. `ENC_CLK` cycles layers by default, and the
backlight shows the active layer's `color`.

A later layer can turn on by itself: `match = { app = "Google Chrome" }`
(the app in front, as macOS names it; `"Rex"` covers every Rex edition) or
`match = { program = "lazygit" }` (the program in the Rex pane on screen;
beats an app match). `rex-micro status --json` shows `front_app` and
`program`, which give the exact names to match. The first layer cannot have a
match. A layer chosen with the dial stays until the app or program calls for
another layer.

```toml
[[layer]]
name = "tools"
color = "#06B6D4"

[layer.keys]
ACT06 = { rex = ["run", "--", "lazygit"] }
ACT07 = { action = "hunk" }
```

## Agent Keys

`[agents] mode`: `auto` (agent panes in session order, stable across
refreshes), `sessions` (sessions 1-6, most urgent agent in each), or `slots`
(`slots = ["label-or-block-id", ...]`). `press`/`double` choose `show`,
`show_zoom` or `none`; binding `AG0n` in a layer overrides them, and the key
then lights in that layer's color. `always = true` keeps all six on their
agents in every layer. Colors live in `[lighting.states]` for `working`,
`blocked`, `error`, `done`, `idle`, `empty`.

People can also edit bindings with `rex-micro edit`, a full-screen editor;
suggest it when they want to try things by pressing keys.

## After every edit

Run `rex-micro config check`. It names the bad setting (for example
`layer "tools".keys.ACT10: unknown key (the wide key is MIC)`); fix it before
finishing. Then confirm with `rex-micro keys`, and ask the user to press the key
when the change matters.

## When the pad does nothing

1. `rex-micro doctor`. Every `FAIL` line says what to do.
2. Input Monitoring is granted per executable path and signature. The service
   must run from `~/Library/Application Support/rex-micro/bin/rex-micro`
   (`rex-micro service install` sets that up); ad-hoc signed builds lose the
   grant whenever the binary changes.
3. Another app driving the pad repaints the lights and acts on presses. Quit
   Work Louder Input. For ChatGPT, switch it off under System Settings >
   Privacy & Security > Input Monitoring and restart it; it then leaves the
   pad alone. The daemon
   notices such writes, and `doctor` then reports `other app`.
4. A key that types a keystroke instead of reaching rex-micro has been
   remapped in the pad's own keymap (Work Louder Input edits it). `rex-micro
   test` marks such keys `device`. rex-micro only sees keys whose Layer 1
   keycode is still the factory `KV_OAI_*` value.
5. A `shortcut` binding does nothing: Accessibility is missing for the
   daemon's executable. `doctor` reports `daemon shortcuts`; allow
   rex-micro under System Settings > Privacy & Security > Accessibility.
6. A layer does not turn on: compare `front_app`/`program` in
   `rex-micro status --json` with the layer's `match`.
7. `rex-micro monitor` prints raw pad events while the daemon is paused.
8. Logs: `~/Library/Logs/rex-micro.log`.
