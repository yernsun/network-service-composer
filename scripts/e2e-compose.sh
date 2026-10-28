#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository=$(CDPATH= cd -- "$script_dir/.." && pwd)
version=$(sed -n '1p' "$repository/VERSION")
controller_binary=${NSC_CONTROLLER_BINARY:-$repository/bin/nsc-controller}
controller_image=${NSC_CONTROLLER_IMAGE:-nsc-controller:v$version}
gateway_image=${NSC_GATEWAY_IMAGE:-nsc-gateway:v$version}
cleanup_image=${NSC_CLEANUP_IMAGE:-alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b}

for dependency in docker curl jq stat python3 openssl; do
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
docker image inspect "$controller_image" "$gateway_image" >/dev/null
docker compose version >/dev/null

suffix=$$
network=nsc-e2e-compose-$suffix
controller=nsc-e2e-compose-controller-$suffix
database=nsc-e2e-compose-database-$suffix
gateway=nsc-e2e-compose-gateway-$suffix
project=nsce2e$suffix
export NSC_E2E_NETWORK="$network"
export NSC_E2E_SERVICE_ALIAS="${project}_app"
work=$(mktemp -d "${TMPDIR:-/tmp}/nsc-compose-e2e.XXXXXX")
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
	printf '\nCompose E2E diagnostics (failed stage: %s):\n' "$stage" >&2
    if [ -n "${api:-}" ]; then
      curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/services?limit=200" |
        jq '[.data[] | {external_id, desired_instances, healthy_instances, configuration_error}]' >&2 || true
      curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/runtimes?limit=200" |
        jq '[.data[] | {enabled, status, last_error}]' >&2 || true
      curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/gateways?limit=200" |
        jq '[.data[] | {status, desired_config_revision, applied_config_revision}]' >&2 || true
    fi
    docker logs "$controller" >&2 || true
    docker logs "$gateway" >&2 || true
    docker logs "$database" >&2 || true
    NSC_E2E_NETWORK="$network" docker compose -p "$project" -f "$repository/test/e2e/compose-app.yaml" ps >&2 || true
  fi
  NSC_E2E_NETWORK="$network" docker compose -p "$project" -f "$repository/test/e2e/compose-app.yaml" down --volumes --remove-orphans >/dev/null 2>&1 || true
  docker rm -f -v "$gateway" "$controller" "$database" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  docker run --rm --user 0 -v "$work:/cleanup" "$cleanup_image" sh -c 'rm -rf /cleanup/*' >/dev/null 2>&1 || true
  rmdir "$work" >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT INT TERM

docker network create --label io.network-service-composer.e2e=true "$network" >/dev/null
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
  --common-name "NSC Compose E2E CA" --valid-for 24h
"$controller_binary" pki issue-controller --instance-id controller \
  --ca-cert "$secrets/ca.crt" --ca-key "$secrets/ca.key" \
  --cert "$secrets/controller.crt" --key "$secrets/controller.key" \
  --common-name "$controller" --dns "$controller" --valid-for 2h
chmod 0644 "$secrets"/*

docker_gid=${NSC_E2E_DOCKER_GID:-$(stat -c '%g' /var/run/docker.sock)}
docker run -d --name "$controller" --network "$network" -p "$publish_host"::8080 \
  --group-add "$docker_gid" \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v "$secrets:/run/nsc:ro" -v "$data:/var/lib/nsc" \
  -e NSC_STORAGE__DRIVER=postgres \
  -e NSC_STORAGE__DSN="$storage_dsn" \
  -e NSC_STORAGE__HA=true \
  -e NSC_MANAGEMENT__ENABLED=true \
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
  -e NSC_SERVER__PUBLIC_URL="http://$controller:8080" \
  -e NSC_SERVER__GRPC_TLS_CERT_FILE=/run/nsc/controller.crt \
  -e NSC_SERVER__GRPC_TLS_KEY_FILE=/run/nsc/controller.key \
  -e NSC_SERVER__GRPC_CLIENT_CA_FILE=/run/nsc/ca.crt \
  -e NSC_CONTROLLER__DISCOVERY__RECONCILE__INTERVAL=2s \
  -e NSC_CONTROLLER__DISCOVERY__FULL_RECONCILE__INTERVAL=5s \
  -e NSC_CONTROLLER__GATEWAY__OFFLINE_TIMEOUT=10s \
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
  -d "{\"name\":\"compose-e2e\",\"backend_network\":\"$network\",\"challenge_upstream\":\"http://$controller:8080\"}" \
  "$api/api/v1/gateway-pools")
pool_id=$(printf '%s' "$pool_response" | jq -r '.data.id')
gateway_id=$("$controller_binary" id gateway)
"$controller_binary" pki issue-agent \
  --ca-cert "$secrets/ca.crt" --ca-key "$secrets/ca.key" \
  --cert "$secrets/agent.crt" --key "$secrets/agent.key" \
  --pool-id "$pool_id" --gateway-id "$gateway_id" --valid-for 2h
chmod 0644 "$secrets"/*

docker run -d --name "$gateway" --network "$network" -p "$publish_host"::80 -p "$publish_host"::443 \
  --sysctl net.ipv4.ip_unprivileged_port_start=0 \
  -v "$secrets:/run/nsc:ro" -v "$state:/var/lib/nsc" \
  -e NSC_STORAGE__DRIVER=postgres \
  -e NSC_STORAGE__DSN="$storage_dsn" \
  -e NSC_STORAGE__HA=true \
  -e NSC_STORAGE__AUTO_MIGRATE=false \
  -e NSC_AGENT__GATEWAY_ID="$gateway_id" \
  -e NSC_AGENT__POOL_ID="$pool_id" \
  -e NSC_AGENT__NAME=compose-e2e-gateway \
  -e NSC_AGENT__CONTROLLER_ADDRESS="$controller:9443" \
  -e NSC_AGENT__SERVER_NAME="$controller" \
  -e NSC_AGENT__CA_CERT_FILE=/run/nsc/ca.crt \
  -e NSC_AGENT__TLS_CERT_FILE=/run/nsc/agent.crt \
  -e NSC_AGENT__TLS_KEY_FILE=/run/nsc/agent.key \
  -e NSC_AGENT__STATE_DIR=/var/lib/nsc \
  -e NSC_NGINX__GENERATION_DIR=/var/lib/nsc/generations \
  -e NSC_AGENT__HEARTBEAT_INTERVAL=1s \
  "$gateway_image" --config= >/dev/null

stage=discovery
curl -fsS \
  -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -d "{\"pool_id\":\"$pool_id\",\"name\":\"compose-engine\",\"runtime_type\":\"docker\",\"endpoint\":\"unix:///var/run/docker.sock\"}" \
  "$api/api/v1/runtimes" >/dev/null

NSC_E2E_NETWORK="$network" docker compose -p "$project" \
  -f "$repository/test/e2e/compose-app.yaml" up -d --scale app=3 >/dev/null

wait_service() {
  wanted=$1
  for _ in $(seq 1 90); do
    if ! response=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/services?limit=200"); then
      sleep 1
      continue
    fi
    desired=$(printf '%s' "$response" | jq -r --arg external "$project/app" '[.data[] | select(.external_id == $external)][0].desired_instances // -1')
    ready=$(printf '%s' "$response" | jq -r --arg external "$project/app" '[.data[] | select(.external_id == $external)][0].healthy_instances // -1')
    config_error=$(printf '%s' "$response" | jq -r --arg external "$project/app" '[.data[] | select(.external_id == $external)][0].configuration_error // ""')
    if [ "$desired" = 3 ] && [ "$ready" = "$wanted" ] && [ -z "$config_error" ]; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_gateway_traffic() {
  for _ in $(seq 1 60); do
    if curl -fsS -H 'Host: compose.e2e.test' "http://$fixture_host:$gateway_port/" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_controller_ready() {
  for _ in $(seq 1 60); do
    if curl -fsS "$api/readyz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_gateway_converged() {
  for _ in $(seq 1 60); do
    if response=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/gateways?limit=200" 2>/dev/null); then
      converged=$(printf '%s' "$response" | jq -r --arg gateway "$gateway_id" '[.data[] | select(.id == $gateway and .enabled == true and .status == "READY" and .applied_config_revision > 0 and .applied_config_revision == .desired_config_revision)] | length')
      if [ "$converged" = 1 ]; then
        return 0
      fi
    fi
    sleep 1
  done
  return 1
}

wait_service 3
gateway_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "80/tcp") 0).HostPort}}' "$gateway")
stage=initial_traffic
wait_gateway_traffic

config_revision_before=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/gateway-pools/$pool_id" | jq -er '.data.config_revision')
NSC_E2E_NETWORK="$network" docker compose -p "$project" \
  -f "$repository/test/e2e/compose-app.yaml" up -d --force-recreate --scale app=3 >/dev/null
stage=dns_recreate
wait_service 3
wait_gateway_traffic
config_revision_after=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/gateway-pools/$pool_id" | jq -er '.data.config_revision')
[ "$config_revision_after" = "$config_revision_before" ]

unhealthy=$(NSC_E2E_NETWORK="$network" docker compose -p "$project" \
  -f "$repository/test/e2e/compose-app.yaml" ps -q app | head -n 1)
[ -n "$unhealthy" ]
docker exec "$unhealthy" touch /tmp/nsc-unhealthy
stage=health_degraded
wait_service 2
curl -fsS -H 'Host: compose.e2e.test' "http://$fixture_host:$gateway_port/" >/dev/null
docker exec "$unhealthy" rm /tmp/nsc-unhealthy
stage=health_recovered
wait_service 3

# The data plane must remain available with the Controller stopped, including
# when the Gateway itself cold-starts from its persisted Last Known Good state.
docker stop -t 10 "$controller" >/dev/null
stage=controller_outage
wait_gateway_traffic
docker restart "$gateway" >/dev/null
gateway_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "80/tcp") 0).HostPort}}' "$gateway")
stage=lkg_cold_start
wait_gateway_traffic
docker start "$controller" >/dev/null
api_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}' "$controller")
api=http://$fixture_host:$api_port
stage=controller_recovery
wait_controller_ready
wait_service 3
stage=gateway_reconnect
wait_gateway_converged
wait_gateway_traffic

if [ "${NSC_E2E_NGINX:-false}" = true ]; then
  stage=nginx_sandbox_release
  NSC_E2E_HOST="$fixture_host" NSC_E2E_PUBLISH_HOST="$publish_host" NSC_E2E_API="$api" NSC_E2E_POOL="$pool_id" NSC_E2E_GATEWAY="$gateway" NSC_E2E_NETWORK="$network" NSC_E2E_CONTROLLER="$controller" NSC_E2E_WORK="$work" NSC_E2E_BINARY="$controller_binary" NSC_E2E_IMAGE="$gateway_image" python3 "$repository/scripts/e2e-nginx.py"
fi

printf 'compose_e2e pool=%s gateway=%s desired=3 healthy=3 dns_recreate_without_reload=ok health_recovery=ok controller_outage=ok lkg_cold_start=ok reconnect=ok\n' "$pool_id" "$gateway_id"
