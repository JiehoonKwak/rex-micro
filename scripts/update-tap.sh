#!/bin/sh
set -eu
version=${1:?usage: update-tap.sh VERSION}
root=$(cd "$(dirname "$0")/.." && pwd)
sha=$(awk '{print $1}' "$root/dist/checksums.txt")
tmp=$(mktemp -d)
git clone --depth 1 "https://x-access-token:${TAP_TOKEN:?}@github.com/JiehoonKwak/homebrew-tap.git" "$tmp/tap"
mkdir -p "$tmp/tap/Formula"
sed -e "s/@VERSION@/$version/g" -e "s/@SHA256@/$sha/g" "$root/packaging/homebrew/rex-micro.rb" > "$tmp/tap/Formula/rex-micro.rb"
cd "$tmp/tap"
git add Formula/rex-micro.rb
git -c user.name="rex-micro release" -c user.email="actions@users.noreply.github.com" commit -m "rex-micro $version"
git push
