#!/bin/sh
# Swarm scheduler fixture with ordinary NGINX, not real Agent acceptance.
# Never initializes or joins the host Swarm. For loaded/LKG/traffic evidence run
# scripts/gateway_acceptance.py; this fixture proves task replacement ordering.
set -eu

daemon_image=${NSC_CLUSTER_DIND_IMAGE:-docker:28-dind@sha256:2a232a42256f70d78e3cc5d2b5d6b3276710a0de0596c145f627ecfae90282ac}
nginx_image=${NSC_NGINX_IMAGE:-nginx:1.31.5-alpine3.24@sha256:72ba65eb42c10344912a84ff42408db7d34f2feb642204570ab8fc5ffd29f1d3}
daemon=nsc-gateway-rollout-test-$$
stack=nscgatewaytest
service=${stack}_gateway
work=$(mktemp -d /tmp/nsc-gateway-rollout.XXXXXX)
cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ -n "${poller:-}" ]; then kill "$poller" >/dev/null 2>&1 || true; fi
  docker rm -fv "$daemon" >/dev/null 2>&1 || true
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT INT TERM

for tool in docker jq python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing $tool" >&2; exit 1; }
done
docker run -d --privileged --name "$daemon" --hostname "$daemon" \
  -e DOCKER_TLS_CERTDIR= "$daemon_image" --tls=false >/dev/null
ready=false
for attempt in $(seq 1 45); do
  if docker exec "$daemon" docker info >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
[ "$ready" = true ] || { echo 'nested Docker daemon did not start' >&2; exit 1; }
docker exec "$daemon" docker swarm init >/dev/null
docker exec "$daemon" docker pull "$nginx_image" >/dev/null
docker exec "$daemon" docker tag "$nginx_image" nsc-gateway-rollout-base:local
for version in v1 v2; do
  printf 'FROM nsc-gateway-rollout-base:local\nRUN printf "%%s\\n" %s > /usr/share/nginx/html/version\n' "$version" |
    docker exec -i "$daemon" docker build -q -t "nsc-gateway-rollout-fixture:$version" - >/dev/null
done

cat >"$work/stack.yaml" <<'YAML'
services:
  gateway:
    image: nsc-gateway-rollout-fixture:v1
    ports:
      - target: 80
        published: 8200
        protocol: tcp
        mode: ingress
      - target: 443
        published: 443
        protocol: tcp
        mode: ingress
    stop_grace_period: 30s
    deploy:
      mode: global
      update_config:
        parallelism: 1
        order: start-first
        failure_action: rollback
        monitor: 10s
      rollback_config:
        parallelism: 1
        order: start-first
YAML
docker exec -i "$daemon" docker stack deploy --detach=true --resolve-image never \
  --compose-file - "$stack" <"$work/stack.yaml" >/dev/null

wait_running() {
  for attempt in $(seq 1 120); do
    replicas=$(docker exec "$daemon" docker service ls --filter "name=$service" --format '{{.Replicas}}')
    if [ "$replicas" = 1/1 ]; then return 0; fi
    sleep 1
  done
  docker exec "$daemon" docker service ps --no-trunc "$service" >&2 || true
  echo 'global Gateway fixture did not reach 1/1' >&2
  return 1
}
wait_running
docker exec "$daemon" docker service inspect "$service" | jq -e '
  .[0].Spec as $spec |
  $spec.Mode.Global != null and
  ($spec.EndpointSpec.Ports | length == 2 and
    any(.[]; .TargetPort == 80 and .PublishedPort == 8200 and .Protocol == "tcp" and .PublishMode == "ingress") and
    any(.[]; .TargetPort == 443 and .PublishedPort == 443 and .Protocol == "tcp" and .PublishMode == "ingress")) and
  $spec.UpdateConfig.Parallelism == 1 and
  $spec.UpdateConfig.Order == "start-first" and
  $spec.UpdateConfig.FailureAction == "rollback" and
  $spec.UpdateConfig.Monitor == 10000000000 and
  $spec.RollbackConfig.Parallelism == 1 and
  $spec.RollbackConfig.Order == "start-first" and
  $spec.TaskTemplate.ContainerSpec.StopGracePeriod == 30000000000
' >/dev/null
docker exec "$daemon" wget -qO /dev/null http://127.0.0.1:8200/
[ "$(docker exec "$daemon" wget -qO - http://127.0.0.1:8200/version)" = v1 ]
old_task=$(docker exec "$daemon" docker service ps --no-trunc -q "$service" | head -n 1)
[ -n "$old_task" ]

(
  while [ ! -e "$work/stop-poll" ]; do
    if docker exec "$daemon" wget -qO /dev/null http://127.0.0.1:8200/ >/dev/null 2>&1; then
      echo reachable
    else
      echo unavailable
    fi
    sleep 0.2
  done
) >"$work/requests.log" &
poller=$!
docker exec "$daemon" docker service update --detach=true --no-resolve-image \
  --image nsc-gateway-rollout-fixture:v2 --force "$service" >/dev/null
complete=false
for attempt in $(seq 1 120); do
  state=$(docker exec "$daemon" docker service inspect --format '{{if .UpdateStatus}}{{.UpdateStatus.State}}{{end}}' "$service")
  replicas=$(docker exec "$daemon" docker service ls --filter "name=$service" --format '{{.Replicas}}')
  if [ "$state" = completed ] && [ "$replicas" = 1/1 ]; then complete=true; break; fi
  case "$state" in paused|rollback_started|rollback_paused|rollback_completed)
    docker exec "$daemon" docker service ps --no-trunc "$service" >&2
    echo "rolling update entered $state" >&2
    exit 1 ;;
  esac
  sleep 1
done
docker exec "$daemon" docker service ps --no-trunc "$service"
touch "$work/stop-poll"
wait "$poller"
poller=
grep -q reachable "$work/requests.log"
[ "$complete" = true ] || { echo 'rolling update did not complete' >&2; exit 1; }
new_task=$(docker exec "$daemon" docker service ps --no-trunc -q "$service" | head -n 1)
[ -n "$new_task" ] && [ "$new_task" != "$old_task" ]
docker exec "$daemon" docker inspect --type task "$old_task" >"$work/old.json"
docker exec "$daemon" docker inspect --type task "$new_task" >"$work/new.json"
python3 - "$work/old.json" "$work/new.json" <<'PY'
import datetime
import json
import sys

with open(sys.argv[1], encoding='utf-8') as source:
    old = json.load(source)[0]
with open(sys.argv[2], encoding='utf-8') as source:
    new = json.load(source)[0]
assert old['Status']['State'] == 'shutdown', old['Status']
assert new['Status']['State'] == 'running', new['Status']
parse = lambda value: datetime.datetime.fromisoformat(value.replace('Z', '+00:00'))
assert parse(new['Status']['Timestamp']) < parse(old['Status']['Timestamp']), (
    'old task shut down before replacement ran', old['Status'], new['Status'])
assert ':v2' in new['Spec']['ContainerSpec']['Image'], new['Spec']['ContainerSpec']['Image']
print('start-first verified: replacement RUNNING before previous SHUTDOWN')
PY
docker exec "$daemon" wget -qO /dev/null http://127.0.0.1:8200/
[ "$(docker exec "$daemon" wget -qO - http://127.0.0.1:8200/version)" = v2 ]
