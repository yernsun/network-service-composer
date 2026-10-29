#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository=$(CDPATH= cd -- "$script_dir/.." && pwd)
version=$(sed -n '1p' "$repository/VERSION")
controller_binary=${NSC_CONTROLLER_BINARY:-$repository/bin/nsc-controller}
controller_image=${NSC_CONTROLLER_IMAGE:-nsc-controller:v$version}
gateway_image=${NSC_GATEWAY_IMAGE:-nsc-gateway:v$version}
pebble_image=${NSC_PEBBLE_IMAGE:-ghcr.io/letsencrypt/pebble:2.10.0@sha256:68cf1ec8a8db96f64244d5f559c448bc8e54f2934e0dd53a414eabffda7a6f22}
challenge_image=${NSC_PEBBLE_CHALLTESTSRV_IMAGE:-ghcr.io/letsencrypt/pebble-challtestsrv:2.10.0@sha256:df85447c39114cd2b3da971e751682ad2db19a996034b47d5392cd4a0f43406a}
cleanup_image=${NSC_CLEANUP_IMAGE:-alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b}

for dependency in docker curl jq stat openssl python3; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$dependency" >&2
    exit 1
  fi
done
if [ ! -x "$controller_binary" ]; then
  printf 'controller binary is missing: %s (run make build)\n' "$controller_binary" >&2
  exit 1
fi
postgres_image=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["images"]["postgres"])' "$repository/build/image-lock.json")
fixture_host=${NSC_E2E_HOST:-127.0.0.1}
publish_host=${NSC_E2E_PUBLISH_HOST:-127.0.0.1}
docker image inspect "$controller_image" "$gateway_image" "$pebble_image" "$challenge_image" >/dev/null

suffix=$$
network=nsc-e2e-acme-$suffix
controller=nsc-e2e-acme-controller-$suffix
database=nsc-e2e-acme-database-$suffix
gateway=nsc-e2e-acme-gateway-$suffix
pebble=nsc-e2e-acme-pebble-$suffix
challenge=nsc-e2e-acme-challenge-$suffix
work=$(mktemp -d "${TMPDIR:-/tmp}/nsc-acme-e2e.XXXXXX")
secrets=$work/secrets
data=$work/data
state=$work/state
stage=initialize
mkdir -p "$secrets" "$data" "$state"
chmod 0777 "$data" "$state"

cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ "$status" -ne 0 ]; then
    printf '\nACME E2E diagnostics (failed stage: %s):\n' "$stage" >&2
    docker logs "$controller" >&2 || true
    docker logs "$gateway" >&2 || true
    docker logs "$database" >&2 || true
    docker logs "$pebble" >&2 || true
    docker logs "$challenge" >&2 || true
  fi
  docker rm -f -v "$gateway" "$controller" "$database" "$pebble" "$challenge" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  docker run --rm --user 0 -v "$work:/cleanup" "$cleanup_image" sh -c 'rm -rf /cleanup/*' >/dev/null 2>&1 || true
  rmdir "$work" >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT INT TERM

docker network create --label io.network-service-composer.e2e=true "$network" >/dev/null

stage=pebble_start
docker run -d --name "$challenge" --network "$network" --network-alias pebble-dns \
  -p "$publish_host"::8055 \
  "$challenge_image" \
  -defaultIPv6 '' -http01 '' -https01 '' -tlsalpn01 '' -doh '' >/dev/null
docker run -d --name "$pebble" --network "$network" --network-alias pebble \
  -p "$publish_host"::14000 -p "$publish_host"::15000 \
  -v "$repository/test/e2e/pebble-config.json:/test/config/nsc-pebble-config.json:ro" \
  -e PEBBLE_VA_NOSLEEP=1 -e PEBBLE_WFE_NONCEREJECT=0 \
  "$pebble_image" -config test/config/nsc-pebble-config.json -dnsserver pebble-dns:8053 -strict=false >/dev/null

pebble_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "14000/tcp") 0).HostPort}}' "$pebble")
management_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "15000/tcp") 0).HostPort}}' "$pebble")
challenge_management_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "8055/tcp") 0).HostPort}}' "$challenge")
for _ in $(seq 1 60); do
  if curl -kfsS "https://$fixture_host:$pebble_port/dir" >/dev/null 2>&1 && \
     curl -fsS "http://$fixture_host:$challenge_management_port/" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -kfsS "https://$fixture_host:$pebble_port/dir" >/dev/null

docker cp "$pebble:/test/certs/pebble.minica.pem" "$secrets/pebble.minica.pem"
curl -kfsS -o "$secrets/pebble-issued-root.pem" "https://$fixture_host:$management_port/roots/0"

stage=database_start
database_password=$(openssl rand -hex 24)
storage_dsn="postgres://nsc:$database_password@$database:5432/nsc?sslmode=disable"
docker run -d --name "$database" --network "$network" \
  --label io.network-service-composer.e2e=true \
  -e POSTGRES_USER=nsc -e POSTGRES_DB=nsc -e POSTGRES_PASSWORD="$database_password" \
  "$postgres_image" >/dev/null
for _ in $(seq 1 60); do
  if docker exec "$database" pg_isready -h 127.0.0.1 -U nsc -d nsc >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "$database" pg_isready -h 127.0.0.1 -U nsc -d nsc >/dev/null

stage=pki
"$controller_binary" pki init-ca \
  --cert "$secrets/ca.crt" --key "$secrets/ca.key" \
  --common-name "NSC ACME E2E CA" --valid-for 24h
"$controller_binary" pki issue-controller --instance-id controller \
  --ca-cert "$secrets/ca.crt" --ca-key "$secrets/ca.key" \
  --cert "$secrets/controller.crt" --key "$secrets/controller.key" \
  --common-name "$controller" --dns "$controller" --valid-for 2h
chmod 0644 "$secrets"/*

docker run -d --name "$controller" --network "$network" --network-alias controller \
  -p "$publish_host"::8080 \
  -v "$secrets:/run/nsc:ro" -v "$data:/var/lib/nsc" \
  -e NSC_STORAGE__DRIVER=postgres \
  -e NSC_STORAGE__DSN="$storage_dsn" \
  -e NSC_STORAGE__HA=true \
  -e NSC_SECURITY__ADMIN_TOKEN=e2e-admin-token \
  -e NSC_SECURITY__SESSION_KEY=01234567890123456789012345678901 \
  -e NSC_SECURITY__PRIMARY_KEY_ID=v1 \
  -e 'NSC_SECURITY__ENCRYPTION_KEYS={"v1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}' \
  -e NSC_CONTROLLER__INSTANCE_ID=controller \
  -e NSC_CONTROLLER__INTERNAL__LISTEN=:9444 \
  -e NSC_CONTROLLER__INTERNAL__URL="https://$controller:9444" \
  -e NSC_CONTROLLER__INTERNAL__GRPC_ADDRESS="$controller:9443" \
  -e NSC_CONTROLLER__INTERNAL__CA_FILE=/run/nsc/ca.crt \
  -e NSC_CONTROLLER__INTERNAL__CERT_FILE=/run/nsc/controller.crt \
  -e NSC_CONTROLLER__INTERNAL__KEY_FILE=/run/nsc/controller.key \
  -e NSC_SERVER__PUBLIC_URL=http://controller:8080 \
  -e NSC_SERVER__GRPC_TLS_CERT_FILE=/run/nsc/controller.crt \
  -e NSC_SERVER__GRPC_TLS_KEY_FILE=/run/nsc/controller.key \
  -e NSC_SERVER__GRPC_CLIENT_CA_FILE=/run/nsc/ca.crt \
  -e NSC_CONTROLLER__CERTIFICATE__RECONCILE_INTERVAL=1s \
  -e NSC_CONTROLLER__CERTIFICATE__RENEW_BEFORE_EXPIRY=1h \
  -e NSC_CONTROLLER__GATEWAY__PERIODIC_INTERVAL=1s \
  -e NSC_CONTROLLER__GATEWAY__OFFLINE_TIMEOUT=10s \
  -e NSC_ACME__DIRECTORY_URL=https://pebble:14000/dir \
  -e NSC_ACME__EMAIL=acme-e2e@example.com \
  -e NSC_ACME__ACCEPT_TERMS=true \
  -e LEGO_CA_CERTIFICATES=/run/nsc/pebble.minica.pem \
  -e LEGO_CA_SERVER_NAME=localhost \
  "$controller_image" serve --config= >/dev/null

stage=controller_ready
api_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}' "$controller")
api=http://$fixture_host:$api_port
for _ in $(seq 1 60); do
  if curl -fsS "$api/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "$api/readyz" >/dev/null

pool_response=$(curl -fsS \
  -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -d "{\"name\":\"acme-e2e\",\"backend_network\":\"$network\",\"challenge_upstream\":\"http://controller:8080\"}" \
  "$api/api/v1/gateway-pools")
pool_id=$(printf '%s' "$pool_response" | jq -r '.data.id')
gateway_id=$("$controller_binary" id gateway)
"$controller_binary" pki issue-agent \
  --ca-cert "$secrets/ca.crt" --ca-key "$secrets/ca.key" \
  --cert "$secrets/agent.crt" --key "$secrets/agent.key" \
  --pool-id "$pool_id" --gateway-id "$gateway_id" --valid-for 2h
chmod 0644 "$secrets"/*

docker run -d --name "$gateway" --network "$network" \
  -p "$publish_host"::80 -p "$publish_host"::443 \
  --sysctl net.ipv4.ip_unprivileged_port_start=0 \
  -v "$secrets:/run/nsc:ro" -v "$state:/var/lib/nsc" \
  -e NSC_STORAGE__DRIVER=postgres \
  -e NSC_STORAGE__DSN="$storage_dsn" \
  -e NSC_STORAGE__HA=true \
  -e NSC_STORAGE__AUTO_MIGRATE=false \
  -e NSC_AGENT__GATEWAY_ID="$gateway_id" \
  -e NSC_AGENT__POOL_ID="$pool_id" \
  -e NSC_AGENT__NAME=acme-e2e-gateway \
  -e NSC_AGENT__CONTROLLER_ADDRESS=controller:9443 \
  -e NSC_AGENT__SERVER_NAME="$controller" \
  -e NSC_AGENT__CA_CERT_FILE=/run/nsc/ca.crt \
  -e NSC_AGENT__TLS_CERT_FILE=/run/nsc/agent.crt \
  -e NSC_AGENT__TLS_KEY_FILE=/run/nsc/agent.key \
  -e NSC_AGENT__STATE_DIR=/var/lib/nsc \
  -e NSC_NGINX__GENERATION_DIR=/var/lib/nsc/generations \
  -e NSC_AGENT__HEARTBEAT_INTERVAL=1s \
  "$gateway_image" --config= >/dev/null

gateway_ip=$(docker inspect -f "{{(index .NetworkSettings.Networks \"$network\").IPAddress}}" "$gateway")
[ -n "$gateway_ip" ]
curl -fsS -X POST -H 'Content-Type: application/json' -d "{\"ip\":\"$gateway_ip\"}" \
  "http://$fixture_host:$challenge_management_port/set-default-ipv4" >/dev/null
curl -fsS -X POST -H 'Content-Type: application/json' -d '{"ip":""}' \
  "http://$fixture_host:$challenge_management_port/set-default-ipv6" >/dev/null

gateway_https_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "443/tcp") 0).HostPort}}' "$gateway")

gateway_converged() {
  response=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/gateways?limit=200") || return 1
  count=$(printf '%s' "$response" | jq -r --arg gateway "$gateway_id" \
    '[(.data // [])[] | select(.id == $gateway and .enabled == true and .status == "READY" and .applied_config_revision > 0 and .applied_config_revision == .desired_config_revision)] | length')
  [ "$count" = 1 ]
}

gateway_registered() {
  response=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/gateways?limit=200") || return 1
  count=$(printf '%s' "$response" | jq -r --arg gateway "$gateway_id" \
    '[(.data // [])[] | select(.id == $gateway and .enabled == true and (.status == "INITIALIZING" or .status == "READY" or (.status == "STALE" and .desired_config_revision == 0)) and .last_seen_at != null and (.agent_version // "") != "")] | length')
  [ "$count" = 1 ]
}

wait_gateway_registered() {
  for _ in $(seq 1 90); do
    if gateway_registered; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_gateway_converged() {
  for _ in $(seq 1 90); do
    if gateway_converged; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_gateway_certificate() {
  python3 "$script_dir/wait_gateway_certificate.py" \
    --ca-file "$secrets/pebble-issued-root.pem" --connect-host "$fixture_host" \
    --port "$gateway_https_port" --server-name "$1" --fingerprint "$2"
}

certificate_json() {
  curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/certificates/$1" | jq -c '.data'
}

wait_certificate_active() {
  certificate_id=$1
  previous_fingerprint=${2:-}
  for _ in $(seq 1 180); do
    if value=$(certificate_json "$certificate_id" 2>/dev/null); then
      status=$(printf '%s' "$value" | jq -r '.status')
      fingerprint=$(printf '%s' "$value" | jq -r '.fingerprint // ""')
      key_id=$(printf '%s' "$value" | jq -r '.encryption_key_id // ""')
      serial=$(printf '%s' "$value" | jq -r '.serial_number // ""')
      not_after=$(printf '%s' "$value" | jq -r '.not_after // ""')
      if [ "$status" = ACTIVE ] && [ -n "$fingerprint" ] && [ "$fingerprint" != "$previous_fingerprint" ] && \
         [ -n "$key_id" ] && [ -n "$serial" ] && [ -n "$not_after" ]; then
        printf '%s\n' "$value"
        return 0
      fi
    fi
    sleep 1
  done
  return 1
}

wait_certificate_error() {
  certificate_id=$1
  for _ in $(seq 1 180); do
    if value=$(certificate_json "$certificate_id" 2>/dev/null); then
      status=$(printf '%s' "$value" | jq -r '.status')
      attempts=$(printf '%s' "$value" | jq -r '.attempt_count')
      retry_at=$(printf '%s' "$value" | jq -r '.next_attempt_at // ""')
      error=$(printf '%s' "$value" | jq -r '.last_error // ""')
      if [ "$status" = ERROR ] && [ "$attempts" -ge 1 ] && [ -n "$retry_at" ] && [ -n "$error" ]; then
        return 0
      fi
    fi
    sleep 1
  done
  return 1
}

create_managed_domain() {
  domain_name=$1
  curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
    -d "{\"pool_id\":\"$pool_id\",\"host\":\"$domain_name\",\"entrypoints\":[\"http\",\"https\"],\"redirect_http_to_https\":true,\"tls\":{\"mode\":\"managed\",\"acme_challenge\":\"http-01\"}}" \
    "$api/api/v1/domains"
}

stage=gateway_registration
# An empty Pool has no configuration revision to load yet. Registration is
# sufficient before creating the first managed Domain/challenge route; the
# subsequent convergence gate still requires a positive loaded revision.
wait_gateway_registered

domain_one=issued-$suffix.acme.e2e.test
stage=initial_issuance
domain_response=$(create_managed_domain "$domain_one")
domain_one_id=$(printf '%s' "$domain_response" | jq -r '.data.id')
certificate_one_id=$(printf '%s' "$domain_response" | jq -r '.data.tls.certificate_id')
# The worker deliberately defers ACME until a Gateway has ACKed the system
# challenge route. Requeue immediately after that ACK to keep the E2E fast.
wait_gateway_converged
curl -fsS -X POST -H 'Authorization: Bearer e2e-admin-token' \
  "$api/api/v1/certificates/$certificate_one_id/renew" >/dev/null
certificate_one=$(wait_certificate_active "$certificate_one_id")
fingerprint_one=$(printf '%s' "$certificate_one" | jq -r '.fingerprint')
wait_gateway_certificate "$domain_one" "$fingerprint_one"
wait_gateway_converged
curl -sS --cacert "$secrets/pebble-issued-root.pem" \
  --resolve "$domain_one:$gateway_https_port:$fixture_host" \
  -o /dev/null "https://$domain_one:$gateway_https_port/"
domain_state=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/domains/$domain_one_id" | jq -r '.data.status')
[ "$domain_state" = READY ]

stage=renewal
curl -fsS -X POST -H 'Authorization: Bearer e2e-admin-token' \
  "$api/api/v1/certificates/$certificate_one_id/renew" >/dev/null
certificate_renewed=$(wait_certificate_active "$certificate_one_id" "$fingerprint_one")
fingerprint_renewed=$(printf '%s' "$certificate_renewed" | jq -r '.fingerprint')
wait_gateway_certificate "$domain_one" "$fingerprint_renewed"
wait_gateway_converged
curl -sS --cacert "$secrets/pebble-issued-root.pem" \
  --resolve "$domain_one:$gateway_https_port:$fixture_host" \
  -o /dev/null "https://$domain_one:$gateway_https_port/"

stage=retry_failure
docker network disconnect "$network" "$pebble"
domain_two=retry-$suffix.acme.e2e.test
domain_response=$(create_managed_domain "$domain_two")
certificate_two_id=$(printf '%s' "$domain_response" | jq -r '.data.tls.certificate_id')
wait_gateway_converged
curl -fsS -X POST -H 'Authorization: Bearer e2e-admin-token' \
  "$api/api/v1/certificates/$certificate_two_id/renew" >/dev/null
wait_certificate_error "$certificate_two_id"

stage=retry_recovery
docker network connect --alias pebble "$network" "$pebble"
sleep 2
curl -fsS -X POST -H 'Authorization: Bearer e2e-admin-token' \
  "$api/api/v1/certificates/$certificate_two_id/renew" >/dev/null
certificate_two=$(wait_certificate_active "$certificate_two_id")
fingerprint_two=$(printf '%s' "$certificate_two" | jq -r '.fingerprint')
wait_gateway_certificate "$domain_two" "$fingerprint_two"
wait_gateway_converged
curl -sS --cacert "$secrets/pebble-issued-root.pem" \
  --resolve "$domain_two:$gateway_https_port:$fixture_host" \
  -o /dev/null "https://$domain_two:$gateway_https_port/"

stage=revocation
curl -fsS -X POST -H 'Authorization: Bearer e2e-admin-token' \
  "$api/api/v1/certificates/$certificate_two_id/revoke" >/dev/null
for _ in $(seq 1 120); do
  value=$(certificate_json "$certificate_two_id")
  status=$(printf '%s' "$value" | jq -r '.status')
  attempts=$(printf '%s' "$value" | jq -r '.attempt_count')
  retry_at=$(printf '%s' "$value" | jq -r '.next_attempt_at // ""')
  error=$(printf '%s' "$value" | jq -r '.last_error // ""')
  if [ "$status" = REVOKED ] && [ "$attempts" = 0 ] && [ -z "$retry_at" ] && [ -z "$error" ]; then
    break
  fi
  sleep 1
done
[ "$status" = REVOKED ]
[ "$attempts" = 0 ]
[ -z "$retry_at" ]
[ -z "$error" ]

printf 'acme_e2e pool=%s gateway=%s http01=ok issuance=ok distribution=ok renewal=ok retry=ok revocation=ok\n' \
  "$pool_id" "$gateway_id"
