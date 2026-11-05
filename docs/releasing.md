# Releasing

- `scripts/make-signing-cert.sh [OUT.p12]`: one-time maintainer step that
  creates the self-signed signing identity. Store the `.p12` (base64) and its
  password as the `MACOS_CERT_P12` and `MACOS_CERT_PASSWORD` repository
  secrets. A new certificate makes every user grant the permissions again, so
  make it once and reuse it.
- `scripts/build-release.sh VERSION`: builds the universal binary into
  `dist/`, signed with that identity when the secrets are set, ad-hoc
  otherwise.
- `scripts/update-tap.sh VERSION`: renders `packaging/homebrew/rex-micro.rb`
  from `dist/checksums.txt` and pushes it to `JiehoonKwak/homebrew-tap`; needs
  `TAP_TOKEN`.
- Pushing a `v*` tag runs all of this in `.github/workflows/release.yml`.
