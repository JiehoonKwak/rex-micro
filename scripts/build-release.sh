#!/bin/sh
set -eu
version=${1:?usage: build-release.sh VERSION}
id=io.github.jiehoonkwak.rex-micro
root=$(cd "$(dirname "$0")/.." && pwd)
dist=$root/dist
rm -rf "$dist" && mkdir -p "$dist/stage"
ldflags="-s -w -X github.com/JiehoonKwak/rex-micro/internal/cli.Version=$version"
for arch in arm64 amd64; do
  CGO_ENABLED=1 GOOS=darwin GOARCH=$arch CGO_CFLAGS="-mmacosx-version-min=13.0" CGO_LDFLAGS="-mmacosx-version-min=13.0" \
    go build -C "$root" -trimpath -ldflags "$ldflags" -o "$dist/rex-micro-$arch" ./cmd/rex-micro
done
lipo -create -output "$dist/stage/rex-micro" "$dist/rex-micro-arm64" "$dist/rex-micro-amd64"

if [ -n "${MACOS_CERT_P12:-}" ]; then
  kc=$dist/signing.keychain-db
  pass=$(openssl rand -hex 16)
  printf '%s' "$MACOS_CERT_P12" | base64 --decode > "$dist/id.p12"
  security create-keychain -p "$pass" "$kc"
  security set-keychain-settings "$kc"
  security unlock-keychain -p "$pass" "$kc"
  security import "$dist/id.p12" -k "$kc" -P "${MACOS_CERT_PASSWORD:-}" -T /usr/bin/codesign
  security set-key-partition-list -S apple-tool:,apple: -s -k "$pass" "$kc" >/dev/null
  security list-keychains -d user > "$dist/keychains"
  xargs security list-keychains -d user -s "$kc" < "$dist/keychains"
  identity=$(security find-certificate -a -Z "$kc" | awk '/SHA-1 hash:/ {print $NF; exit}')
  codesign --force --sign "$identity" --keychain "$kc" --identifier "$id" --options runtime "$dist/stage/rex-micro"
  xargs security list-keychains -d user -s < "$dist/keychains"
  security delete-keychain "$kc"
  rm -f "$dist/id.p12"
else
  echo "warning: MACOS_CERT_P12 not set; ad-hoc signing (Input Monitoring may reset after updates)" >&2
  codesign --force --sign - --identifier "$id" "$dist/stage/rex-micro"
fi
codesign -dvv "$dist/stage/rex-micro" 2>&1 | grep -E '^(Identifier|Authority|Signature)'

cp "$root/LICENSE" "$root/README.md" "$dist/stage/"
tarball=rex-micro_${version}_darwin_universal.tar.gz
tar -C "$dist/stage" -czf "$dist/$tarball" .
(cd "$dist" && shasum -a 256 "$tarball" > checksums.txt && cat checksums.txt)
