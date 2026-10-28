#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository=$(CDPATH= cd -- "$script_dir/.." && pwd)
version=$(sed -n '1p' "$repository/VERSION")
controller_binary=${NSC_CONTROLLER_BINARY:-$repository/bin/nsc-controller}
controller_image=${NSC_CONTROLLER_IMAGE:-nsc-controller:v$version}
gateway_image=${NSC_GATEWAY_IMAGE:-nsc-gateway:v$version}
postgres_image=${NSC_POSTGRES_IMAGE:-postgres:18.6-alpine3.24@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2}
nginx_image=${NSC_NGINX_IMAGE:-nginx:1.31.5-alpine3.24@sha256:72ba65eb42c10344912a84ff42408db7d34f2feb642204570ab8fc5ffd29f1d3}
cleanup_image=${NSC_CLEANUP_IMAGE:-alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b}

for dependency in docker curl grep jq stat; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$dependency" >&2
    exit 1
  fi
done
if [ ! -x "$controller_binary" ]; then
  printf 'controller binary is missing: %s (run make build)\n' "$controller_binary" >&2
  exit 1
fi
docker image inspect "$controller_image" "$gateway_image" "$postgres_image" >/dev/null
docker compose version >/dev/null

suffix=$$
network=nsc-e2e-management-$suffix
controller=nsc-e2e-management-controller-$suffix
gateway=nsc-e2e-management-gateway-$suffix
database=nsc-e2e-management-postgres-$suffix
project=nscmanage$suffix
host=managed-$suffix.e2e.test
work=$(mktemp -d /tmp/nsc-management-e2e.XXXXXX)
secrets=$work/secrets
data=$work/data
state=$work/state
compose_file=$work/compose.yaml
failed_compose_file=$work/compose-failed.yaml
stage=initialize
mkdir -p "$secrets" "$data" "$state"
chmod 0777 "$data" "$state"

cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ "$status" -ne 0 ]; then
    printf '\nManagement E2E diagnostics (failed stage: %s):\n' "$stage" >&2
    docker logs "$controller" >&2 || true
    docker logs "$gateway" >&2 || true
    docker logs "$database" >&2 || true
    docker ps -a --filter "label=com.docker.compose.project=$project" >&2 || true
  fi
  if [ -f "$compose_file" ]; then
    NSC_E2E_MESSAGE=cleanup docker compose -p "$project" -f "$compose_file" down --remove-orphans --volumes >/dev/null 2>&1 || true
  fi
  docker rm -f "$gateway" "$controller" "$database" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  docker run --rm --user 0 -v "$work:/cleanup" "$cleanup_image" sh -c 'rm -rf /cleanup/*' >/dev/null 2>&1 || true
  rmdir "$work" >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT INT TERM

printf '%s\n' \
  'services:' \
  '  web:' \
  "    image: $nginx_image" \
  '    expose:' \
  "      - '80'" \
  '    environment:' \
  '      NSC_E2E_MESSAGE: ${NSC_E2E_MESSAGE:?required}' \
  '    volumes:' \
  '      - webdata:/data' \
  'volumes:' \
  '  webdata:' >"$compose_file"
printf '%s\n' \
  'services:' \
  '  web:' \
  "    image: $nginx_image" \
  '    expose:' \
  "      - '80'" \
  '    environment:' \
  '      NSC_E2E_MESSAGE: ${NSC_E2E_MESSAGE:?required}' \
  '    healthcheck:' \
  "      test: ['CMD', 'false']" \
  '      interval: 1s' \
  '      timeout: 1s' \
  '      retries: 1' >"$failed_compose_file"

docker network create --label io.network-service-composer.e2e=true "$network" >/dev/null
docker run -d --name "$database" --network "$network" \
  -e POSTGRES_DB=nsc -e POSTGRES_USER=nsc -e POSTGRES_PASSWORD=e2e-database-password \
  --health-cmd 'pg_isready -U nsc -d nsc' --health-interval 1s --health-timeout 1s --health-retries 30 \
  "$postgres_image" >/dev/null
for _ in $(seq 1 60); do
  [ "$(docker inspect -f '{{.State.Health.Status}}' "$database")" = healthy ] && break
  sleep 1
done
[ "$(docker inspect -f '{{.State.Health.Status}}' "$database")" = healthy ]
stage=pki
"$controller_binary" pki init-ca \
  --cert "$secrets/ca.crt" --key "$secrets/ca.key" \
  --common-name "NSC Management E2E CA" --valid-for 24h
"$controller_binary" pki issue-controller --instance-id controller \
  --ca-cert "$secrets/ca.crt" --ca-key "$secrets/ca.key" \
  --cert "$secrets/controller.crt" --key "$secrets/controller.key" \
  --common-name "$controller" --dns "$controller" --valid-for 2h
chmod 0644 "$secrets"/*

docker_gid=$(stat -c '%g' /var/run/docker.sock)
docker run -d --name "$controller" --network "$network" -p 127.0.0.1::8080 \
  --group-add "$docker_gid" \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v "$secrets:/run/nsc:ro" -v "$data:/var/lib/nsc" \
  -e NSC_STORAGE__DRIVER=postgres \
  -e NSC_STORAGE__DSN="postgres://nsc:e2e-database-password@$database:5432/nsc?sslmode=disable" \
  -e NSC_STORAGE__HA=true \
  -e NSC_MANAGEMENT__ENABLED=true \
  -e NSC_MANAGEMENT__POLL_INTERVAL=250ms \
  -e NSC_MANAGEMENT__HEARTBEAT_INTERVAL=1s \
  -e NSC_MANAGEMENT__DEPLOY_TIMEOUT=45s \
  -e NSC_SECURITY__ADMIN_TOKEN=e2e-admin-token \
  -e NSC_SECURITY__SESSION_KEY=01234567890123456789012345678901 \
  -e NSC_SECURITY__PRIMARY_KEY_ID=v1 \
  -e 'NSC_SECURITY__ENCRYPTION_KEYS={"v1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}' \
  -e NSC_SERVER__PUBLIC_URL="http://$controller:8080" \
  -e NSC_SERVER__GRPC_TLS_CERT_FILE=/run/nsc/controller.crt \
  -e NSC_SERVER__GRPC_TLS_KEY_FILE=/run/nsc/controller.key \
  -e NSC_SERVER__GRPC_CLIENT_CA_FILE=/run/nsc/ca.crt \
  -e NSC_CONTROLLER__INSTANCE_ID=controller \
  -e NSC_CONTROLLER__INTERNAL__LISTEN=:9444 \
  -e NSC_CONTROLLER__INTERNAL__URL="https://$controller:9444" \
  -e NSC_CONTROLLER__INTERNAL__CA_FILE=/run/nsc/ca.crt \
  -e NSC_CONTROLLER__INTERNAL__CERT_FILE=/run/nsc/controller.crt \
  -e NSC_CONTROLLER__INTERNAL__KEY_FILE=/run/nsc/controller.key \
  -e NSC_CONTROLLER__INTERNAL__GRPC_ADDRESS="$controller:9443" \
  -e NSC_CONTROLLER__DISCOVERY__RECONCILE__INTERVAL=1s \
  -e NSC_CONTROLLER__DISCOVERY__FULL_RECONCILE__INTERVAL=3s \
  -e NSC_CONTROLLER__ENDPOINT__READINESS__SUCCESS_THRESHOLD=1 \
  -e NSC_CONTROLLER__GATEWAY__OFFLINE_TIMEOUT=10s \
  "$controller_image" serve --config= >/dev/null

stage=controller_ready
api_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}' "$controller")
api=http://127.0.0.1:$api_port
for _ in $(seq 1 60); do
  if curl -fsS "$api/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "$api/readyz" >/dev/null

api_get() {
  curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api$1"
}

pool_response=$(curl -fsS \
  -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -d "{\"name\":\"management-e2e\",\"backend_network\":\"$network\",\"challenge_upstream\":\"http://$controller:8080\"}" \
  "$api/api/v1/gateway-pools")
pool_id=$(printf '%s' "$pool_response" | jq -er '.data.id')
gateway_id=$("$controller_binary" id gateway)
"$controller_binary" pki issue-agent \
  --ca-cert "$secrets/ca.crt" --ca-key "$secrets/ca.key" \
  --cert "$secrets/agent.crt" --key "$secrets/agent.key" \
  --pool-id "$pool_id" --gateway-id "$gateway_id" --valid-for 2h
chmod 0644 "$secrets"/*

docker run -d --name "$gateway" --network "$network" -p 127.0.0.1::80 -p 127.0.0.1::443 \
  --sysctl net.ipv4.ip_unprivileged_port_start=0 \
  -v "$secrets:/run/nsc:ro" -v "$state:/var/lib/nsc" \
  -e NSC_AGENT__GATEWAY_ID="$gateway_id" -e NSC_AGENT__POOL_ID="$pool_id" \
  -e NSC_AGENT__NAME=management-e2e-gateway -e NSC_AGENT__CONTROLLER_ADDRESS="$controller:9443" \
  -e NSC_AGENT__SERVER_NAME="$controller" -e NSC_AGENT__CA_CERT_FILE=/run/nsc/ca.crt \
  -e NSC_AGENT__TLS_CERT_FILE=/run/nsc/agent.crt -e NSC_AGENT__TLS_KEY_FILE=/run/nsc/agent.key \
  -e NSC_AGENT__STATE_DIR=/var/lib/nsc -e NSC_NGINX__GENERATION_DIR=/var/lib/nsc/generations \
  -e NSC_AGENT__HEARTBEAT_INTERVAL=1s \
  -e NSC_STORAGE__DRIVER=postgres \
  -e NSC_STORAGE__DSN="postgres://nsc:e2e-database-password@$database:5432/nsc?sslmode=disable" \
  "$gateway_image" --config= >/dev/null
gateway_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "80/tcp") 0).HostPort}}' "$gateway")

runtime_response=$(curl -fsS \
  -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -d "{\"pool_id\":\"$pool_id\",\"name\":\"managed-docker\",\"runtime_type\":\"docker\",\"endpoint\":\"unix:///var/run/docker.sock\",\"management_enabled\":true}" \
  "$api/api/v1/runtimes")
runtime_id=$(printf '%s' "$runtime_response" | jq -er '.data.id')

wait_operation() {
  operation_id=$1
  expected=$2
  for _ in $(seq 1 180); do
    operation=$(api_get "/api/v1/operations/$operation_id")
    status=$(printf '%s' "$operation" | jq -r '.data.status')
    case "$status" in
      QUEUED|RUNNING) sleep 1 ;;
      "$expected") return 0 ;;
      *) printf 'operation %s finished as %s, expected %s\n%s\n' "$operation_id" "$status" "$expected" "$operation" >&2; return 1 ;;
    esac
  done
  printf 'operation %s did not finish\n' "$operation_id" >&2
  return 1
}

wait_application_status() {
  wanted=$1
  for _ in $(seq 1 60); do
    status=$(api_get "/api/v1/applications/$application_id" | jq -r '.data.status')
    if [ "$status" = "$wanted" ]; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_service_replicas() {
  wanted=$1
  for _ in $(seq 1 90); do
    service=$(api_get "/api/v1/services?application_id=$application_id&limit=20" | jq -c '.data[0] // {}')
    desired=$(printf '%s' "$service" | jq -r '.desired_instances // -1')
    running=$(printf '%s' "$service" | jq -r '.running_instances // -1')
    healthy=$(printf '%s' "$service" | jq -r '.healthy_instances // -1')
    if [ "$desired" = "$wanted" ] && [ "$running" = "$wanted" ] && [ "$healthy" = "$wanted" ]; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_traffic() {
  for _ in $(seq 1 90); do
    if curl -fsS -H "Host: $host" "http://127.0.0.1:$gateway_port/" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

queue_application_action() {
  action=$1
  key=$2
  response=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
    -H "Idempotency-Key: $key" -d "{\"action\":\"$action\"}" "$api/api/v1/applications/$application_id/operations")
  printf '%s' "$response" | jq -er '.data.id'
}

stage=raw_install_with_domain
install_body=$work/install.json
jq -n --arg runtime "$runtime_id" --arg name "$project" --arg host "$host" --rawfile compose "$compose_file" \
  '{runtime_id:$runtime,name:"Managed E2E",project_name:$name,compose_yaml:$compose,environment:{raw:"NSC_E2E_MESSAGE=raw-mode\n"},domain_binding:{service:"web",domain:{host:$host,entrypoints:["http"],redirect_http_to_https:false,tls:{mode:"disabled"}},route:{path:"/",backend:{port:80,protocol:"http"}}}}' >"$install_body"
preflight_body=$work/preflight.json
jq -n --arg runtime "$runtime_id" --rawfile compose "$compose_file" \
  '{runtime_id:$runtime,compose_yaml:$compose,environment:{raw:"NSC_E2E_MESSAGE=raw-mode\n"}}' >"$preflight_body"
curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  --data-binary "@$preflight_body" "$api/api/v1/applications/preflight" | jq -e '.data.swarm_compatible == true' >/dev/null
install_response=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -D "$work/install.headers" -H 'Idempotency-Key: management-install' --data-binary "@$install_body" "$api/api/v1/applications")
application_id=$(printf '%s' "$install_response" | jq -er '.data.application.id')
operation_id=$(printf '%s' "$install_response" | jq -er '.data.operation.id')
grep -qi "^Location: /api/v1/operations/$operation_id" "$work/install.headers"
replayed_install=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: management-install' --data-binary "@$install_body" "$api/api/v1/applications")
printf '%s' "$replayed_install" | jq -e --arg app "$application_id" --arg operation "$operation_id" \
  '.data.application.id == $app and .data.operation.id == $operation' >/dev/null
wait_operation "$operation_id" SUCCEEDED
service=$(api_get "/api/v1/services?application_id=$application_id&limit=20" | jq -ec '.data[0]')
service_id=$(printf '%s' "$service" | jq -er '.id')
printf '%s' "$service" | jq -e --arg key "$project/web" --arg qualified "${project}_web" \
  '.managed == true and .service_key == $key and .qualified_name == $qualified and .dns_name == $qualified and .dns_status == "READY"' >/dev/null
bindings=$(api_get "/api/v1/services/$service_id/domain-bindings")
printf '%s' "$bindings" | jq -e --arg host "$host" '.data | length == 1 and .[0].domain.host == $host and .[0].route.status == "ACTIVE"' >/dev/null
wait_service_replicas 1
wait_traffic

stage=key_environment_upgrade
application_revision=$(api_get "/api/v1/applications/$application_id" | jq -er '.data.revision')
upgrade_body=$work/upgrade.json
jq -n \
  '{environment:{values:[{key:"NSC_E2E_MESSAGE",value:"key-mode"}]}}' >"$upgrade_body"
upgrade_response=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: management-key-upgrade' -H "If-Match: \"rev-$application_revision\"" \
  --data-binary "@$upgrade_body" "$api/api/v1/applications/$application_id/revisions")
upgrade_operation=$(printf '%s' "$upgrade_response" | jq -er '.data.operation.id')
wait_operation "$upgrade_operation" SUCCEEDED
wait_traffic

stage=redeploy
redeploy_operation=$(queue_application_action redeploy management-redeploy)
wait_operation "$redeploy_operation" SUCCEEDED
wait_service_replicas 1
wait_traffic

stage=restart
restart_response=$(curl -fsS -X POST -H 'Authorization: Bearer e2e-admin-token' -H 'Idempotency-Key: management-restart' \
  "$api/api/v1/services/$service_id/restart")
restart_operation=$(printf '%s' "$restart_response" | jq -er '.data.id')
wait_operation "$restart_operation" SUCCEEDED
wait_traffic

stage=scale
scale_response=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: management-scale' -d '{"replicas":2}' "$api/api/v1/services/$service_id/scale")
scale_operation=$(printf '%s' "$scale_response" | jq -er '.data.id')
wait_operation "$scale_operation" SUCCEEDED
wait_service_replicas 2
wait_traffic

stage=stop
operation_id=$(queue_application_action stop management-stop)
wait_operation "$operation_id" SUCCEEDED
wait_application_status STOPPED

stage=start_after_stop
operation_id=$(queue_application_action start management-start-one)
wait_operation "$operation_id" SUCCEEDED
wait_application_status RUNNING
wait_service_replicas 1
wait_traffic

stage=failed_upgrade_rollback
application_revision=$(api_get "/api/v1/applications/$application_id" | jq -er '.data.revision')
failed_body=$work/failed-upgrade.json
jq -n --rawfile compose "$failed_compose_file" \
  '{compose_yaml:$compose,environment:{values:[{key:"NSC_E2E_MESSAGE",value:"must-rollback"}]}}' >"$failed_body"
failed_response=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: management-failed-upgrade' -H "If-Match: \"rev-$application_revision\"" \
  --data-binary "@$failed_body" "$api/api/v1/applications/$application_id/revisions")
failed_operation=$(printf '%s' "$failed_response" | jq -er '.data.operation.id')
wait_operation "$failed_operation" ROLLED_BACK
wait_application_status RUNNING
wait_service_replicas 1
wait_traffic

stage=persistent_scale
service_revision=$(api_get "/api/v1/services/$service_id" | jq -er '.data.revision')
persistent_response=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: management-persistent-scale' -H "If-Match: \"rev-$service_revision\"" \
  -d '{"replicas":2,"persist":true}' "$api/api/v1/services/$service_id/scale")
persistent_operation=$(printf '%s' "$persistent_response" | jq -er '.data.id')
wait_operation "$persistent_operation" SUCCEEDED
wait_service_replicas 2
api_get "/api/v1/applications/$application_id/source" | jq -e '.data.compose_yaml | contains("replicas: 2")' >/dev/null

stage=service_stop
stop_response=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: management-service-stop' -d '{"replicas":0}' "$api/api/v1/services/$service_id/scale")
stop_operation=$(printf '%s' "$stop_response" | jq -er '.data.id')
wait_operation "$stop_operation" SUCCEEDED
wait_service_replicas 0
operation_id=$(queue_application_action redeploy management-after-service-stop)
wait_operation "$operation_id" SUCCEEDED
wait_service_replicas 2
wait_traffic

stage=down
operation_id=$(queue_application_action down management-down)
wait_operation "$operation_id" SUCCEEDED
wait_application_status DOWN
if docker ps -q --filter "label=com.docker.compose.project=$project" | grep . >/dev/null; then
  printf 'managed Compose containers remain after down\n' >&2
  exit 1
fi

stage=start_after_down
operation_id=$(queue_application_action start management-start-two)
wait_operation "$operation_id" SUCCEEDED
wait_application_status RUNNING
wait_service_replicas 2
wait_traffic

stage=remove_runtime
container_with_volume=$(docker ps -q --filter "label=com.docker.compose.project=$project" --filter 'label=com.docker.compose.service=web' | head -n 1)
[ -n "$container_with_volume" ]
docker exec "$container_with_volume" sh -c 'printf retained > /data/nsc-e2e-sentinel'
remove_status=412
for _ in 1 2 3 4 5; do
  service_revision=$(api_get "/api/v1/services/$service_id" | jq -er '.data.revision')
  remove_status=$(curl -sS -o "$work/remove-response.json" -w '%{http_code}' \
    -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
    -H 'Idempotency-Key: management-remove-runtime' -H "If-Match: \"rev-$service_revision\"" \
    -d '{"action":"remove_runtime"}' "$api/api/v1/services/$service_id/operations")
  [ "$remove_status" = 202 ] && break
  [ "$remove_status" = 412 ] || break
  sleep 1
done
if [ "$remove_status" != 202 ]; then
  printf 'remove runtime returned HTTP %s: %s\n' "$remove_status" "$(cat "$work/remove-response.json")" >&2
  exit 1
fi
remove_response=$(cat "$work/remove-response.json")
remove_operation=$(printf '%s' "$remove_response" | jq -er '.data.id')
wait_operation "$remove_operation" SUCCEEDED
[ -z "$(docker ps -q --filter "label=com.docker.compose.project=$project")" ]
docker volume inspect "${project}_webdata" >/dev/null
api_get "/api/v1/services/$service_id/domain-bindings" | jq -e '.data | length == 1 and .[0].route.status == "ACTIVE"' >/dev/null
api_get "/api/v1/applications/$application_id/source" | jq -e '.data.compose_yaml | contains("replicas: 2")' >/dev/null
operation_id=$(queue_application_action redeploy management-after-remove)
wait_operation "$operation_id" SUCCEEDED
wait_service_replicas 2
container_with_volume=$(docker ps -q --filter "label=com.docker.compose.project=$project" --filter 'label=com.docker.compose.service=web' | head -n 1)
[ "$(docker exec "$container_with_volume" cat /data/nsc-e2e-sentinel)" = retained ]
wait_traffic

printf 'management_e2e application=%s service=%s preflight=ok idempotency=ok raw=ok key=ok dns=ok domain=ok redeploy=ok restart=ok temporary_scale=reset persistent_scale=2 service_stop=0 down_start=ok rollback=ok remove_redeploy=ok\n' "$application_id" "$service_id"
