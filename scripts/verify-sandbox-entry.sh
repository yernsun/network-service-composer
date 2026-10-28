#!/bin/sh
set -eu
if [ "$#" -lt 4 ]; then
  echo "usage: $0 CONNECT_HOST HTTP_PORT HTTPS_PORT DOMAIN [PATH] [HTTP_STATUS] [HTTPS_STATUS]" >&2
  exit 2
fi
connect_host=$1
http_port=$2
https_port=$3
domain=$4
request_path=${5:-/}
http_expected=${6:-200}
https_expected=${7:-200}
case "$request_path" in /*) ;; *) echo "expected an absolute request path" >&2; exit 2 ;; esac
http_status=$(curl --silent --show-error --noproxy '*' --max-time 10 --output /dev/null --write-out '%{http_code}' \
  --connect-to "$domain:80:$connect_host:$http_port" "http://$domain$request_path")
https_status=$(curl --silent --show-error --noproxy '*' --max-time 10 --output /dev/null --write-out '%{http_code}' \
  --connect-to "$domain:443:$connect_host:$https_port" "https://$domain$request_path")
echo "Sandbox HTTP: $http_status; HTTPS: $https_status"
[ "$http_status" = "$http_expected" ] && [ "$https_status" = "$https_expected" ]
