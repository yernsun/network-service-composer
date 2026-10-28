#!/bin/sh
# Real replication, owner crash/restart and DB-only partition acceptance.
# Only invocation-owned resources and a nested Docker engine are used.
set -eu
if [ "$#" -ne 1 ] || { [ "$1" != postgres ] && [ "$1" != mysql ]; }; then
  printf 'usage: %s <postgres|mysql>\n' "$0" >&2
  exit 2
fi
repository=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
version=$(sed -n '1p' "$repository/VERSION")
exec python3 "$repository/scripts/e2e-ha-failover.py" "$1" \
  --image "${NSC_CONTROLLER_IMAGE:-nsc-controller:v$version}" \
  --output "$repository/coverage/p0/r04/$1-$$.json"
