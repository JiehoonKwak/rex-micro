# Releasing

Push a `v*` tag; `.github/workflows/release.yml` does the rest:

1. `test` runs the tests with a read-only token and no secrets.
2. `release` runs in the `release` environment, which only `v*` tags may use
   and which holds the secrets. It builds and signs the universal binary
   (`scripts/build-release.sh`), attests the tarball and binary
   (`actions/attest`), creates the GitHub release as a draft with every file,
   publishes it, then updates the Homebrew formula (`scripts/update-tap.sh`).

Releases are immutable: a published tag and its files cannot change. A
mistake is fixed by the next patch version, never by re-tagging.

Secrets in the `release` environment:

- `MACOS_CERT_P12`, `MACOS_CERT_PASSWORD`: the self-signed signing identity
  (base64 `.p12` and its password) from `scripts/make-signing-cert.sh`. macOS
  ties users' Input Monitoring and Accessibility grants to it, so a new
  certificate makes every user grant them again: make it once and keep a
  copy outside GitHub.
- `TAP_DEPLOY_KEY`: private half of a write deploy key on
  `JiehoonKwak/homebrew-tap`. Losing it only means adding a new deploy key.

Workflow dependencies are pinned in `.github/workflows/actions.lock`; after
changing a `uses:` line run `gh actions-lock` (CI fails while the lockfile is
stale). Dependabot updates actions and Go modules weekly after a 7-day
cooldown.
