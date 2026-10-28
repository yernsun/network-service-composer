#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  echo "usage: $0 POOL_ID GATEWAY_ID" >&2
  exit 2
fi
pool_id=$1
gateway_id=$2
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
controller_bin=${NSC_CONTROLLER_BIN:-"$repo_dir/bin/nsc-controller"}
pki_dir=${NSC_PKI_DIR:-"$repo_dir/pki"}
secret_dir=${NSC_AGENT_SECRET_DIR:-"$repo_dir/deploy/secrets/gateway"}

if [ ! -x "$controller_bin" ]; then
  echo "missing $controller_bin; run 'make build' first" >&2
  exit 1
fi
for target in "$secret_dir/agent.key" "$secret_dir/agent.crt"; do
  if [ -e "$target" ]; then
    echo "refusing to overwrite $target" >&2
    exit 1
  fi
done

umask 077
mkdir -p "$secret_dir"
"$controller_bin" pki issue-agent \
  --ca-cert "$pki_dir/ca.crt" --ca-key "$pki_dir/ca.key" \
  --cert "$secret_dir/agent.crt" --key "$secret_dir/agent.key" \
  --pool-id "$pool_id" --gateway-id "$gateway_id"
if [ -e "$secret_dir/ca.crt" ]; then
  if ! cmp -s "$pki_dir/ca.crt" "$secret_dir/ca.crt"; then
    echo "refusing to replace $secret_dir/ca.crt with a different CA" >&2
    exit 1
  fi
else
  cp "$pki_dir/ca.crt" "$secret_dir/ca.crt"
fi
chmod 0750 "$secret_dir"
chmod 0440 "$secret_dir/agent.key" "$secret_dir/agent.crt" "$secret_dir/ca.crt"

echo "issued agent identity for gateway $gateway_id in pool $pool_id"
echo "set NSC_POOL_ID and NSC_GATEWAY_ID in .env before starting the gateway"
