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

`rex-micro config default` prints the default config: every option, binding
type, gesture, layer `match` and Agent Key mode, with examples. Read it before
writing a binding you have not used. `rex-micro actions` lists the Rex actions;
actions without a dot are the user's own `rex.action` definitions in
`~/.config/rex/init.lua`, where a complex behaviour belongs, bound with
`{ action = "name" }`. For a layer `match`, `rex-micro status --json` shows the
exact `front_app` and `program` names.

People can also edit bindings with `rex-micro edit`, a full-screen editor;
suggest it when they want to try things by pressing keys.

## Agent Keys and layers

- The Agent Keys follow agents on this Mac and on every host in `rex hosts`,
  as the Rex sidebar does. `[agents] order` picks how they are ordered:
  `stable` (a key keeps its agent), `priority` (the sidebar's Activity by
  Priority) or `session`. `rex-micro status` shows each key's host.
- `[pad] layers = "sync"` stores the config's layers (at most six) on the pad:
  the touch sensor switches them and the pad's LEDs show the active one.
  Setting `"off"` writes the pad's previous keymap back. It replaces what Work
  Louder Input put on the pad, so confirm with the user before turning it on,
  and have them quit Input.
- Keep Agent Keys on agents in every layer (`[agents] always = true`) unless the
  user wants otherwise; layers that turn on by `match = { program = ... }`
  need no switching at all.

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
   keycode is still the factory one.
5. A `shortcut` binding does nothing: Accessibility is missing for the
   daemon's executable. `doctor` reports `daemon shortcuts`; allow
   rex-micro under System Settings > Privacy & Security > Accessibility.
6. A layer does not turn on: compare `front_app`/`program` in
   `rex-micro status --json` with the layer's `match`. With pad layers on,
   `pad_layers` in the same output says `sync` or the error that stopped it.
7. `rex-micro monitor` prints raw pad events while the daemon is paused.
8. Logs: `~/Library/Logs/rex-micro.log`.
