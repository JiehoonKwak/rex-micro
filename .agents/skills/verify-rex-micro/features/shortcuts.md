# Shortcuts fire with Rex in front

**Covers:** `internal/macos/`, `internal/daemon/effects.go`, `internal/engine/engine.go` @ unconfirmed
**Tier:** script
**Positive control:** with Rex in front, pressing a key bound to Rex's next-tab shortcut changes the shown session's `active_window_id`, and the previous-tab key brings it back.
**Negative control:** with `front_app` not Rex the same press must not change Rex's tab (the shortcut goes to the app in front); if Accessibility is denied, `rex-micro press` logs the failure and the tab stays.

## Sub-features

A `shortcut` binding posts its key combination to the app in front, but only
while that app (and Rex program, for a `match = { program }` layer) is still in
front when the action runs. It needs Accessibility for the daemon's executable.

## How to get to it (user POV)

The user presses a pad key while Rex is the active app.

## Driving it

1. `rex-micro status --json`: `front_app` must be Rex (the agent's own Rex
   pane usually is) and `accessibility` granted. If another app is in front,
   ask the user once, before delivering, whether you may bring Rex to the
   front for about 30 seconds, then press and restore their app. Without that
   yes, record a skip and report the change unverified; do not ship another
   attempt judged only from the CLI (2026-11-09: two joystick fixes failed on
   the pad this way).
2. From `rex-micro keys`, pick a shortcut binding that is reversible and
   observable in Rex, such as next/previous tab (`cmd+shift+]` / `cmd+shift+[`)
   in a shown session with two or more tabs. Never a close, Enter or Escape.
3. Record `rex session inspect SESSION --json` `.active_window_id` for
   `shown_pane.session`, `rex-micro press KEY`, read it again: it must have
   changed. Press the reverse key and confirm it is back.

## Gotchas

- `rex-micro press` lists a skipped shortcut in its `log:` lines ("was for X,
  but Y is in front now; skipped"); that is a FAIL of the setup, not the code.
- `key` bindings go to the Rex pane through the CLI and need no Accessibility;
  do not use them to judge shortcut posting.
