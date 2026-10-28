#!/usr/bin/env bash
# Disposable nested daemons only; never joins the host's Swarm or prunes resources.
set -euo pipefail
volume_run="nsc-volume-e2e-$$"
manager="$volume_run-manager"
worker="$volume_run-worker"
cleanup() { docker rm -fv "$worker" "$manager" >/dev/null 2>&1 || true; docker network rm "$volume_run" >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM
docker network create "$volume_run" >/dev/null
image='docker:28-dind@sha256:2a232a42256f70d78e3cc5d2b5d6b3276710a0de0596c145f627ecfae90282ac'
for node in "$manager" "$worker"; do
 docker run -d --privileged --network "$volume_run" --name "$node" --hostname "$node" -e DOCKER_TLS_CERTDIR= -p 127.0.0.1::2375 "$image" --tls=false >/dev/null
 for attempt in $(seq 1 45); do
  if docker exec "$node" docker info >/dev/null 2>&1; then break; fi
  sleep 1
 done
 docker exec "$node" docker info >/dev/null
 done
manager_address=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$manager")
docker exec "$manager" docker swarm init --advertise-addr "$manager_address" >/dev/null
join_token=$(docker exec "$manager" docker swarm join-token -q worker)
docker exec "$worker" docker swarm join --token "$join_token" "$manager_address:2377" >/dev/null
# Load an already installed fixture image; no application images or production volumes are touched.
docker save alpine:3.24 | docker exec -i "$worker" docker load >/dev/null
manager_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "2375/tcp") 0).HostPort}}' "$manager")
worker_port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "2375/tcp") 0).HostPort}}' "$worker")
NSC_VOLUME_TEST_MANAGER="tcp://127.0.0.1:$manager_port" NSC_VOLUME_TEST_WORKER="tcp://127.0.0.1:$worker_port" "${GO:-go}" test ./internal/volumemanager -run '^TestIsolatedTwoNodeVolumeLifecycle$' -count=1 -v
