---
name: verify-rex-micro
description: Verify rex-micro changes in the real Rex app without the physical pad - Agent Key presses focus the right pane in the right session, the Agent Keys cover agents on every Rex host, and shortcut bindings fire with Rex in front. Use before reporting any rex-micro change to keys, Agent Keys, layers, bindings or Rex following as done.
---

# Verify rex-micro

Judge a change by what Rex does, never by rex-micro's own report alone and
never by asking the user to press the pad. `rex-micro press KEY [--double|--hold]`
makes the running daemon handle a key exactly as a pad press (same engine,
active layer and Agent Key resolution) and returns after its actions ran; the
checks below read Rex itself to see whether the press did the right thing.

## Launch and doctor

1. The daemon under test must be the build you changed. A release reaches the
   service through `brew upgrade rex-micro` and its self-update within 30 s:
   `rex-micro status --json` `.executable` is the service copy, and `doctor`
   shows the new version. Ask the user before putting an unreleased build in
   the service's place; prefer releasing.
2. `rex-micro doctor` passes, `status` shows the pad connected, not paused,
   and `rex hosts` lists the hosts the Agent Keys should cover.
3. The Rex CLI guide: `rex __internal skill --feature-session-naming=false`.

## Drive and judge

Read [features/README.md](features/README.md) for which recipe a change needs.
The helper prints PASS/FAIL with the observations behind it, exits 0/1, and
exits 2 when the setup (not the change) is the problem:

```sh
S=.agents/skills/verify-rex-micro/scripts/check_rex_micro.py
python3 -I $S judge-controls          # the judges still tell good from bad
python3 -I $S agents                  # Agent Keys vs agent panes Rex lists on every host
python3 -I $S press AG03              # press, then check focus in Rex; restores the view
```

Safety while driving the user's real Rex app:

- Press only keys whose effect you mean. Bindings that type Enter or Escape
  into a pane, or close a pane or tab, act on real agents: never press them
  as a test. `rex-micro keys` shows what each key does on the active layer.
- `press` restores the pane shown before (`--keep` skips that). For any
  other key, put the view back yourself (`rex focus` plus
  `rex -C <app label> do session.select session_id=... window_id=...`).
- Never restart the Rex server, the app or terminals to make a check pass.

## Evidence and cleanup

Keep the helper's output in the report or the verification record:
`python3 ~/.agents/scripts/agent_lint verify record <recipe> --result pass|fail|skip --note "<command and result>"`.
The checks leave nothing behind but the restored view.
