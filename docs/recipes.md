# Recipes

Shell bindings that act on the selected agent: the pane the last Agent Key
showed, or else the most urgent agent. Each recipe goes in a layer's
`[layer.keys]`, and `rex-micro press KEY` tries it without the pad.

A shell binding gets these environment variables:

| Variable | Value |
| --- | --- |
| `REX_MICRO_REX` | the `rex` the daemon runs, which matches the Rex server (launchd's `PATH` has no `rex`) |
| `REX_MICRO_KEY`, `REX_MICRO_GESTURE`, `REX_MICRO_LAYER` | the key (`ACT09`), gesture (`press`, `hold`, ...) and active layer |
| `REX_MICRO_AGENT_BLOCK`, `_SESSION`, `_WINDOW` | where the selected agent's pane is |
| `REX_MICRO_AGENT_HOST` | its Rex host label, empty on this Mac (pass it to `rex -S`) |
| `REX_MICRO_AGENT_APP`, `_STATE`, `_KIND`, `_MSG` | e.g. `claude-code`, `blocked`, `permission`, and its status line |
| `REX_MICRO_AGENT_LABEL`, `_SESSION_LABEL` | the pane's and session's labels |

The values are taken when the key goes down, before any wait for a double
press or a hold. With no agent pane, the
`REX_MICRO_AGENT_*` variables are unset, so `${REX_MICRO_AGENT_BLOCK:?}` stops
the command. `_MSG` and the labels are text that programs wrote: quote them,
and never pass them to `eval`.

`${REX_MICRO_AGENT_HOST:+-S "$REX_MICRO_AGENT_HOST"}` adds `-S HOST` only for
an agent on another host. The recipes use it so that they work on every host.

## Open the dev server the agent started

`rex forward open` finds the port that a program in the pane is listening on.
For an agent on another host, it forwards that port to this Mac. It then opens
the URL in your browser. The forward runs until you stop it, so the binding
detaches:

```toml
ACT09 = { shell = '"$REX_MICRO_REX" ${REX_MICRO_AGENT_HOST:+-S "$REX_MICRO_AGENT_HOST"} forward open "${REX_MICRO_AGENT_BLOCK:?no agent}"', detach = true, label = "Open agent's server" }
```

When the pane listens on several ports, `rex forward open` lists them and
opens none. `rex [-S HOST] forward ls -s SESSION` shows the forwards of the
agent's session, and `rex [-S HOST] forward close -s SESSION ID` stops one.

## Copy the agent's screen

This copies the agent's screen and scrollback to the clipboard, ready to paste
into another agent or a chat:

```toml
ACT08 = { shell = 'out=$("$REX_MICRO_REX" ${REX_MICRO_AGENT_HOST:+-S "$REX_MICRO_AGENT_HOST"} capture -s "${REX_MICRO_AGENT_SESSION:?no agent}" -b "$REX_MICRO_AGENT_BLOCK" --trim --unwrap) && printf %s "$out" | pbcopy', label = "Copy agent screen" }
```

## Enter, only while the agent waits for a permission

The default APPROVE key sends Enter to the focused pane, whatever it is
doing. This one sends Enter to the selected agent, and only if that agent was
waiting for a permission when you pressed the key. A question or a working
agent gets nothing. Rex has no way to approve one particular prompt, so this
is still a plain Enter: press it twice quickly and the second Enter can land
on whatever the agent asks next.

```toml
ACT07 = { shell = '[ "$REX_MICRO_AGENT_KIND" = permission ] && exec "$REX_MICRO_REX" ${REX_MICRO_AGENT_HOST:+-S "$REX_MICRO_AGENT_HOST"} send-key -s "$REX_MICRO_AGENT_SESSION" -b "$REX_MICRO_AGENT_BLOCK" enter; exit 0', label = "Enter if permission" }
```

## Rex palette prompts

`rex.ui.pick` and `rex.ui.input` (Rex Mac preview.32) ask in the Rex command
palette, but only inside an action defined in `~/.config/rex/init.lua`; a
`rex do` script gets `unsupported`. Write the action there and bind it with
`{ action = "name" }`.
