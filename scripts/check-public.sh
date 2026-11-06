#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

fail=0
report() { echo "check-public: $1" >&2; fail=1; }

if files=$(git ls-files | grep -E '\.(p12|pem|key|env)$|(^|/)\.env$'); then
  report "secret-like files are tracked: $files"
fi

home_path='/(Users|home)/[A-Za-z0-9_][A-Za-z0-9_.-]*/'
private_key='-----BEGIN [A-Z ]*PRIVATE KEY-----'
email='[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[a-z]{2,}'
allowed_hits='noreply\.github\.com|git@github\.com'
patterns=("$home_path" "$private_key" "$email")
if [[ -f .public-deny ]]; then
  while IFS= read -r line || [[ -n $line ]]; do [[ -n $line ]] && patterns+=("$line"); done < .public-deny
fi

for p in "${patterns[@]}"; do
  if hits=$(git grep -noIE -e "$p" -- ':!scripts/check-public.sh' | grep -vE ":[0-9]+:[^:]*($allowed_hits)$"); then
    report "pattern '$p' matches:"$'\n'"$hits"
  fi
done

exit $fail
