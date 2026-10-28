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

for dependency in docker curl jq stat; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$dependency" >&2
    exit 1
  fi
done
if [ ! -x "$controller_binary" ]; then
  printf 'controller binary is missing: %s (run make build)\n' "$controller_binary" >&2
  exit 1
fi
docker image inspect "$controller_image" "$gateway_image" >/dev/null
if [ "$(docker info --format '{{.Swarm.LocalNodeState}}')" != active ] || \
   [ "$(docker info --format '{{.Swarm.ControlAvailable}}')" != true ]; then
  printf 'the Swarm E2E requires an active local manager\n' >&2
  exit 1
fi

suffix=$$
network=nsc-e2e-swarm-$suffix
controller=nsc-e2e-swarm-controller-$suffix
gateway=nsc-e2e-swarm-gateway-$suffix
database=nsc-e2e-swarm-postgres-$suffix
service_name=nscswarmext${suffix}_web
invalid_service=nscswarmmissing${suffix}_web
managed_stack=nscswarmmanage$suffix
managed_service=${managed_stack}_web
managed_host=managed-swarm-$suffix.e2e.test
managed_wait_attempts=${NSC_SWARM_MANAGED_WAIT_ATTEMPTS:-120}
local_node=$(docker info --format '{{.Name}}')
work=$(mktemp -d /tmp/nsc-swarm-e2e.XXXXXX)
secrets=$work/secrets
state=$work/state
managed_compose=$work/managed-stack.yaml
managed_failed_compose=$work/managed-stack-failed.yaml
mkdir -p "$secrets" "$state"
chmod 0777 "$state"

printf '%s\n' \
  'services:' \
  '  web:' \
  "    image: $nginx_image" \
  '    expose:' \
  "      - '80'" \
  '    environment:' \
  '      NSC_E2E_MODE: ${NSC_E2E_MODE:?required}' \
  '    deploy:' \
  '      replicas: 1' \
  '      placement:' \
  '        constraints:' \
  "          - 'node.hostname == $local_node'" >"$managed_compose"
printf '%s\n' \
  'services:' \
  '  web:' \
  "    image: 127.0.0.1:1/nsc/missing:$suffix" \
  '    expose:' \
  "      - '80'" \
  '    environment:' \
  '      NSC_E2E_MODE: ${NSC_E2E_MODE:?required}' \
  '    deploy:' \
  '      replicas: 1' \
  '      placement:' \
  '        constraints:' \
  "          - 'node.hostname == $local_node'" >"$managed_failed_compose"

cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ "$status" -ne 0 ]; then
    printf '\nSwarm E2E diagnostics:\n' >&2
    if [ -n "${managed_application_id:-}" ] && [ -n "${api:-}" ]; then
      printf 'Managed application service state:\n' >&2
      curl -fsS -H 'Authorization: Bearer e2e-admin-token' \
        "$api/api/v1/services?application_id=$managed_application_id&limit=20" >&2 || true
      printf '\n' >&2
    fi
    if [ -n "${pool_id:-}" ] && [ -n "${api:-}" ]; then
      printf 'Gateway pool state:\n' >&2
      curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/gateway-pools/$pool_id" >&2 || true
      printf '\nProbe metrics:\n' >&2
      docker exec "$controller" wget -qO- http://127.0.0.1:9090/metrics 2>/dev/null | grep '^nsc_service_dns_probe_results_total' >&2 || true
    fi
    printf '\nRecent controller logs:\n' >&2
    docker logs --tail 300 "$controller" >&2 || true
    printf '\nRecent agent logs:\n' >&2
    docker logs --tail 100 "$gateway" >&2 || true
    printf '\nRecent PostgreSQL logs:\n' >&2
    docker logs --tail 100 "$database" >&2 || true
    docker service ps --no-trunc "$service_name" >&2 || true
    docker service ps --no-trunc "$invalid_service" >&2 || true
    docker stack services "$managed_stack" >&2 || true
    docker service ps --no-trunc "$managed_service" >&2 || true
  fi
  docker stack rm "$managed_stack" >/dev/null 2>&1 || true
  docker service rm "$service_name" "$invalid_service" >/dev/null 2>&1 || true
  docker rm -f "$gateway" "$controller" "$database" >/dev/null 2>&1 || true
  for _ in $(seq 1 30); do
    if docker network rm "$network" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  docker run --rm --user 0 -v "$work:/cleanup" "$cleanup_image" sh -c 'rm -rf /cleanup/*' >/dev/null 2>&1 || true
  rmdir "$work" >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT INT TERM

docker network create --driver overlay --attachable \
  --label io.network-service-composer.e2e=true "$network" >/dev/null
docker run -d --name "$database" --network "$network" \
  -e POSTGRES_DB=nsc -e POSTGRES_USER=nsc -e POSTGRES_PASSWORD=e2e-database-password \
  --health-cmd 'pg_isready -U nsc -d nsc' --health-interval 1s --health-timeout 1s --health-retries 30 \
  "$postgres_image" >/dev/null
for _ in $(seq 1 60); do
  if [ "$(docker inspect -f '{{.State.Health.Status}}' "$database")" = healthy ]; then
    break
  fi
  sleep 1
done
[ "$(docker inspect -f '{{.State.Health.Status}}' "$database")" = healthy ]

"$controller_binary" pki init-ca \
  --cert "$secrets/ca.crt" --key "$secrets/ca.key" \
  --common-name "NSC Swarm E2E CA" --valid-for 24h
"$controller_binary" pki issue-controller --instance-id controller \
  --ca-cert "$secrets/ca.crt" --ca-key "$secrets/ca.key" \
  --cert "$secrets/controller.crt" --key "$secrets/controller.key" \
  --common-name "$controller" --dns "$controller" --valid-for 2h
chmod 0644 "$secrets"/*

docker_gid=$(stat -c '%g' /var/run/docker.sock)
docker run -d --name "$controller" --network "$network" -p 127.0.0.1::8080 \
  --group-add "$docker_gid" \
  -v /var/run/docker.sock:/var/run/docker.sock:ro -v "$secrets:/run/nsc:ro" \
  -e NSC_STORAGE__DRIVER=postgres \
  -e NSC_STORAGE__DSN="postgres://nsc:e2e-database-password@$database:5432/nsc?sslmode=disable" \
  -e NSC_STORAGE__HA=true \
  -e NSC_MANAGEMENT__ENABLED=true \
  -e NSC_MANAGEMENT__POLL_INTERVAL=250ms \
  -e NSC_MANAGEMENT__HEARTBEAT_INTERVAL=1s \
  -e NSC_MANAGEMENT__DEPLOY_TIMEOUT=25s \
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
  -e NSC_CONTROLLER__DISCOVERY__RECONCILE__INTERVAL=2s \
  -e NSC_CONTROLLER__DISCOVERY__FULL_RECONCILE__INTERVAL=5s \
  -e NSC_CONTROLLER__ENDPOINT__DRAIN_TIMEOUT=2s \
  -e NSC_CONTROLLER__GATEWAY__OFFLINE_TIMEOUT=10s \
  "$controller_image" serve --config= >/dev/null

api_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}' "$controller")
api=http://127.0.0.1:$api_port
for _ in $(seq 1 60); do
  if curl -fsS "$api/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "$api/readyz" >/dev/null

pool_response=$(curl -fsS \
  -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -d "{\"name\":\"swarm-e2e\",\"backend_network\":\"$network\",\"challenge_upstream\":\"http://$controller:8080\"}" \
  "$api/api/v1/gateway-pools")
pool_id=$(printf '%s' "$pool_response" | jq -r '.data.id')
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
  -e NSC_AGENT__PUBLIC_ADDRESS=127.0.0.1 \
  -e NSC_AGENT__NAME=swarm-e2e-gateway -e NSC_AGENT__CONTROLLER_ADDRESS="$controller:9443" \
  -e NSC_AGENT__SERVER_NAME="$controller" -e NSC_AGENT__CA_CERT_FILE=/run/nsc/ca.crt \
  -e NSC_AGENT__TLS_CERT_FILE=/run/nsc/agent.crt -e NSC_AGENT__TLS_KEY_FILE=/run/nsc/agent.key \
  -e NSC_AGENT__STATE_DIR=/var/lib/nsc -e NSC_NGINX__GENERATION_DIR=/var/lib/nsc/generations \
  -e NSC_AGENT__HEARTBEAT_INTERVAL=1s \
  -e NSC_STORAGE__DRIVER=postgres \
  -e NSC_STORAGE__DSN="postgres://nsc:e2e-database-password@$database:5432/nsc?sslmode=disable" \
  "$gateway_image" --config= >/dev/null

runtime_response=$(curl -fsS \
  -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -d "{\"pool_id\":\"$pool_id\",\"name\":\"swarm-engine\",\"runtime_type\":\"swarm\",\"endpoint\":\"unix:///var/run/docker.sock\",\"management_enabled\":true}" \
  "$api/api/v1/runtimes")
runtime_id=$(printf '%s' "$runtime_response" | jq -r '.data.id')

api_get() {
  curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api$1"
}

wait_operation() {
  operation_id=$1
  expected=$2
  for _ in $(seq 1 180); do
    operation=$(api_get "/api/v1/operations/$operation_id")
    operation_status=$(printf '%s' "$operation" | jq -r '.data.status')
    case "$operation_status" in
      QUEUED|RUNNING) sleep 1 ;;
      "$expected") return 0 ;;
      *) printf 'operation %s finished as %s, expected %s\n%s\n' "$operation_id" "$operation_status" "$expected" "$operation" >&2; return 1 ;;
    esac
  done
  printf 'operation %s did not finish\n' "$operation_id" >&2
  return 1
}

managed_service_json() {
  api_get "/api/v1/services?application_id=$managed_application_id&limit=20" | jq -c '.data[0] // {}'
}

wait_managed_replicas() {
  wanted=$1
  for _ in $(seq 1 "$managed_wait_attempts"); do
    value=$(managed_service_json)
    desired=$(printf '%s' "$value" | jq -r '.desired_instances // -1')
    running=$(printf '%s' "$value" | jq -r '.running_instances // -1')
    ready=$(printf '%s' "$value" | jq -r '.healthy_instances // -1')
    if [ "$desired" = "$wanted" ] && [ "$running" = "$wanted" ] && [ "$ready" = "$wanted" ]; then
      return 0
    fi
    sleep 1
  done
  printf 'managed replicas did not converge: wanted=%s actual=%s\n' "$wanted" "$value" >&2
  service_id=$(printf '%s' "$value" | jq -r '.id // empty')
  if [ -n "$service_id" ]; then
    printf 'managed instances: ' >&2
    api_get "/api/v1/services/$service_id/instances" >&2 || true
    printf '\n' >&2
  fi
  return 1
}

wait_managed_traffic() {
  for _ in $(seq 1 120); do
    if curl -fsS -H "Host: $managed_host" "http://127.0.0.1:$gateway_port/" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  printf 'managed route did not serve traffic for host %s\n' "$managed_host" >&2
  return 1
}

queue_managed_action() {
  action=$1
  key=$2
  curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
    -H "Idempotency-Key: $key" -d "{\"action\":\"$action\"}" \
    "$api/api/v1/applications/$managed_application_id/operations" | jq -er '.data.id'
}

gateway_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "80/tcp") 0).HostPort}}' "$gateway")
if [ "${NSC_SWARM_MANAGEMENT_ONLY:-false}" != true ]; then
  docker service create --quiet --name "$service_name" --network "$network" --replicas 3 \
    --constraint "node.hostname==$local_node" \
    --update-parallelism 1 --update-delay 1s \
    --label nsc.enable=true --label nsc.port=80 --label nsc.protocol=http \
    --label nsc.readiness.mode=tcp --label nsc.readiness.interval=1s --label nsc.readiness.timeout=1s \
    --label nsc.readiness.success-threshold=1 --label nsc.readiness.failure-threshold=1 \
    --label nsc.routes.public.host=swarm.e2e.test --label nsc.routes.public.path=/ \
    --label nsc.routes.public.entrypoints=http \
    "$nginx_image" >/dev/null

  service_json() {
    curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/services?limit=200" |
      jq -c --arg name "$service_name" '[.data[] | select(.name == $name)][0] // {}'
  }
  wait_dns_ready() {
    for _ in $(seq 1 120); do
      if ! value=$(service_json); then
        sleep 1
        continue
      fi
      desired=$(printf '%s' "$value" | jq -r '.desired_instances // -1')
      running=$(printf '%s' "$value" | jq -r '.running_instances // -1')
      healthy=$(printf '%s' "$value" | jq -r '.healthy_instances // -1')
      dns_name=$(printf '%s' "$value" | jq -r '.dns_name // ""')
      dns_status=$(printf '%s' "$value" | jq -r '.dns_status // ""')
      config_error=$(printf '%s' "$value" | jq -r '.configuration_error // ""')
      if [ "$desired" = 3 ] && [ "$running" = 3 ] && [ "$healthy" = 3 ] && \
         [ "$dns_name" = "$service_name" ] && [ "$dns_status" = READY ] && [ -z "$config_error" ]; then
        return 0
      fi
      sleep 1
    done
    return 1
  }

  wait_dns_ready
  for _ in $(seq 1 60); do
    if curl -fsS -H 'Host: swarm.e2e.test' "http://127.0.0.1:$gateway_port/" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  curl -fsS -H 'Host: swarm.e2e.test' "http://127.0.0.1:$gateway_port/" >/dev/null
  config_revision_before=$(api_get "/api/v1/gateway-pools/$pool_id" | jq -er '.data.config_revision')
  config_preview=$(api_get "/api/v1/gateway-pools/$pool_id/config-preview?host=swarm.e2e.test")
  printf '%s' "$config_preview" | jq -er --arg dns "$service_name" \
    '.data.upstreams | length == 1 and (.[0].config | contains("server " + $dns + ":80 resolve;")) and (.[0].config | test("server [0-9]+\\.[0-9]+\\.[0-9]+\\.[0-9]+:") | not)' >/dev/null

  docker service update --quiet --force --update-parallelism 1 --update-delay 1s "$service_name" >/dev/null
  for _ in $(seq 1 90); do
    curl -fsS -H 'Host: swarm.e2e.test' "http://127.0.0.1:$gateway_port/" >/dev/null
    update_state=$(docker service inspect -f '{{if .UpdateStatus}}{{.UpdateStatus.State}}{{end}}' "$service_name")
    if [ "$update_state" = completed ]; then
      break
    fi
    sleep 1
  done
  [ "$(docker service inspect -f '{{.UpdateStatus.State}}' "$service_name")" = completed ]
  wait_dns_ready
  config_revision_after=$(api_get "/api/v1/gateway-pools/$pool_id" | jq -er '.data.config_revision')
  [ "$config_revision_after" = "$config_revision_before" ]

  task_container=$(docker ps -q --filter "label=com.docker.swarm.service.name=$service_name" | head -n 1)
  [ -n "$task_container" ]
  docker kill "$task_container" >/dev/null
  wait_dns_ready
  curl -fsS -H 'Host: swarm.e2e.test' "http://127.0.0.1:$gateway_port/" >/dev/null
  config_revision_after=$(api_get "/api/v1/gateway-pools/$pool_id" | jq -er '.data.config_revision')
  [ "$config_revision_after" = "$config_revision_before" ]

  docker service create --quiet --name "$invalid_service" --replicas 1 \
    --constraint "node.hostname==$local_node" \
    --label nsc.enable=true --label nsc.port=80 --label nsc.readiness.mode=tcp \
    --label nsc.routes.public.host=invalid.swarm.e2e.test --label nsc.routes.public.path=/ \
    --label nsc.routes.public.entrypoints=http \
    "$nginx_image" >/dev/null
  for _ in $(seq 1 60); do
    if ! invalid=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' "$api/api/v1/services?limit=200" |
      jq -c --arg name "$invalid_service" '[.data[] | select(.name == $name)][0] // {}'); then
      sleep 1
      continue
    fi
    invalid_status=$(printf '%s' "$invalid" | jq -r '.dns_status // ""')
    invalid_error=$(printf '%s' "$invalid" | jq -r '([.dns_last_error // "", .configuration_error // ""] + (.routing_issues // [])) | map(select(length > 0)) | join("; ")')
    if [ "$invalid_status" = MISSING ] && printf '%s' "$invalid_error" | grep -q "network \"$network\""; then
      break
    fi
    sleep 1
  done
  [ "$invalid_status" = MISSING ]
  printf '%s' "$invalid_error" | grep -q "network \"$network\""
fi

managed_install_body=$work/managed-install.json
jq -n --arg runtime "$runtime_id" --arg name "$managed_stack" --arg host "$managed_host" --rawfile compose "$managed_compose" \
  '{runtime_id:$runtime,name:"Managed Swarm E2E",project_name:$name,compose_yaml:$compose,environment:{raw:"NSC_E2E_MODE=raw-mode\n"},domain_binding:{service:"web",domain:{host:$host,entrypoints:["http"],redirect_http_to_https:false,tls:{mode:"disabled"}},route:{path:"/",backend:{port:80,protocol:"http"}}}}' >"$managed_install_body"
managed_preflight_body=$work/managed-preflight.json
jq -n --arg runtime "$runtime_id" --rawfile compose "$managed_compose" \
  '{runtime_id:$runtime,compose_yaml:$compose,environment:{raw:"NSC_E2E_MODE=raw-mode\n"}}' >"$managed_preflight_body"
curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  --data-binary "@$managed_preflight_body" "$api/api/v1/applications/preflight" | jq -e '.data.swarm_compatible == true' >/dev/null
managed_install=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -D "$work/managed-install.headers" -H 'Idempotency-Key: swarm-management-install' --data-binary "@$managed_install_body" "$api/api/v1/applications")
managed_application_id=$(printf '%s' "$managed_install" | jq -er '.data.application.id')
managed_operation_id=$(printf '%s' "$managed_install" | jq -er '.data.operation.id')
grep -qi "^Location: /api/v1/operations/$managed_operation_id" "$work/managed-install.headers"
managed_install_replay=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: swarm-management-install' --data-binary "@$managed_install_body" "$api/api/v1/applications")
printf '%s' "$managed_install_replay" | jq -e --arg app "$managed_application_id" --arg operation "$managed_operation_id" \
  '.data.application.id == $app and .data.operation.id == $operation' >/dev/null
wait_operation "$managed_operation_id" SUCCEEDED
wait_managed_replicas 1
managed_service_value=$(managed_service_json)
managed_database_service_id=$(printf '%s' "$managed_service_value" | jq -er '.id')
old_runtime_object_id=$(printf '%s' "$managed_service_value" | jq -er '.runtime_object_id')
printf '%s' "$managed_service_value" | jq -e --arg key "$managed_service" \
  '.managed == true and .service_key == $key and .qualified_name == $key and .dns_name == $key and .dns_status == "READY"' >/dev/null
[ "$(docker service inspect -f '{{len .Endpoint.Spec.Ports}}' "$managed_service")" = 0 ]
managed_bindings=$(api_get "/api/v1/services/$managed_database_service_id/domain-bindings")
printf '%s' "$managed_bindings" | jq -e --arg host "$managed_host" \
  '.data | length == 1 and .[0].domain.host == $host and .[0].route.status == "ACTIVE" and any(.[0].dns_targets[]; .record_type == "A" and .value == "127.0.0.1")' >/dev/null
wait_managed_traffic
managed_config_revision=$(api_get "/api/v1/gateway-pools/$pool_id" | jq -er '.data.config_revision')

managed_application_revision=$(api_get "/api/v1/applications/$managed_application_id" | jq -er '.data.revision')
managed_upgrade_body=$work/managed-upgrade.json
jq -n \
  '{environment:{values:[{key:"NSC_E2E_MODE",value:"key-mode"}]}}' >"$managed_upgrade_body"
managed_upgrade=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: swarm-management-key-upgrade' -H "If-Match: \"rev-$managed_application_revision\"" \
  --data-binary "@$managed_upgrade_body" "$api/api/v1/applications/$managed_application_id/revisions")
managed_operation_id=$(printf '%s' "$managed_upgrade" | jq -er '.data.operation.id')
wait_operation "$managed_operation_id" SUCCEEDED
wait_managed_traffic

managed_operation_id=$(queue_managed_action redeploy swarm-management-redeploy)
wait_operation "$managed_operation_id" SUCCEEDED
wait_managed_replicas 1
wait_managed_traffic

managed_restart=$(curl -fsS -X POST -H 'Authorization: Bearer e2e-admin-token' -H 'Idempotency-Key: swarm-management-restart' \
  "$api/api/v1/services/$managed_database_service_id/restart")
managed_operation_id=$(printf '%s' "$managed_restart" | jq -er '.data.id')
wait_operation "$managed_operation_id" SUCCEEDED
wait_managed_replicas 1
wait_managed_traffic

managed_scale=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: swarm-management-scale' -d '{"replicas":2}' "$api/api/v1/services/$managed_database_service_id/scale")
managed_operation_id=$(printf '%s' "$managed_scale" | jq -er '.data.id')
wait_operation "$managed_operation_id" SUCCEEDED
wait_managed_replicas 2

managed_operation_id=$(queue_managed_action stop swarm-management-stop)
wait_operation "$managed_operation_id" SUCCEEDED
[ "$(api_get "/api/v1/applications/$managed_application_id" | jq -r '.data.status')" = STOPPED ]
[ "$(api_get "/api/v1/gateway-pools/$pool_id" | jq -er '.data.config_revision')" = "$managed_config_revision" ]
if docker stack ls --format '{{.Name}}' | grep -Fx "$managed_stack" >/dev/null; then
  printf 'managed Swarm stack remains after stop\n' >&2
  exit 1
fi

managed_operation_id=$(queue_managed_action start swarm-management-start)
wait_operation "$managed_operation_id" SUCCEEDED
wait_managed_replicas 1
wait_managed_traffic
managed_service_value=$(managed_service_json)
new_runtime_object_id=$(printf '%s' "$managed_service_value" | jq -er '.runtime_object_id')
printf '%s' "$managed_service_value" | jq -e --arg id "$managed_database_service_id" '.id == $id' >/dev/null
[ "$new_runtime_object_id" != "$old_runtime_object_id" ]
managed_bindings=$(api_get "/api/v1/services/$managed_database_service_id/domain-bindings")
printf '%s' "$managed_bindings" | jq -e '.data | length == 1 and .[0].route.status == "ACTIVE"' >/dev/null
[ "$(api_get "/api/v1/gateway-pools/$pool_id" | jq -er '.data.config_revision')" = "$managed_config_revision" ]

managed_operation_id=$(queue_managed_action down swarm-management-down)
wait_operation "$managed_operation_id" SUCCEEDED
[ "$(api_get "/api/v1/applications/$managed_application_id" | jq -r '.data.status')" = DOWN ]
[ "$(api_get "/api/v1/gateway-pools/$pool_id" | jq -er '.data.config_revision')" = "$managed_config_revision" ]
if docker stack ls --format '{{.Name}}' | grep -Fx "$managed_stack" >/dev/null; then
  printf 'managed Swarm stack remains after down\n' >&2
  exit 1
fi

managed_operation_id=$(queue_managed_action start swarm-management-start-after-down)
wait_operation "$managed_operation_id" SUCCEEDED
wait_managed_replicas 1
wait_managed_traffic
[ "$(api_get "/api/v1/gateway-pools/$pool_id" | jq -er '.data.config_revision')" = "$managed_config_revision" ]

managed_application_revision=$(api_get "/api/v1/applications/$managed_application_id" | jq -er '.data.revision')
managed_failed_body=$work/managed-failed-upgrade.json
jq -n --rawfile compose "$managed_failed_compose" \
  '{compose_yaml:$compose,environment:{values:[{key:"NSC_E2E_MODE",value:"must-rollback"}]}}' >"$managed_failed_body"
managed_failed_upgrade=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: swarm-management-failed-upgrade' -H "If-Match: \"rev-$managed_application_revision\"" \
  --data-binary "@$managed_failed_body" "$api/api/v1/applications/$managed_application_id/revisions")
managed_operation_id=$(printf '%s' "$managed_failed_upgrade" | jq -er '.data.operation.id')
wait_operation "$managed_operation_id" ROLLED_BACK
wait_managed_replicas 1
wait_managed_traffic

managed_service_revision=$(api_get "/api/v1/services/$managed_database_service_id" | jq -er '.data.revision')
managed_persistent_scale=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: swarm-management-persistent-scale' -H "If-Match: \"rev-$managed_service_revision\"" \
  -d '{"replicas":2,"persist":true}' "$api/api/v1/services/$managed_database_service_id/scale")
managed_operation_id=$(printf '%s' "$managed_persistent_scale" | jq -er '.data.id')
wait_operation "$managed_operation_id" SUCCEEDED
wait_managed_replicas 2
api_get "/api/v1/applications/$managed_application_id/source" | jq -e '.data.compose_yaml | contains("replicas: 2")' >/dev/null

managed_bindings=$(api_get "/api/v1/services/$managed_database_service_id/domain-bindings")
printf '%s' "$managed_bindings" | jq -e '.data | length == 1 and .[0].route.status == "ACTIVE"' >/dev/null

managed_service_revision=$(api_get "/api/v1/services/$managed_database_service_id" | jq -er '.data.revision')
managed_remove=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: swarm-management-remove-runtime' -H "If-Match: \"rev-$managed_service_revision\"" \
  -d '{"action":"remove_runtime"}' "$api/api/v1/services/$managed_database_service_id/operations")
managed_operation_id=$(printf '%s' "$managed_remove" | jq -er '.data.id')
wait_operation "$managed_operation_id" SUCCEEDED
if docker service inspect "$managed_service" >/dev/null 2>&1; then
  printf 'selected Swarm Service remains after removal\n' >&2
  exit 1
fi
managed_bindings=$(api_get "/api/v1/services/$managed_database_service_id/domain-bindings")
printf '%s' "$managed_bindings" | jq -e '.data | length == 1 and .[0].route.status == "ACTIVE"' >/dev/null
managed_operation_id=$(queue_managed_action redeploy swarm-management-after-remove)
wait_operation "$managed_operation_id" SUCCEEDED
wait_managed_replicas 2
wait_managed_traffic

managed_domain_id=$(printf '%s' "$managed_bindings" | jq -er '.data[0].domain.id')
fixed_route_id=$("$controller_binary" id route)
redirect_route_id=$("$controller_binary" id route)
external_route_id=$("$controller_binary" id route)
draft_headers=$work/nginx-draft.headers
curl -fsS -D "$draft_headers" -H 'Authorization: Bearer e2e-admin-token' \
  "$api/api/v1/gateway-pools/$pool_id/nginx" >"$work/nginx-before.json"
draft_etag=$(tr -d '\r' <"$draft_headers" | sed -n 's/^[Ee][Tt][Aa][Gg]: //p' | tail -n 1)
[ -n "$draft_etag" ]
jq -n --arg domain "$managed_domain_id" --arg fixed "$fixed_route_id" \
  --arg redirect "$redirect_route_id" --arg external "$external_route_id" \
  --arg target "http://$managed_service:80/" --arg host "$managed_host" \
  '{snippets:[],routes:[
    {route:{id:$fixed,domain_id:$domain,kind:"return",match:"exact",path:"/hello",response_code:200,response_value:"hello-e2e",enabled:true}},
    {route:{id:$redirect,domain_id:$domain,kind:"redirect",match:"exact",path:"/old",response_code:308,response_value:("http://"+$host+"/hello"),enabled:true}},
    {route:{id:$external,domain_id:$domain,kind:"proxy",match:"prefix",path:"/external/",external_url:$target,enabled:true}}
  ]}' >"$work/nginx-draft.json"
fixed_before=$(curl -sS -H "Host: $managed_host" "http://127.0.0.1:$gateway_port/hello")
[ "$fixed_before" != hello-e2e ]
draft_saved=$(curl -sS -X PUT -H 'Authorization: Bearer e2e-admin-token' \
  -H 'Content-Type: application/json' -H "If-Match: $draft_etag" \
  --data-binary "@$work/nginx-draft.json" "$api/api/v1/gateway-pools/$pool_id/nginx/draft")
if ! draft_revision=$(printf '%s' "$draft_saved" | jq -er '.data.revision'); then
  printf 'draft save failed: %s\n' "$draft_saved" >&2
  exit 1
fi
[ "$(curl -sS -H "Host: $managed_host" "http://127.0.0.1:$gateway_port/hello")" != hello-e2e ]
api_get "/api/v1/gateway-pools/$pool_id/nginx/preview?source=draft" |
  jq -e '[.data.files[] | select(contains("hello-e2e"))] | length > 0' >/dev/null
validation=$(curl -fsS -X POST -H 'Authorization: Bearer e2e-admin-token' \
  -H "If-Match: \"rev-$draft_revision\"" "$api/api/v1/gateway-pools/$pool_id/nginx/validations")
validation_id=$(printf '%s' "$validation" | jq -er '.data.id')
for _ in $(seq 1 90); do
  validation=$(api_get "/api/v1/gateway-pools/$pool_id/nginx/validations/$validation_id")
  validation_status=$(printf '%s' "$validation" | jq -r '.data.status')
  case "$validation_status" in SUCCEEDED) break ;; FAILED|EXPIRED|CANCELLED) printf '%s\n' "$validation" >&2; exit 1 ;; esac
  sleep 1
done
[ "$validation_status" = SUCCEEDED ]
release=$(curl -fsS -H 'Authorization: Bearer e2e-admin-token' -H 'Content-Type: application/json' \
  -H "If-Match: \"rev-$draft_revision\"" -H 'Idempotency-Key: swarm-route-e2e' \
  -d "{\"validation_id\":\"$validation_id\"}" "$api/api/v1/gateway-pools/$pool_id/nginx/releases")
release_id=$(printf '%s' "$release" | jq -er '.data.id')
for _ in $(seq 1 90); do
  release=$(api_get "/api/v1/gateway-pools/$pool_id/nginx/releases/$release_id")
  release_status=$(printf '%s' "$release" | jq -r '.data.status')
  case "$release_status" in SUCCEEDED) break ;; FAILED|PARTIAL|UNKNOWN|STOPPED) printf '%s\n' "$release" >&2; exit 1 ;; esac
  sleep 1
done
[ "$release_status" = SUCCEEDED ]
[ "$(curl -fsS -H "Host: $managed_host" "http://127.0.0.1:$gateway_port/hello")" = hello-e2e ]
[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "Host: $managed_host" "http://127.0.0.1:$gateway_port/old")" = 308 ]
[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "Host: $managed_host" "http://127.0.0.1:$gateway_port/external/")" = 200 ]
printf 'route_e2e fixed=ok redirect=ok external_proxy=ok draft_isolation=ok gateway_release=ok\n'

log_status=$(curl -sS -o /dev/null -w '%{http_code}' \
  "$api/api/v1/services/$managed_database_service_id/logs")
[ "$log_status" = 401 ]
if curl -sS -N --max-time 5 -H 'Authorization: Bearer e2e-admin-token' \
  "$api/api/v1/services/$managed_database_service_id/logs" >"$work/service-logs.sse"; then
  :
else
  [ "$?" -eq 28 ]
fi
grep -q '^event: log$' "$work/service-logs.sse"
printf 'service_logs_e2e admin_stream=ok anonymous_denied=ok\n'

printf 'swarm_e2e pool=%s gateway=%s dns_only=ok rolling_without_reload=ok replacement_without_reload=ok missing_overlay=diagnosed managed_stack=ok preflight=ok idempotency=ok env=raw+key domain=ok redeploy=ok restart=ok temporary_scale=reset persistent_scale=2 down_start=stable rollback=ok remove_redeploy=ok\n' \
  "$pool_id" "$gateway_id"
