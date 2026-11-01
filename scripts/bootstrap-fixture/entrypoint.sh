#!/bin/sh
set -eu
# All identities exist only in this disposable nested node.
tr -d '-' </proc/sys/kernel/random/uuid >/etc/machine-id
cp /fixture/ssh-key.pub /root/.ssh/authorized_keys
chmod 0600 /root/.ssh/authorized_keys
ssh-keygen -A >/dev/null
/usr/sbin/sshd -D -e -o PasswordAuthentication=no -o PermitRootLogin=prohibit-password -o SetEnv=DOCKER_CONTEXT=nsc-unrelated-fixture &
exec /usr/local/bin/dockerd-entrypoint.sh dockerd --tls=false --host=unix:///var/run/docker.sock "$@"
