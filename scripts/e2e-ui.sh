#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository=$(CDPATH= cd -- "$script_dir/.." && pwd)
version=$(sed -n '1p' "$repository/VERSION")
controller_image=${NSC_CONTROLLER_IMAGE:-nsc-controller:v$version}
gateway_image=${NSC_GATEWAY_IMAGE:-nsc-gateway:v$version}
cleanup_image=${NSC_CLEANUP_IMAGE:-alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b}

for dependency in docker curl npm node openssl; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$dependency" >&2
    exit 1
  fi
done

browser=${NSC_BROWSER_BINARY:-}
if [ -z "$browser" ]; then
  for candidate in google-chrome chromium chromium-browser; do
    if command -v "$candidate" >/dev/null 2>&1; then
      browser=$(command -v "$candidate")
      break
    fi
  done
fi
if [ -z "$browser" ] || [ ! -x "$browser" ]; then
  printf 'missing headless Chrome/Chromium; set NSC_BROWSER_BINARY\n' >&2
  exit 1
fi
docker image inspect "$controller_image" >/dev/null

suffix=$$
network=nsc-e2e-ui-$suffix
controller=nsc-e2e-ui-$suffix
proxy=nsc-e2e-ui-proxy-$suffix
work=$(mktemp -d /tmp/nsc-ui-e2e.XXXXXX)
data=$work/data
mkdir -p "$data"
chmod 0777 "$data"
tls_pid=
stage=initialize

cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ "$status" -ne 0 ]; then
    printf '\nUI E2E diagnostics (failed stage: %s):\n' "$stage" >&2
    docker logs "$controller" >&2 || true
    docker logs "$proxy" >&2 || true
    if [ -f "$work/chrome.stderr" ]; then
      tail -n 100 "$work/chrome.stderr" >&2 || true
    fi
  fi
  if [ -n "$tls_pid" ]; then kill "$tls_pid" 2>/dev/null || true; fi
  docker rm -f "$proxy" "$controller" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  docker run --rm --user 0 -v "$work:/cleanup" "$cleanup_image" sh -c 'rm -rf /cleanup/*' >/dev/null 2>&1 || true
  rmdir "$work" >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT INT TERM

stage=controller_start
docker network create --label io.network-service-composer.e2e=true "$network" >/dev/null
docker run -d --name "$controller" --network "$network" --network-alias controller -p 127.0.0.1::8080 \
  -v "$data:/var/lib/nsc" \
  -e NSC_STORAGE__DRIVER=sqlite \
  -e 'NSC_STORAGE__DSN=file:/var/lib/nsc/nsc.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_time_format=sqlite' \
  -e NSC_STORAGE__HA=false \
  -e NSC_SECURITY__ADMIN_TOKEN=browser-smoke-admin-token \
  -e NSC_SECURITY__SESSION_KEY=01234567890123456789012345678901 \
  -e NSC_SECURITY__PRIMARY_KEY_ID=v1 \
  -e 'NSC_SECURITY__ENCRYPTION_KEYS={"v1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}' \
  -e NSC_SERVER__GRPC_LISTEN= \
  -e NSC_METRICS__LISTEN=127.0.0.1:0 \
  -e NSC_MANAGEMENT__ENABLED=true \
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

stage=tls_test_origin
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$work/tls.key" -out "$work/tls.crt" -days 1 -subj /CN=localhost -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' >/dev/null 2>&1
cat > "$work/tls-proxy.cjs" <<'JS'
const https=require('node:https'),http=require('node:http'),fs=require('node:fs');
const [dir,target]=process.argv.slice(2);
const server=https.createServer({key:fs.readFileSync(dir+'/tls.key'),cert:fs.readFileSync(dir+'/tls.crt')},(req,res)=>{
 const upstream=http.request(target+req.url,{method:req.method,headers:req.headers},r=>{res.writeHead(r.statusCode,r.headers);r.pipe(res)});
 upstream.on('error',()=>{res.writeHead(502);res.end()});req.pipe(upstream);
});
server.listen(0,'127.0.0.1',()=>fs.writeFileSync(dir+'/tls.port',String(server.address().port)));
JS
node "$work/tls-proxy.cjs" "$work" "$api" &
tls_pid=$!
for _ in $(seq 1 30); do [ ! -s "$work/tls.port" ] || break; sleep 1; done
tls_api="https://127.0.0.1:$(cat "$work/tls.port")"
stage=browser_flows
cd "$repository"
E2E_BASE_URL="$api" E2E_HTTPS_BASE_URL="$tls_api" NSC_BROWSER_BINARY="$browser" E2E_ADMIN_PASSWORD=browser-smoke-admin-token \
  npm run e2e:shared --prefix frontend
printf 'ui_e2e vue=embedded browser=ok shared_api=ok i18n=8-locales session=preserved\n'
