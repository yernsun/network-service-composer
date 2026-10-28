#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
controller_bin=${NSC_CONTROLLER_BIN:-"$repo_dir/bin/nsc-controller"}
pki_dir=${NSC_PKI_DIR:-"$repo_dir/pki"}
controller_secret_dir="$repo_dir/deploy/secrets/controller"
gateway_secret_dir="$repo_dir/deploy/secrets/gateway"
environment_file="$repo_dir/.env"

if [ ! -x "$controller_bin" ]; then
  echo "missing $controller_bin; run 'make build' first" >&2
  exit 1
fi
for target in "$pki_dir/ca.key" "$pki_dir/ca.crt" "$controller_secret_dir/controller.key" "$controller_secret_dir/controller.crt" "$environment_file"; do
  if [ -e "$target" ]; then
    echo "refusing to overwrite $target" >&2
    exit 1
  fi
done

umask 077
mkdir -p "$pki_dir" "$controller_secret_dir" "$gateway_secret_dir"
admin_username=${NSC_ADMIN_USERNAME:-admin}
admin_token=${NSC_ADMIN_TOKEN:-$(openssl rand -hex 32)}
session_key=$(openssl rand -hex 32)
encryption_key=$(openssl rand 32 | openssl base64 -A)
database_password=$(openssl rand -hex 24)
secret_gid=$(id -g)
docker_gid=$(stat -c %g /var/run/docker.sock 2>/dev/null || echo 0)
admin_bind=${NSC_ADMIN_BIND:-0.0.0.0}
admin_port=${NSC_ADMIN_PORT:-8080}
grpc_bind=${NSC_GRPC_BIND:-0.0.0.0}
grpc_port=${NSC_GRPC_PORT:-9443}
metrics_port=${NSC_METRICS_PORT:-9090}
http_bind=${NSC_HTTP_BIND:-0.0.0.0}
http_port=${NSC_HTTP_PORT:-80}
https_bind=${NSC_HTTPS_BIND:-0.0.0.0}
https_port=${NSC_HTTPS_PORT:-443}

{
  echo "NSC_ADMIN_USERNAME=$admin_username"
  echo "NSC_ADMIN_TOKEN=$admin_token"
  echo "NSC_SESSION_KEY=$session_key"
  echo "NSC_ENCRYPTION_KEY_B64=$encryption_key"
  echo "NSC_DATABASE_PASSWORD=$database_password"
  echo "NSC_SECRET_GID=$secret_gid"
  echo "DOCKER_GID=$docker_gid"
  echo "NSC_ADMIN_BIND=$admin_bind"
  echo "NSC_ADMIN_PORT=$admin_port"
  echo "NSC_GRPC_BIND=$grpc_bind"
  echo "NSC_GRPC_PORT=$grpc_port"
  echo "NSC_METRICS_PORT=$metrics_port"
  echo "NSC_HTTP_BIND=$http_bind"
  echo "NSC_HTTP_PORT=$http_port"
  echo "NSC_HTTPS_BIND=$https_bind"
  echo "NSC_HTTPS_PORT=$https_port"
  echo "NSC_ACME_EMAIL="
  echo "NSC_ACME_ACCEPT_TERMS=false"
  echo "NSC_GATEWAY_ID=gw_not-configured"
  echo "NSC_POOL_ID=pool_not-configured"
} > "$environment_file"

"$controller_bin" pki init-ca --cert "$pki_dir/ca.crt" --key "$pki_dir/ca.key"
"$controller_bin" pki issue-controller --instance-id controller \
  --ca-cert "$pki_dir/ca.crt" --ca-key "$pki_dir/ca.key" \
  --cert "$controller_secret_dir/controller.crt" --key "$controller_secret_dir/controller.key" \
  --dns controller --dns controller-mysql --dns controller-sqlite --dns localhost --ip 127.0.0.1
cp "$pki_dir/ca.crt" "$controller_secret_dir/ca.crt"
cp "$pki_dir/ca.crt" "$gateway_secret_dir/ca.crt"
chmod 0700 "$pki_dir"
chmod 0750 "$controller_secret_dir" "$gateway_secret_dir"
chmod 0400 "$pki_dir/ca.key"
chmod 0440 "$controller_secret_dir/controller.key" "$controller_secret_dir/ca.crt" "$controller_secret_dir/controller.crt" "$gateway_secret_dir/ca.crt"
chmod 0444 "$pki_dir/ca.crt"

echo "created .env, offline CA, and controller certificates"
echo "keep $pki_dir/ca.key offline; it is not mounted into any service"
