#!/bin/sh
set -eu
version=${1:?usage: update-tap.sh VERSION}
if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "rex-micro $version is a prerelease; the tap keeps the last stable release"
  exit 0
fi
root=$(cd "$(dirname "$0")/.." && pwd)
sha=$(awk '{print $1}' "$root/dist/checksums.txt")
tmp=$(mktemp -d)
key=$tmp/deploy_key
printf '%s\n' "${TAP_DEPLOY_KEY:?}" > "$key"
chmod 600 "$key"
export GIT_SSH_COMMAND="ssh -i $key -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new"
git clone --depth 1 git@github.com:JiehoonKwak/homebrew-tap.git "$tmp/tap"
mkdir -p "$tmp/tap/Formula"
current=$(sed -n 's/^  version "\(.*\)"$/\1/p' "$tmp/tap/Formula/rex-micro.rb" 2>/dev/null || true)
if [ -n "$current" ] &&
  [ "$(printf '%s\n%s\n' "$current" "$version" | sort -V | tail -n 1)" = "$current" ]; then
  echo "tap already has rex-micro $current; not writing $version"
  exit 0
fi
sed -e "s/@VERSION@/$version/g" -e "s/@SHA256@/$sha/g" "$root/packaging/homebrew/rex-micro.rb" > "$tmp/tap/Formula/rex-micro.rb"
cd "$tmp/tap"
git add Formula/rex-micro.rb
git -c user.name="rex-micro release" -c user.email="actions@users.noreply.github.com" commit -m "rex-micro $version"
git push
