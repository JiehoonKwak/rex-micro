# AGENTS.md

rex-micro drives the Rex terminal from a Work Louder Micro pad without the
Work Louder Input app. It is a public showcase for Rex, so behaviour belongs in
`config.toml`, not in code paths tuned to one machine.

## Layout

- `cmd/rex-micro`: entry point; `internal/cli` holds every subcommand.
- `internal/daemon`: the long-running loop (pad events, Rex polling, layers,
  lights, effects queue). launchd's KeepAlive restarts it whenever it returns,
  which is how upgrades and an Accessibility grant take effect.
- `internal/engine`: gestures and bindings; `internal/config`: TOML schema,
  with `default.toml` as the documented default.
- `internal/hid`, `internal/micro`, `internal/protocol`: the pad's USB/BLE
  transport and its JSON-lines protocol.
- `internal/macos`: front app, shortcut posting, Accessibility checks.
- `internal/editor`, `internal/tomledit`: `rex-micro edit`, which rewrites only
  the lines it changes and keeps comments.
- `skills/rex-micro/SKILL.md`: the agent skill shipped with the CLI.

## Working rules

- Build and test: `go build ./... && go test ./...` (cgo, macOS only).
- A new option goes in `internal/config/default.toml` with a comment, and in
  README's Configuration section.
- Tests must catch a planted fault: a test that still passes with the behaviour
  broken does not count.
- macOS permissions follow the signing identity. Release builds are signed by
  `scripts/build-release.sh` with the self-signed identity from
  `scripts/make-signing-cert.sh` (see `docs/releasing.md`); never commit the
  `.p12` or its password.
- README and docs are English, short and plain.

