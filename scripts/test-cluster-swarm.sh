#!/bin/sh
# Uses an isolated nested daemon. Never joins or modifies the host Swarm.
set -eu
image=${NSC_CLUSTER_DIND_IMAGE:-docker:28-dind@sha256:2a232a42256f70d78e3cc5d2b5d6b3276710a0de0596c145f627ecfae90282ac}
name=nsc-cluster-e2e-$$
cleanup() { docker rm -fv "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM
docker run -d --privileged --name "$name" --hostname "$name" -e DOCKER_TLS_CERTDIR= -p 127.0.0.1::2375 "$image" --tls=false >/dev/null
for attempt in $(seq 1 45); do
 if docker exec "$name" docker info >/dev/null 2>&1; then break; fi
 sleep 1
done
docker exec "$name" docker info >/dev/null
docker exec "$name" docker swarm init >/dev/null
docker exec "$name" docker service create --name nsc-zero --replicas 0 --no-resolve-image alpine:3.24 sleep 600 >/dev/null
port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "2375/tcp") 0).HostPort}}' "$name")
NSC_CLUSTER_TEST_ENDPOINT="tcp://127.0.0.1:$port" go test ./internal/cluster -run TestIsolatedSwarmLabels -count=1 -v
