#!/usr/bin/env python3
"""Exercise tracked database installation profiles in an owned nested daemon.

Only the summary is suitable for CI artifacts. Copied configuration, resolved
Compose files, credentials, PKI and command diagnostics remain private.
"""

import argparse
import base64
import datetime
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

from gateway_acceptance import confirms_gateway


def private_json(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')
    path.chmod(0o600)


class InstallationFixture:
    def __init__(self, args):
        self.args = args
        self.root = Path(__file__).resolve().parent.parent
        self.output = Path(args.output).resolve()
        self.output.mkdir(parents=True, exist_ok=False, mode=0o700)
        self.output.chmod(0o700)
        self.lock_path = self.root / 'build/image-lock.json'
        self.images = json.loads(self.lock_path.read_text())['images']
        self.suffix = uuid.uuid4().hex[:10]
        self.daemon = 'nsc-installation-daemon-' + self.suffix
        self.network = self.daemon + '-net'
        self.created_network = self.created_daemon = False
        self.sequence = 0
        self.env = os.environ.copy()
        # HTTP probes address only the owned daemon's private bridge address.
        self.http = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.summary = {'format': 1, 'status': 'FAIL', 'topologies': [], 'ovh': 'DISABLED',
                        'started_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                        'scope': 'TRACKED_COMPOSE_INSTALLATION_IN_ISOLATED_NESTED_DOCKER',
                        'limitations': ['One Docker host is used; these checks do not prove multi-host failure tolerance.',
                                        'HTTP routing and persisted restart are checked; long-connection migration and cross-version upgrade are not.']}

    def run(self, *args, env=None, check=True, stdin=None):
        self.sequence += 1
        result = subprocess.run(args, env=env, stdin=stdin, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=240)
        if result.returncode:
            path = self.output / f'command-{self.sequence}-error.log'
            path.write_bytes(result.stdout)
            path.chmod(0o600)
            if check:
                raise RuntimeError(f'isolated fixture command failed (exit {result.returncode}); private diagnostics retained')
        return result

    def docker(self, *args, **kwargs):
        return self.run('docker', *args, **kwargs)

    def nested(self, *args, **kwargs):
        return self.docker(*args, env=self.env, **kwargs)

    def wait(self, probe, label, timeout=120):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            try:
                result = probe()
                if result:
                    return result
            except (urllib.error.URLError, OSError):
                pass
            time.sleep(0.5)
        raise RuntimeError('isolated installation timed out: ' + label)

    def pki(self, fixture, *args):
        self.docker('run', '--rm', '--network', 'none', '--user', f'{os.getuid()}:{os.getgid()}',
                    '-v', f'{fixture}:/fixture', self.controller_image, 'pki', *args)

    def stage(self, source, destination):
        # DIND's runtime /tmp can mask host bind paths. Explicitly stage only
        # this fixture's files in /opt; never mount a host Docker socket.
        self.docker('exec', self.daemon, 'mkdir', '-p', destination)
        self.docker('cp', str(source) + '/.', self.daemon + ':' + destination)

    def resolve_images(self, config):
        # The locked host reference established each image's authority before
        # save/load. Resolve only those verified images in private fixture files;
        # the tracked Compose source and every other service option stay intact.
        for name, service in config['services'].items():
            reference = service.get('image')
            if reference not in self.image_ids:
                raise RuntimeError('installation service uses an unverified image: ' + name)
            service['image'] = self.image_ids[reference]

    def start(self):
        names = ('compose.yaml', 'config/controller.yaml', 'config/agent.yaml', 'test/e2e/compose-app.yaml',
                 'scripts/e2e-installation.py', 'scripts/gateway_acceptance.py')
        self.summary['source_sha256'] = {name: hashlib.sha256((self.root / name).read_bytes()).hexdigest() for name in names}
        self.summary['source_commit'] = self.run('git', 'rev-parse', 'HEAD').stdout.decode().strip()
        self.summary['image_lock_sha256'] = hashlib.sha256(self.lock_path.read_bytes()).hexdigest()
        self.controller_image = self.docker('image', 'inspect', '-f', '{{.Id}}', self.args.controller_image).stdout.decode().strip()
        self.gateway_image = self.docker('image', 'inspect', '-f', '{{.Id}}', self.args.gateway_image).stdout.decode().strip()
        self.summary['controller_image_id'] = self.controller_image
        self.summary['gateway_image_id'] = self.gateway_image
        self.summary['dind_image_reference'] = self.images['docker_dind']
        self.image_ids = {self.controller_image: self.controller_image, self.gateway_image: self.gateway_image}
        self.summary['locked_dependencies'] = []
        for name in ('postgres', 'mysql', 'nginx'):
            reference = self.images[name]
            identifier = self.docker('image', 'inspect', '-f', '{{.Id}}', reference).stdout.decode().strip()
            if not identifier.startswith('sha256:') or len(identifier) != 71:
                raise RuntimeError('locked installation image has an invalid content ID: ' + name)
            self.image_ids[reference] = identifier
            self.summary['locked_dependencies'].append({'name': name, 'reference': reference, 'host_image_id': identifier})
        self.docker('network', 'create', '--label', 'io.network-service-composer.e2e=true', self.network)
        self.created_network = True
        self.docker('run', '-d', '--privileged', '--name', self.daemon, '--network', self.network,
                    '--label', 'io.network-service-composer.e2e=true', '-e', 'DOCKER_TLS_CERTDIR=',
                    self.images['docker_dind'], '--tls=false')
        self.created_daemon = True
        metadata = json.loads(self.docker('inspect', self.daemon).stdout)[0]
        if metadata['HostConfig'].get('PortBindings') or metadata['Mounts'] and any(m.get('Type') == 'bind' for m in metadata['Mounts']):
            raise RuntimeError('owned nested daemon acquired published ports or host bind mounts')
        self.host = metadata['NetworkSettings']['Networks'][self.network]['IPAddress']
        self.env['DOCKER_HOST'] = 'tcp://' + self.host + ':2375'
        self.wait(lambda: self.nested('info', check=False).returncode == 0, 'owned nested daemon')
        self.socket_gid = self.docker('exec', self.daemon, 'stat', '-c', '%g', '/var/run/docker.sock').stdout.decode().strip()
        archive = self.output / 'fixture-images.tar'
        references = list(dict.fromkeys(self.image_ids.values()))
        self.docker('save', '-o', str(archive), *references)
        archive.chmod(0o600)
        with archive.open('rb') as source:
            self.docker('exec', '-i', self.daemon, 'docker', 'load', stdin=source)
        # Docker save/load omits canonical RepoDigests. Verify its retained
        # content IDs instead of adding an external registry dependency here.
        for identifier in references:
            loaded = self.nested('image', 'inspect', '-f', '{{.Id}}', identifier).stdout.decode().strip()
            if loaded != identifier:
                raise RuntimeError('nested installation image differs from its verified host image')
        for dependency in self.summary['locked_dependencies']:
            dependency['loaded_image_id'] = self.image_ids[dependency['reference']]
            dependency['verified_loaded_content'] = True

    def topology(self, driver, index):
        result = {'driver': driver, 'status': 'FAIL', 'checks': []}
        self.summary['topologies'].append(result)
        fixture = self.output / driver
        fixture.mkdir(mode=0o700)
        (fixture / 'config').mkdir()
        for name in ('controller.yaml', 'agent.yaml'):
            shutil.copyfile(self.root / 'config' / name, fixture / 'config' / name)
            # The tracked non-root processes use the fixture owner's secret group.
            (fixture / 'config' / name).chmod(0o440)
        controller_secrets = fixture / 'deploy/secrets/controller'
        agent_secrets = fixture / 'deploy/secrets/gateway'
        controller_secrets.mkdir(parents=True)
        agent_secrets.mkdir()
        for directory in (controller_secrets, agent_secrets):
            directory.chmod(0o750)
        self.pki(fixture, 'init-ca', '--cert', '/fixture/ca.crt', '--key', '/fixture/ca.key', '--valid-for', '24h')
        self.pki(fixture, 'issue-controller', '--instance-id', 'controller', '--ca-cert', '/fixture/ca.crt', '--ca-key', '/fixture/ca.key',
                 '--cert', '/fixture/deploy/secrets/controller/controller.crt', '--key', '/fixture/deploy/secrets/controller/controller.key',
                 '--dns', 'controller', '--dns', 'controller-mysql', '--dns', 'controller-sqlite', '--valid-for', '2h')
        for directory in (controller_secrets, agent_secrets):
            shutil.copyfile(fixture / 'ca.crt', directory / 'ca.crt')
        for path in controller_secrets.iterdir():
            path.chmod(0o440)
        admin = secrets.token_urlsafe(32)
        ingress = 'nsc-installation-ingress-' + driver + '-' + self.suffix
        project = 'nscinstall' + driver + self.suffix
        profile = 'gateway' if driver == 'postgres' else driver
        environment = self.env.copy()
        environment.update({'NSC_CONTROLLER_IMAGE': self.controller_image, 'NSC_GATEWAY_IMAGE': self.gateway_image,
            'NSC_ADMIN_TOKEN': admin, 'NSC_SESSION_KEY': secrets.token_hex(32),
            'NSC_ENCRYPTION_KEY_B64': base64.b64encode(secrets.token_bytes(32)).decode(), 'NSC_DATABASE_PASSWORD': secrets.token_hex(24),
            'NSC_SECRET_GID': str(os.getgid()), 'DOCKER_GID': self.socket_gid, 'NSC_ADMIN_PORT': '0', 'NSC_GRPC_PORT': '0', 'NSC_METRICS_PORT': '0',
            'NSC_HTTP_PORT': str(41000 + index * 10), 'NSC_HTTPS_PORT': str(41001 + index * 10),
            'NSC_INGRESS_NETWORK': ingress, 'NSC_HTTP_BIND': '0.0.0.0', 'NSC_HTTPS_BIND': '0.0.0.0'})
        config = json.loads(self.docker('compose', '--project-directory', str(fixture), '--profile', profile, '-f',
                                       str(self.root / 'compose.yaml'), 'config', '--format', 'json', env=environment).stdout)
        self.resolve_images(config)
        remote = '/opt/nsc-installation/' + driver
        self.stage(fixture, remote)
        for service in config['services'].values():
            for volume in service.get('volumes', []):
                if volume.get('type') == 'bind' and volume['source'].startswith(str(fixture) + '/'):
                    volume['source'] = remote + volume['source'][len(str(fixture)):]
        # Preserve tracked service/environment/security/volume semantics. Only
        # private source bind paths, verified image IDs and ephemeral host ports
        # differ in the clone.
        config_file = fixture / 'resolved-private.json'
        private_json(config_file, config)
        cli = ['compose', '-p', project, '--profile', profile, '-f', str(config_file)]
        def compose(*args, **kwargs):
            return self.docker(*cli, *args, env=environment, **kwargs)
        controller = 'controller' if driver == 'postgres' else 'controller-' + driver
        databases = [] if driver == 'sqlite' else ['postgres' if driver == 'postgres' else 'mysql']
        backend_project = project + 'backend'
        backend_env = environment.copy()
        backend_env.update({'NSC_E2E_NETWORK': ingress, 'NSC_E2E_SERVICE_ALIAS': backend_project + '_app'})
        backend = json.loads(self.docker('compose', '-p', backend_project, '-f',
                             str(self.root / 'test/e2e/compose-app.yaml'), 'config', '--format', 'json', env=backend_env).stdout)
        self.resolve_images(backend)
        backend_file = fixture / 'backend-resolved-private.json'
        private_json(backend_file, backend)
        created_ingress = created_compose = created_backend = False
        try:
            self.nested('network', 'create', ingress)
            created_ingress = True
            created_compose = True
            compose('up', '-d', '--no-build', *databases, controller)
            def refresh_api():
                identifier = compose('ps', '-q', controller).stdout.decode().strip()
                metadata = json.loads(self.nested('inspect', identifier).stdout)[0]
                self.api = 'http://' + self.host + ':' + metadata['NetworkSettings']['Ports']['8080/tcp'][0]['HostPort']
                return identifier
            refresh_api()
            def ready():
                with self.http.open(self.api + '/readyz', timeout=2) as response:
                    return response.status == 200
            self.wait(ready, 'Controller ready')
            def request(method, path, body=None):
                data = None if body is None else json.dumps(body).encode()
                req = urllib.request.Request(self.api + '/api/v1' + path, method=method, data=data,
                    headers={'Authorization': 'Bearer ' + admin, 'Content-Type': 'application/json'})
                with self.http.open(req, timeout=10) as response:
                    return json.load(response)['data']
            pool = request('POST', '/gateway-pools', {'name': driver + ' installation', 'backend_network': ingress,
                                                     'challenge_upstream': 'http://' + controller + ':8080'})
            gateway = self.docker('run', '--rm', '--network', 'none', self.controller_image, 'id', 'gateway').stdout.decode().strip()
            self.pki(fixture, 'issue-agent', '--ca-cert', '/fixture/ca.crt', '--ca-key', '/fixture/ca.key',
                     '--cert', '/fixture/deploy/secrets/gateway/agent.crt', '--key', '/fixture/deploy/secrets/gateway/agent.key',
                     '--pool-id', pool['id'], '--gateway-id', gateway, '--valid-for', '2h')
            for path in agent_secrets.iterdir():
                path.chmod(0o440)
            self.stage(agent_secrets, remote + '/deploy/secrets/gateway')
            service = controller if driver == 'sqlite' else ('gateway' if driver == 'postgres' else 'gateway-mysql')
            config['services'][service]['environment'].update({'NSC_AGENT__POOL_ID': pool['id'], 'NSC_AGENT__GATEWAY_ID': gateway})
            if driver == 'sqlite':
                config['services'][service]['environment']['NSC_CONTROLLER__LOCAL_GATEWAY'] = 'true'
            private_json(config_file, config)
            compose('up', '-d', '--no-build', service)
            refresh_api()
            self.wait(ready, 'Controller after identity provisioning')
            request('POST', '/runtimes', {'pool_id': pool['id'], 'name': 'owned nested daemon', 'runtime_type': 'docker',
                                         'endpoint': 'unix:///var/run/docker.sock'})
            started = time.time()
            created_backend = True
            self.docker('compose', '-p', backend_project, '-f', str(backend_file), 'up', '-d', env=backend_env)
            def loaded(after):
                for entry in request('GET', '/gateways?limit=200'):
                    revision = entry.get('desired_config_revision', 0)
                    if entry['id'] == gateway and revision > 0 and confirms_gateway(entry, revision, after):
                        return entry
                return None
            evidence = self.wait(lambda: loaded(started), 'current loaded and next-start Gateway evidence')
            private_json(fixture / 'gateway-evidence.json', evidence)
            gateway_container = compose('ps', '-q', service).stdout.decode().strip()
            metadata = json.loads(self.nested('inspect', gateway_container).stdout)[0]
            http_port = metadata['NetworkSettings']['Ports']['80/tcp'][0]['HostPort']
            def traffic():
                req = urllib.request.Request('http://' + self.host + ':' + http_port + '/', headers={'Host': 'compose.e2e.test'})
                with self.http.open(req, timeout=4) as response:
                    return response.status == 200 and b'Welcome to nginx' in response.read()
            self.wait(traffic, 'real routed business HTTP')
            uid = self.nested('exec', gateway_container, 'id', '-u').stdout.decode().strip()
            if not metadata['HostConfig']['ReadonlyRootfs'] or uid == '0':
                raise RuntimeError('tracked Gateway did not retain its non-root read-only contract')
            result['checks'].extend(['tracked_compose_profile_started', 'correct_leader_store_and_service_address',
                                     'fresh_loaded_revision_model_rendered_disk_digest_and_next_start', 'real_routed_http_200', 'nonroot_readonly_gateway'])
            compose('stop', controller)
            if driver == 'sqlite':
                unavailable = False
                try:
                    traffic()
                except (urllib.error.URLError, OSError):
                    unavailable = True
                if not unavailable:
                    raise RuntimeError('embedded Gateway served after Controller stop')
                result['checks'].append('documented_embedded_controller_gateway_outage')
            else:
                for _ in range(3):
                    if not traffic():
                        raise RuntimeError('independent Gateway stopped serving during Controller outage')
                compose('restart', service)
                self.wait(traffic, 'independent Gateway persisted cold start with Controller offline')
                result['checks'].extend(['controller_offline_serving', 'independent_gateway_persisted_cold_start'])
            recovered = time.time()
            compose('start', controller)
            refresh_api()
            self.wait(ready, 'Controller restart')
            self.wait(traffic, 'business routing after restart')
            self.wait(lambda: loaded(recovered), 'fresh evidence after Controller recovery')
            result['checks'].append('persistent_restart_and_fresh_reconnection')
            result['status'] = 'PASS'
        finally:
            cleanup = []
            if created_compose:
                log = fixture / 'diagnostics.log'
                log.write_bytes(compose('logs', '--no-color', check=False).stdout)
                log.chmod(0o600)
            if created_backend:
                cleanup.append(self.docker('compose', '-p', backend_project, '-f', str(backend_file), 'down', '--volumes', env=backend_env, check=False).returncode)
            if created_compose:
                cleanup.append(compose('down', '--volumes', '--remove-orphans', check=False).returncode)
            if created_ingress:
                cleanup.append(self.nested('network', 'rm', ingress, check=False).returncode)
            result['cleanup_exit_codes'] = cleanup
            if any(cleanup):
                result['status'] = 'FAIL'
        if result['status'] != 'PASS':
            raise RuntimeError(driver + ' tracked installation failed; private diagnostics retained')

    def cleanup(self):
        codes = []
        if self.created_daemon:
            codes.append(self.docker('rm', '-fv', self.daemon, check=False).returncode)
        if self.created_network:
            codes.append(self.docker('network', 'rm', self.network, check=False).returncode)
        self.summary['cleanup_exit_codes'] = codes
        if any(codes):
            self.summary['status'] = 'FAIL'
            self.summary['error'] = 'owned installation fixture resources did not clean up'
        self.summary['finished_at'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        private_json(self.output / 'summary.json', self.summary)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--controller-image', required=True, help='current full production Controller image including Docker CLI/Compose')
    parser.add_argument('--gateway-image', required=True, help='current production Gateway image')
    parser.add_argument('--output', required=True, help='new private evidence directory; only summary.json is suitable for CI artifacts')
    args = parser.parse_args()
    fixture = InstallationFixture(args)
    def cancelled(_number, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, cancelled)
    try:
        fixture.start()
        for index, driver in enumerate(('postgres', 'mysql', 'sqlite'), 1):
            fixture.topology(driver, index)
        fixture.summary['status'] = 'PASS'
    except KeyboardInterrupt:
        fixture.summary['error'] = 'installation fixture cancelled'
    except Exception as error:
        fixture.summary['error'] = str(error)
    finally:
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        fixture.cleanup()
    print('installation_e2e status=' + fixture.summary['status'] + ' report=' + str(fixture.output / 'summary.json'))
    return 0 if fixture.summary['status'] == 'PASS' else 1


if __name__ == '__main__':
    sys.exit(main())
