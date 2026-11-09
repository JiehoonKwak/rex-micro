# Agent Keys reach the right pane

**Covers:** `internal/daemon/`, `internal/engine/`, `internal/agents/`, `internal/rex/`, `internal/cli/cli.go`, `.agents/skills/verify-rex-micro/scripts/check_rex_micro.py` @ a4da753
**Tier:** script
**Positive control:** a live press of a key whose pane is in another session passes, and `judge-controls` accepts the recorded good results.
**Negative control:** `judge-controls` must reject a pane focused but its session not shown, the wrong pane focused, a same-ID session on another host, an Agent Key list that drops another host's agent (the v0.1.3 regression) and a key naming the wrong host.

## Sub-features

1. Pressing `AGnn` shows that key's pane: it becomes the focused block of its
   session's active window, and that session is the one on screen, whether
   it is the session already viewed or another one, on this Mac or a host.
2. Double press also zooms it (`agents.double`, default `show_zoom`).
3. The Agent Keys follow agents on this Mac and on every host in `rex hosts`,
   as the Rex sidebar does.

## How to get to it (user POV)

The user presses an Agent Key on the pad; the key's color shows its agent's
state. `rex-micro status` lists each key's host, session and pane.

## Driving it with the helper

1. `python3 -I $S agents`: Rex's own list of agent panes per host (from each
   terminal's `program_status`) against `status`'s `agent_keys`. Every key's
   pane must be an agent on the host it names; with `mode = "auto"` and six or
   fewer agents, every agent must have a key. Compare with the Rex sidebar if
   in doubt (`rex -S HOST ls`).
2. Pick from `rex-micro status --json`: one key whose `session_id` differs from
   `shown_pane.session` and one in the shown session but another pane. Run
   `python3 -I $S press KEY` for each. PASS means
   `rex [-S host] session inspect SESSION --json` has `active_window_id` equal
   to the key's `window` and that window's `focused_block_id` equal to its
   `block`, and (for another session or tab) this Mac's app sized the pane after the
   press, which is how Rex shows the app put it on screen.
3. Watch the Rex window during the press when you can; the helper restores the
   previous view afterwards.

## Gotchas

- `shown_pane` and `agent_keys` come from rex-micro; the verdict uses only
  Rex's answers. Do not judge a press by `rex-micro press` output alone.
- A key whose pane is already on screen proves nothing; the helper refuses it.
- In the tab already on screen the app does not resize panes it shows, so only
  focus is judged there; another tab of the shown session needs the size record.
- Several keys on one pane fail the `agents` check, as does an agent without a
  key while keys are free.
- Keys reorder with `order = "priority"`; the helper reads the target just
  before pressing.
- A host that does not answer is skipped by both rex-micro and the helper
  (reported as unreachable), so an all-hosts PASS needs every host reachable.
