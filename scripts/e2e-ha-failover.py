#!/usr/bin/env python3
"""Owned, isolated DB failover/Controller partition acceptance fixture.

This is a controlled, caught-up promotion test, not a production DB operator.
No host Swarm changes, global pruning or existing container mutations are used.
"""

import argparse
import base64
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[1]


class Fixture:
    def __init__(self, driver, image, output):
        self.driver, self.image, self.output = driver, image, output
        self.prefix = "nsc-ha-failover-" + uuid.uuid4().hex[:12]
        self.network = self.prefix + "-net"
        self.names = []
        self.primary = self.prefix + "-primary"
        self.replica = self.prefix + "-replica"
        self.alias = self.prefix + "-writer"
        self.controllers = []
        self.work = Path(tempfile.mkdtemp(prefix="nsc-ha-failover-"))
        os.chmod(self.work, 0o700)
        self.engine = self.prefix + "-engine"
        self.token = uuid.uuid4().hex
        self.password = uuid.uuid4().hex
        self.report = {"status": "FAILED", "driver": driver, "checks": []}
        self.images = json.loads((ROOT / "build/image-lock.json").read_text())["images"]

    def run(self, *args, stdin=None, check=True, timeout=180):
        result = subprocess.run(args, input=stdin, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=timeout, check=False)
        if check and result.returncode:
            # SQL/DSNs and container logs can contain secrets. Keep only a code.
            raise RuntimeError(f"fixture.command_failed:{args[0]}:{result.returncode}")
        return result.stdout.decode().strip()

    def docker(self, *args, **kwargs):
        return self.run("docker", *args, **kwargs)

    def container(self, name, *args):
        self.names.append(name)
        return self.docker("run", "-d", "--name", name,
                           "--label", "io.network-service-composer.fixture=" + self.prefix,
                           "--network", self.network, *args)

    def sql(self, name, query):
        if self.driver == "postgres":
            return self.docker("exec", "-i", name, "psql", "-U", "nsc", "-d", "nsc",
                               "-v", "ON_ERROR_STOP=1", "-At", stdin=query.encode())
        return self.docker("exec", "-i", "-e", "MYSQL_PWD=" + self.password,
                           name, "mysql", "--protocol=TCP", "-h127.0.0.1", "-uroot", "-N", "-B", stdin=query.encode())

    @staticmethod
    def wait(predicate, seconds=120):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            try:
                value = predicate()
                if value:
                    return value
            except (RuntimeError, OSError, urllib.error.URLError, ValueError):
                pass
            time.sleep(.5)
        raise RuntimeError("fixture.deadline_exceeded")

    def api(self, base, path, value=None):
        body = None if value is None else json.dumps(value).encode()
        request = urllib.request.Request(base + path, data=body, headers={
            "Authorization": "Bearer " + self.token, "Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=3) as response:
            raw = response.read()
        return json.loads(raw).get("data") if raw else None

    def database(self):
        self.docker("network", "create", "--label",
                    "io.network-service-composer.fixture=" + self.prefix, self.network)
        if self.driver == "postgres":
            self.container(self.primary, "--network-alias", self.alias,
                           "-e", "POSTGRES_USER=nsc", "-e", "POSTGRES_DB=nsc",
                           "-e", "POSTGRES_PASSWORD=" + self.password, self.images["postgres"],
                           "-c", "wal_level=replica", "-c", "max_wal_senders=10",
                           "-c", "tcp_keepalives_idle=2", "-c", "tcp_keepalives_interval=1",
                           "-c", "tcp_keepalives_count=2")
            self.wait(lambda: self.sql(self.primary, "SELECT 1") == "1")
            self.docker("exec", self.primary, "sh", "-c",
                        'printf "host replication nsc all scram-sha-256\\n" >> "$PGDATA/pg_hba.conf"')
            self.sql(self.primary, "SELECT pg_reload_conf()")
            self.container(self.replica, "--user", "postgres", "--tmpfs",
                           "/var/lib/postgresql:uid=70,gid=70,mode=0700",
                           "-e", "PGPASSWORD=" + self.password,
                           "--entrypoint", "sh", self.images["postgres"], "-ec",
                           "mkdir -p /var/lib/postgresql/18/docker; chmod 0700 /var/lib/postgresql/18/docker; "
                           f"pg_basebackup -h {self.primary} -U nsc -D /var/lib/postgresql/18/docker -R -X stream; "
                           "exec postgres -D /var/lib/postgresql/18/docker")
            self.wait(lambda: self.sql(self.replica, "SELECT pg_is_in_recovery()") == "t")
        else:
            for name, server_id in ((self.primary, "1"), (self.replica, "2")):
                extra = ["--network-alias", self.alias] if name == self.primary else []
                self.container(name, *extra, "-e", "MYSQL_DATABASE=nsc", "-e", "MYSQL_USER=nsc",
                               "-e", "MYSQL_PASSWORD=" + self.password, "-e", "MYSQL_ROOT_HOST=%",
                               "-e", "MYSQL_ROOT_PASSWORD=" + self.password, self.images["mysql"],
                               "--server-id=" + server_id, "--gtid-mode=ON",
                               "--enforce-gtid-consistency=ON", "--log-bin=mysql-bin",
                               "--skip-replica-start=ON")
                self.wait(lambda name=name: self.sql(name, "SELECT 1") == "1")
            self.sql(self.primary, f"CREATE USER 'replicator'@'%' IDENTIFIED BY '{self.password}'; "
                     "GRANT REPLICATION SLAVE ON *.* TO 'replicator'@'%';")
            dump = self.docker("exec", "-e", "MYSQL_PWD=" + self.password,
                               self.primary, "mysqldump", "-uroot", "--single-transaction",
                               "--set-gtid-purged=ON", "--no-tablespaces", "--databases", "nsc")
            self.sql(self.replica, "RESET BINARY LOGS AND GTIDS;")
            self.sql(self.replica, dump)
            self.sql(self.replica, f"CHANGE REPLICATION SOURCE TO SOURCE_HOST='{self.primary}', "
                     f"SOURCE_USER='replicator', SOURCE_PASSWORD='{self.password}', "
                     "SOURCE_AUTO_POSITION=1, GET_SOURCE_PUBLIC_KEY=1; START REPLICA; "
                     "SET GLOBAL super_read_only=ON;")
        self.report["checks"].append("real_replica_started")

    def start_controllers(self):
        pki = self.work / "pki"
        pki.mkdir(mode=0o700)
        def issue(*args):
            self.docker("run", "--rm", "--network", "none", "--user", "0:0",
                        "-v", str(pki) + ":/pki", self.image, "pki", *args)
        issue("init-ca", "--cert", "/pki/ca.crt", "--key", "/pki/ca.key",
              "--common-name", "fixture", "--valid-for", "2h")
        for index in range(3):
            name = self.prefix + "-controller-" + str(index)
            issue("issue-controller", "--instance-id", name, "--ca-cert", "/pki/ca.crt",
                  "--ca-key", "/pki/ca.key", "--cert", f"/pki/{name}.crt", "--key",
                  f"/pki/{name}.key", "--common-name", name, "--dns", name, "--valid-for", "2h")
            dsn = (f"postgres://nsc:{self.password}@{self.alias}:5432/nsc?sslmode=disable&connect_timeout=3"
                   if self.driver == "postgres" else
                   f"nsc:{self.password}@tcp({self.alias}:3306)/nsc?parseTime=true&multiStatements=true&timeout=3s&readTimeout=3s&writeTimeout=3s")
            env = {"NSC_STORAGE__DRIVER": self.driver, "NSC_STORAGE__DSN": dsn,
                   "NSC_STORAGE__HA": "true", "NSC_SECURITY__ADMIN_TOKEN": self.token,
                   "NSC_SECURITY__SESSION_KEY": self.token,
                   "NSC_SECURITY__PRIMARY_KEY_ID": "v1",
                   "NSC_SECURITY__ENCRYPTION_KEYS": self.encryption_keys,
                   "NSC_SERVER__PUBLIC_URL": "http://" + name + ":8080", "NSC_SERVER__GRPC_LISTEN": "",
                   "NSC_CONTROLLER__INSTANCE_ID": name,
                   "NSC_CONTROLLER__INTERNAL__LISTEN": ":9444",
                   "NSC_CONTROLLER__INTERNAL__URL": "https://" + name + ":9444",
                   "NSC_CONTROLLER__INTERNAL__CA_FILE": "/run/nsc/ca.crt",
                   "NSC_CONTROLLER__INTERNAL__CERT_FILE": "/run/nsc/" + name + ".crt",
                   "NSC_CONTROLLER__INTERNAL__KEY_FILE": "/run/nsc/" + name + ".key",
                   "NSC_CONTROLLER__DISCOVERY__RECONCILE__INTERVAL": "1s",
                   "NSC_CONTROLLER__DISCOVERY__FULL_RECONCILE__INTERVAL": "2s"}
            arguments = [part for key, value in env.items() for part in ("-e", key + "=" + value)]
            self.container(name, "-p", "127.0.0.1::8080", "--user", "0:0",
                           "-v", str(pki) + ":/run/nsc:ro",
                           *arguments, self.image, "serve", "--config=")
            def assigned_port():
                ports = json.loads(self.docker("inspect", name))[0]["NetworkSettings"]["Ports"] or {}
                return (ports.get("8080/tcp") or [{}])[0].get("HostPort")
            port = self.wait(assigned_port, 10)
            base = "http://127.0.0.1:" + port
            self.controllers.append((name, base))
            self.wait(lambda: self.api(base, "/api/v1/gateway-pools") is not None)

    def observations(self, runtime):
        for name, base in self.controllers:
            if name != getattr(self, "partitioned", ""):
                try:
                    return self.api(base, "/api/v1/runtimes/" + runtime)["applied_observation"]
                except (OSError, urllib.error.URLError):
                    continue
        raise RuntimeError("fixture.no_available_controller")

    def refresh_ports(self):
        refreshed = []
        for name, _ in self.controllers:
            port = json.loads(self.docker("inspect", name))[0]["NetworkSettings"]["Ports"]["8080/tcp"][0]["HostPort"]
            refreshed.append((name, "http://127.0.0.1:" + port))
        self.controllers = refreshed

    def service_health(self, expected):
        for name, base in self.controllers:
            if name == getattr(self, "partitioned", ""):
                continue
            try:
                values = self.api(base, "/api/v1/services?limit=200")
                service = next((s for s in values if s.get("external_id") == "fixture/app"), {})
                return service.get("desired_instances") == 1 and service.get("healthy_instances") == expected
            except (OSError, urllib.error.URLError):
                continue
        return False

    def owner(self, runtime, excluded=""):
        if self.driver == "postgres":
            hashed = 14695981039346656037
            for byte in ("runtime/" + runtime).encode():
                hashed = ((hashed ^ byte) * 1099511628211) & ((1 << 64) - 1)
            query = ("SELECT a.client_addr FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid "
                     f"WHERE l.locktype='advisory' AND l.classid={hashed >> 32} "
                     f"AND l.objid={hashed & 0xffffffff} AND l.objsubid=1 AND l.granted")
        else:
            query = ("SELECT SUBSTRING_INDEX(HOST,':',1) FROM information_schema.PROCESSLIST "
                     f"WHERE ID=IS_USED_LOCK('runtime/{runtime}')")
        address = self.sql(self.writer, query)
        matches = [name for name, _ in self.controllers if self.addresses[name] == address]
        if len(matches) != 1:
            return None
        return matches[0] if matches[0] != excluded else None

    def exercise(self):
        self.writer = self.primary
        self.start_controllers()
        self.addresses = {name: json.loads(self.docker("inspect", name))[0]["NetworkSettings"]["Networks"][self.network]["IPAddress"]
                          for name, _ in self.controllers}
        base = self.controllers[0][1]
        pool = self.api(base, "/api/v1/gateway-pools", {"name": "fixture", "backend_network": self.network,
                        "challenge_upstream": "http://controller:8080"})["id"]
        self.container(self.engine, "--privileged", "-e", "DOCKER_TLS_CERTDIR=",
                       "docker:28-dind@sha256:2a232a42256f70d78e3cc5d2b5d6b3276710a0de0596c145f627ecfae90282ac", "--tls=false")
        self.wait(lambda: self.docker("exec", self.engine, "docker", "info"))
        archive = self.work / "nginx.tar"
        self.docker("save", "-o", str(archive), self.images["nginx"])
        with archive.open("rb") as stream:
            result = subprocess.run(["docker", "exec", "-i", self.engine, "docker", "load"],
                                    stdin=stream, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=120)
            if result.returncode:
                raise RuntimeError("fixture.nested_image_load_failed")
        self.docker("exec", self.engine, "docker", "network", "create", self.network)
        self.docker("exec", self.engine, "docker", "run", "-d", "--name", "fixture-app",
                    "--network", self.network, "--network-alias", "fixture_app",
                    "--label", "com.docker.compose.project=fixture", "--label", "com.docker.compose.service=app",
                    "--label", "com.docker.compose.oneoff=False", "--label", "nsc.enable=true",
                    "--label", "nsc.port=80", "--label", "nsc.protocol=http", "--label", "nsc.readiness.mode=docker",
                    "--health-cmd", "test ! -f /tmp/nsc-unhealthy", "--health-interval", "1s",
                    "--health-retries", "1", self.images["nginx"])
        runtime = self.api(base, "/api/v1/runtimes", {"pool_id": pool, "name": "fixture", "runtime_type": "docker",
                           "endpoint": "tcp://" + self.engine + ":2375"})["id"]
        self.wait(lambda: self.observations(runtime) > 0)
        owner = self.wait(lambda: self.owner(runtime))
        self.wait(lambda: self.service_health(1))
        self.partitioned = owner
        self.docker("kill", owner)
        successor = self.wait(lambda: self.owner(runtime, owner))
        self.docker("exec", self.engine, "docker", "exec", "fixture-app", "touch", "/tmp/nsc-unhealthy")
        self.wait(lambda: self.service_health(0))
        self.docker("exec", self.engine, "docker", "exec", "fixture-app", "rm", "/tmp/nsc-unhealthy")
        self.wait(lambda: self.service_health(1))
        self.docker("start", owner)
        self.refresh_ports()
        self.partitioned = ""
        self.wait(lambda: self.api(dict(self.controllers)[owner], "/api/v1/gateway-pools") is not None)
        assert self.owner(runtime) == successor
        self.report["checks"].append("owner_crash_restart_and_docker_health_progression")
        owner = successor
        before = self.observations(runtime)
        self.owner_ip = self.addresses[owner]
        self.partitioned = owner
        # Drop only the owner's owned network; its Docker socket remains available.
        self.docker("network", "disconnect", self.network, owner)
        # A partition can leave the server-side session alive. Explicitly expire
        # those sessions to model bounded DB keepalive/session termination.
        if self.driver == "postgres":
            self.sql(self.primary, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity "
                     f"WHERE client_addr IS NOT NULL AND application_name='' AND pid<>pg_backend_pid() "
                     f"AND client_addr='{self.owner_ip}'")
        else:
            sessions = self.sql(self.primary, "SELECT ID FROM information_schema.PROCESSLIST "
                                f"WHERE USER='nsc' AND HOST LIKE '{self.owner_ip}:%'")
            for session in sessions.splitlines():
                self.sql(self.primary, "KILL " + str(int(session)))
        replacement = self.wait(lambda: self.owner(runtime, owner))
        self.wait(lambda: self.observations(runtime) > before)
        self.docker("network", "connect", self.network, owner)
        self.refresh_ports()
        self.partitioned = ""
        # Restored former owner must not reclaim a newer term.
        time.sleep(3)
        assert self.owner(runtime) == replacement, "former owner reclaimed lease"
        self.report["checks"].append("controller_db_partition_takeover_and_rejoin")
        self.promote()
        before = self.wait(lambda: self.observations(runtime))
        self.wait(lambda: self.observations(runtime) > before)
        for _, peer in self.controllers:
            assert self.api(peer, "/api/v1/gateway-pools/" + pool)["id"] == pool
        self.report["checks"].append("all_controllers_reconnected_to_promoted_writer")

    def promote(self):
        # Quiesce these owned writers, then fence before exposing the new primary.
        # This does not claim lossless unplanned promotion of an async replica.
        for name, _ in self.controllers:
            self.docker("pause", name)
        if self.driver == "postgres":
            watermark = self.sql(self.primary, "SELECT pg_current_wal_flush_lsn()")
            self.wait(lambda: self.sql(self.replica, f"SELECT pg_last_wal_replay_lsn()>='{watermark}'::pg_lsn") == "t")
        else:
            self.sql(self.primary, "SET GLOBAL super_read_only=ON;")
            watermark = self.sql(self.primary, "SELECT @@GLOBAL.gtid_executed")
            assert self.sql(self.replica, f"SELECT WAIT_FOR_EXECUTED_GTID_SET('{watermark}',60)") == "0"
        self.docker("stop", "-t", "10", self.primary)
        self.docker("network", "disconnect", self.network, self.primary)
        if self.driver == "postgres":
            assert self.sql(self.replica, "SELECT pg_promote(true,60)") == "t"
        else:
            self.sql(self.replica, "STOP REPLICA; RESET REPLICA ALL; SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF;")
        self.docker("network", "disconnect", self.network, self.replica)
        self.docker("network", "connect", "--alias", self.alias, self.network, self.replica)
        self.writer = self.replica
        for name, _ in self.controllers:
            self.docker("unpause", name)
        self.report["checks"].append("old_primary_fenced_watermark_replayed_replica_promoted")

    def execute(self):
        try:
            self.encryption_keys = json.dumps({"v1": base64.b64encode(os.urandom(32)).decode()})
            self.database()
            self.exercise()
            self.report["status"] = "PASS"
        finally:
            for name in reversed(self.names):
                self.docker("rm", "-fv", name, check=False)
            self.docker("network", "rm", self.network, check=False)
            self.output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            descriptor = os.open(self.output, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
            with os.fdopen(descriptor, "w") as stream:
                json.dump(self.report, stream, indent=2)
            import shutil
            shutil.rmtree(self.work)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("driver", choices=("postgres", "mysql"))
    parser.add_argument("--image", required=True)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    Fixture(args.driver, args.image, args.output).execute()


if __name__ == "__main__":
    main()
