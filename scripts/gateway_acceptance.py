#!/usr/bin/env python3
"""Real NSC Gateway acceptance in a private Docker network; no host socket.

The TCP balancer is a test load balancer with explicit drain and HTTP health
checks. It is not evidence about any particular production load balancer.
"""

import argparse
import base64
import datetime
import hashlib
import http.client
import json
import os
from pathlib import Path
import select
import secrets
import shutil
import socket
import socketserver
import ssl
import subprocess
import tempfile
import threading
import time
from types import SimpleNamespace


HOST = 'gateway.acceptance.test'
BODY = b'gateway-acceptance\n'


def utc_now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def confirms_gateway(gateway, revision, after):
    """Require current loaded and next-start evidence, not merely READY."""
    evidence = gateway.get('evidence') or {}
    try:
        observed = datetime.datetime.fromisoformat(evidence['observed_at'].replace('Z', '+00:00')).timestamp()
    except (KeyError, ValueError):
        return False
    digests = [gateway.get('model_digest'), evidence.get('model_digest'),
               evidence.get('rendered_digest'), evidence.get('disk_digest'), gateway.get('rendered_digest')]
    return (gateway.get('enabled') is True and gateway.get('status') == 'READY'
            and gateway.get('desired_config_revision') == revision
            and gateway.get('applied_config_revision') == revision
            and evidence.get('confirmed_revision') == revision
            and evidence.get('last_confirmed_revision') == revision
            and evidence.get('loading_confirmed') is True
            and evidence.get('startup_state') == 'CONFIRMED'
            and evidence.get('configuration_state') == 'CONFIRMED'
            and observed >= after
            and all(isinstance(digest, str) and len(digest) == 64
                    and all(character in '0123456789abcdef' for character in digest) for digest in digests)
            and digests[0] == digests[1] and digests[2] == digests[3] == digests[4])


def request(port, tls_context=None, path='/'):
    connection = (http.client.HTTPSConnection('127.0.0.1', port, context=tls_context, timeout=3)
                  if tls_context else http.client.HTTPConnection('127.0.0.1', port, timeout=3))
    # Connect explicitly so TLS verifies the declared domain, not the fixture IP.
    if tls_context:
        raw = socket.create_connection(('127.0.0.1', port), timeout=3)
        connection.sock = tls_context.wrap_socket(raw, server_hostname=HOST)
    try:
        connection.request('GET', path, headers={'Host': HOST, 'Connection': 'close'})
        response = connection.getresponse()
        content = response.read()
        if response.status != 200 or content != BODY:
            raise AssertionError('unexpected business response')
        return dict(response.getheaders())
    finally:
        connection.close()


class Balancer:
    """TCP pass-through, bounded health checks, and explicit per-node drain."""

    def __init__(self, backends):
        self.backends = backends
        self.lock = threading.Lock()
        self.drained = set()
        self.healthy = set(range(len(backends)))
        self.active = [0] * len(backends)
        self.sequence = 0
        self.stop = threading.Event()
        owner = self

        class Handler(socketserver.BaseRequestHandler):
            def handle(self):
                upstream = None
                with owner.lock:
                    choices = sorted(owner.healthy - owner.drained)
                    if not choices:
                        return
                    index = choices[owner.sequence % len(choices)]
                    owner.sequence += 1
                    owner.active[index] += 1
                try:
                    upstream = socket.create_connection(('127.0.0.1', owner.backends[index][self.server.scheme]), timeout=2)
                    pair = [self.request, upstream]
                    while not owner.stop.is_set():
                        readable, _, _ = select.select(pair, [], [], 0.25)
                        for source in readable:
                            data = source.recv(65536)
                            if not data:
                                return
                            pair[1 if source is pair[0] else 0].sendall(data)
                except (OSError, TimeoutError):
                    return
                finally:
                    if upstream:
                        upstream.close()
                    with owner.lock:
                        owner.active[index] -= 1

        class Server(socketserver.ThreadingTCPServer):
            allow_reuse_address = True
            daemon_threads = True

        self.servers = []
        self.ports = {}
        for scheme in ('http', 'https'):
            server = Server(('127.0.0.1', 0), Handler)
            server.scheme = scheme
            self.servers.append(server)
            self.ports[scheme] = server.server_address[1]
            threading.Thread(target=server.serve_forever, daemon=True).start()
        self.health = threading.Thread(target=self.watch_health, daemon=True)
        self.health.start()

    def watch_health(self):
        while not self.stop.is_set():
            for index, backend in enumerate(self.backends):
                try:
                    request(backend['http'])
                    valid = True
                except (OSError, http.client.HTTPException, AssertionError):
                    valid = False
                with self.lock:
                    if valid:
                        self.healthy.add(index)
                    else:
                        self.healthy.discard(index)
            self.stop.wait(0.1)

    def drain(self, index):
        with self.lock:
            self.drained.add(index)
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            with self.lock:
                if self.active[index] == 0:
                    return
            time.sleep(0.05)
        raise AssertionError('bounded streams failed to drain')

    def admit(self, index):
        with self.lock:
            self.healthy.add(index)
            self.drained.discard(index)

    def close(self):
        self.stop.set()
        for server in self.servers:
            server.shutdown()
            server.server_close()
        self.health.join(timeout=4)


class Sampler:
    def __init__(self, balancer, tls_context):
        self.balancer = balancer
        self.tls_context = tls_context
        self.stop = threading.Event()
        self.samples = []
        self.thread = threading.Thread(target=self.run, daemon=True)
        self.thread.start()

    def run(self):
        while not self.stop.is_set():
            for scheme in ('http', 'https'):
                started = time.monotonic()
                try:
                    request(self.balancer.ports[scheme], self.tls_context if scheme == 'https' else None)
                    result = 'PASS'
                except (OSError, http.client.HTTPException, AssertionError):
                    result = 'FAIL'
                self.samples.append({'at': started, 'scheme': scheme, 'result': result,
                                     'duration_seconds': time.monotonic() - started})
            self.stop.wait(0.05)

    def close(self):
        self.stop.set()
        self.thread.join(timeout=7)
        if self.thread.is_alive():
            raise AssertionError('request sampler did not stop')

    def report(self, start, end, fault=False):
        return summarize_samples(self.samples, start, end, fault)


def summarize_samples(samples, start, end, fault=False):
    selected = [sample for sample in samples if start <= sample['at'] <= end]
    counts = {scheme: {'total': 0, 'failed': 0} for scheme in ('http', 'https')}
    for sample in selected:
        counts[sample['scheme']]['total'] += 1
        counts[sample['scheme']]['failed'] += sample['result'] != 'PASS'
    assert all(value['total'] >= 10 for value in counts.values()), 'insufficient traffic samples'
    if not fault:
        assert all(value['failed'] == 0 for value in counts.values()), 'normal operation interrupted traffic'
    else:
        for scheme in counts:
            latest = [sample for sample in selected if sample['scheme'] == scheme][-5:]
            assert all(sample['result'] == 'PASS' for sample in latest), 'faulted business traffic did not recover'
    return {'start_monotonic': start, 'end_monotonic': end, 'duration_seconds': end - start, 'requests': counts,
            'fault_injection': fault, 'maximum_request_seconds': max(sample['duration_seconds'] for sample in selected)}


class Stream:
    def __init__(self, port, websocket=False):
        self.socket = socket.create_connection(('127.0.0.1', port), timeout=6)
        self.socket.settimeout(6)
        path = '/socket' if websocket else '/events'
        extra = ('Connection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\n'
                 'Sec-WebSocket-Key: YWNjZXB0YW5jZS1maXh0dXJl\r\n' if websocket else 'Connection: close\r\n')
        self.socket.sendall(f'GET {path} HTTP/1.1\r\nHost: {HOST}\r\n{extra}\r\n'.encode())
        content = b''
        while b'\r\n\r\n' not in content:
            content += self.socket.recv(4096)
        headers, self.initial = content.split(b'\r\n\r\n', 1)
        assert (b'101 Switching Protocols' if websocket else b'200 OK') in headers
        self.websocket = websocket

    def finish(self):
        content = self.initial
        try:
            while True:
                chunk = self.socket.recv(4096)
                if not chunk:
                    break
                content += chunk
        finally:
            self.socket.close()
        if self.websocket:
            for sequence in range(21):
                assert f'tick-{sequence}'.encode() in content, 'WebSocket stream truncated'
        else:
            for sequence in range(21):
                assert f'data: {sequence}\n\n'.encode() in content, 'SSE stream truncated'


class Fixture:
    def __init__(self, args):
        self.repository = Path(__file__).resolve().parent.parent
        self.args = args
        self.work = Path(tempfile.mkdtemp(prefix='nsc-gateway-acceptance-'))
        self.work.chmod(0o700)
        self.suffix = self.work.name.rsplit('-', 1)[-1]
        self.network = 'nsc-gateway-acceptance-' + self.suffix
        self.controller = self.network + '-controller'
        self.backend = self.network + '-backend'
        self.database = self.network + '-database'
        self.containers = []
        self.balancer = None
        self.sampler = None
        self.lb_processes = []
        self.lb_samples = []
        self.admin_token = secrets.token_urlsafe(32)
        self.database_password = secrets.token_urlsafe(24)
        self.stage = 'prerequisites'
        self.summary = {'schema_version': 1, 'started_at': utc_now(), 'status': 'RUNNING',
                        'scenarios': [], 'limitations': [
                            'Docker replica replacement uses explicit drain; Swarm scheduling is checked separately.',
                            'The test balancer is not a production LB acceptance result.',
                            'HTTP/1.1, TLS, bounded SSE and WebSocket are tested; HTTP/2 and HTTP/3 are not tested.',
                            'The same immutable Gateway image is recreated; cross-version upgrade is not tested.']}

    def command(self, *arguments):
        result = subprocess.run(arguments, capture_output=True, text=True, timeout=180)
        if result.returncode:
            (self.work / 'command-error.log').write_text(result.stderr)
            raise AssertionError(f'fixture command failed with exit code {result.returncode}')
        return result.stdout.strip()

    def docker(self, *arguments):
        return self.command('docker', *arguments)

    def cli(self, *arguments):
        return self.docker('run', '--rm', '--user', '0', '-v', f'{self.work}:/fixture',
                           self.args.controller_image, *arguments)

    def run(self, name, image, environment=None, arguments=(), mounts=(), ports=()):
        self.containers.append(name)
        command = ['run', '-d', '--name', name, '--network', self.network,
                   '--label', 'io.network-service-composer.e2e=true']
        for port in ports:
            command.extend(['-p', f'127.0.0.1::{port}'])
        for source, target in mounts:
            command.extend(['-v', f'{source}:{target}'])
        for key, value in (environment or {}).items():
            command.extend(['-e', f'{key}={value}'])
        self.docker(*command, image, *arguments)

    def port(self, name, port):
        value = json.loads(self.docker('inspect', name))[0]['NetworkSettings']['Ports'][f'{port}/tcp']
        return int(value[0]['HostPort'])

    def api(self, method, path, body=None, revision=None, idem=None):
        connection = http.client.HTTPConnection('127.0.0.1', self.api_port, timeout=10)
        headers = {'Authorization': 'Bearer ' + self.admin_token, 'Content-Type': 'application/json'}
        if revision is not None:
            headers['If-Match'] = f'"rev-{revision}"'
        if idem:
            headers['Idempotency-Key'] = idem
        try:
            connection.request(method, '/api/v1' + path, json.dumps(body) if body is not None else None, headers)
            response = connection.getresponse()
            payload = json.loads(response.read())
            if response.status >= 400:
                (self.work / 'api-error.json').write_text(json.dumps(payload, indent=2))
                raise AssertionError(f'API {method} {path} returned {response.status}: {payload.get("code", "unknown")}')
            return payload['data']
        finally:
            connection.close()

    def wait(self, predicate, label, timeout=90):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                result = predicate()
                if result:
                    return result
            except (OSError, http.client.HTTPException):
                pass
            time.sleep(0.2)
        raise AssertionError('timed out: ' + label)

    def ready(self):
        connection = http.client.HTTPConnection('127.0.0.1', self.api_port, timeout=2)
        try:
            connection.request('GET', '/readyz')
            response = connection.getresponse()
            response.read()
            return response.status == 200
        finally:
            connection.close()

    def prepare(self):
        images = json.loads((self.repository / 'build/image-lock.json').read_text())['images']
        self.cleanup_image = images['alpine']
        self.summary['source_commit'] = self.command('git', '-C', str(self.repository), 'rev-parse', 'HEAD')
        self.summary['fixture_sha256'] = {
            name: hashlib.sha256((self.repository / name).read_bytes()).hexdigest()
            for name in ('scripts/gateway_acceptance.py', 'scripts/gateway_lb_fixture.py', 'test/e2e/gateway-backend.mjs')}
        self.summary['images'] = {label: {'reference': image, 'id': self.docker('image', 'inspect', '-f', '{{.Id}}', image)}
                                 for label, image in [('controller', self.args.controller_image),
                                                      ('gateway', self.args.gateway_image), ('backend', images['node_frontend_builder']),
                                                      ('postgres', images['postgres'])]}
        self.docker('network', 'create', '--label', 'io.network-service-composer.e2e=true', self.network)
        for directory in ('secrets', 'data', 'gateway-0', 'gateway-1', 'sandbox'):
            (self.work / directory).mkdir(mode=0o777)
            (self.work / directory).chmod(0o777)
        self.stage = 'pki'
        self.cli('pki', 'init-ca', '--cert', '/fixture/secrets/ca.crt', '--key', '/fixture/secrets/ca.key',
                 '--common-name', 'Gateway acceptance CA', '--valid-for', '24h')
        self.cli('pki', 'issue-controller', '--instance-id', 'controller', '--ca-cert', '/fixture/secrets/ca.crt',
                 '--ca-key', '/fixture/secrets/ca.key', '--cert', '/fixture/secrets/controller.crt',
                 '--key', '/fixture/secrets/controller.key', '--dns', self.controller, '--valid-for', '2h')
        self.command('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                     '-subj', f'/CN={HOST}', '-addext', f'subjectAltName=DNS:{HOST}',
                     '-addext', 'basicConstraints=critical,CA:FALSE',
                     '-addext', 'keyUsage=critical,digitalSignature,keyEncipherment',
                     '-addext', 'extendedKeyUsage=serverAuth',
                     '-keyout', str(self.work / 'tls.key'), '-out', str(self.work / 'tls.crt'))
        self.docker('run', '--rm', '--user', '0', '-v', f'{self.work}:/fixture', self.cleanup_image,
                    'sh', '-c', 'chmod 644 /fixture/secrets/*')
        self.stage = 'controller'
        self.run(self.database, images['postgres'], {'POSTGRES_USER': 'nsc', 'POSTGRES_PASSWORD': self.database_password,
                                                   'POSTGRES_DB': 'nsc'})
        self.wait(lambda: subprocess.run(['docker', 'exec', self.database, 'pg_isready', '-h', '127.0.0.1', '-U', 'nsc', '-d', 'nsc'],
                                        capture_output=True).returncode == 0, 'isolated database')
        self.dsn = f'postgres://nsc:{self.database_password}@{self.database}:5432/nsc?sslmode=disable'
        environment = {
            'NSC_STORAGE__DRIVER': 'postgres', 'NSC_STORAGE__HA': 'true', 'NSC_STORAGE__DSN': self.dsn,
            'NSC_CONTROLLER__INSTANCE_ID': 'controller',
            'NSC_CONTROLLER__INTERNAL__GRPC_ADDRESS': self.controller + ':9443',
            'NSC_CONTROLLER__INTERNAL__URL': 'https://' + self.controller + ':9444',
            'NSC_CONTROLLER__INTERNAL__LISTEN': ':9444',
            'NSC_CONTROLLER__INTERNAL__CA_FILE': '/run/nsc/ca.crt',
            'NSC_CONTROLLER__INTERNAL__CERT_FILE': '/run/nsc/controller.crt',
            'NSC_CONTROLLER__INTERNAL__KEY_FILE': '/run/nsc/controller.key',
            'NSC_MANAGEMENT__ENABLED': 'true', 'NSC_SECURITY__ADMIN_TOKEN': self.admin_token,
            'NSC_SECURITY__SESSION_KEY': secrets.token_hex(16),
            'NSC_SECURITY__PRIMARY_KEY_ID': 'v1',
            'NSC_SECURITY__ENCRYPTION_KEYS': json.dumps({'v1': base64.b64encode(secrets.token_bytes(32)).decode()}),
            'NSC_SERVER__PUBLIC_URL': f'http://{self.controller}:8080',
            'NSC_SERVER__GRPC_TLS_CERT_FILE': '/run/nsc/controller.crt',
            'NSC_SERVER__GRPC_TLS_KEY_FILE': '/run/nsc/controller.key',
            'NSC_SERVER__GRPC_CLIENT_CA_FILE': '/run/nsc/ca.crt',
            'NSC_CONTROLLER__GATEWAY__PERIODIC_INTERVAL': '1s',
            'NSC_CONTROLLER__GATEWAY__OFFLINE_TIMEOUT': '5s'}
        self.run(self.controller, self.args.controller_image, environment, ('serve', '--config='),
                 [(self.work / 'secrets', '/run/nsc:ro'), (self.work / 'data', '/var/lib/nsc')], [8080])
        self.api_port = self.port(self.controller, 8080)
        self.wait(self.ready, 'controller ready')
        self.pool = self.api('POST', '/gateway-pools', {'name': 'gateway-acceptance',
                             'backend_network': self.network, 'challenge_upstream': f'http://{self.controller}:8080'})
        self.certificate = self.api('POST', '/certificates', {
            'pool_id': self.pool['id'], 'certificate_pem': (self.work / 'tls.crt').read_text(),
            'private_key_pem': (self.work / 'tls.key').read_text()})
        self.domain = self.api('POST', '/domains', {'pool_id': self.pool['id'], 'host': HOST,
            'entrypoints': ['http', 'https'], 'tls': {'mode': 'custom', 'certificate_id': self.certificate['id']}})
        self.run(self.backend, images['node_frontend_builder'], arguments=('node', '/fixture/backend.mjs'),
                 mounts=[(self.repository / 'test/e2e/gateway-backend.mjs', '/fixture/backend.mjs:ro')])
        self.gateways = []
        for index in range(3):
            identity = self.cli('id', 'gateway')
            directory = 'sandbox' if index == 2 else f'gateway-{index}'
            self.cli('pki', 'issue-agent', '--ca-cert', '/fixture/secrets/ca.crt', '--ca-key', '/fixture/secrets/ca.key',
                     '--cert', f'/fixture/secrets/{directory}.crt', '--key', f'/fixture/secrets/{directory}.key',
                     '--pool-id', self.pool['id'], '--gateway-id', identity, '--valid-for', '2h')
            self.gateways.append({'id': identity, 'directory': directory, 'name': self.network + '-' + directory})
        self.docker('run', '--rm', '--user', '0', '-v', f'{self.work}:/fixture', self.cleanup_image,
                    'sh', '-c', 'chmod 644 /fixture/secrets/*')
        self.base = '/gateway-pools/' + self.pool['id'] + '/nginx'
        self.route_id = self.cli('id', 'route')
        self.api('PUT', self.base + '/sandbox', {'gateway_id': self.gateways[2]['id'], 'checks': []}, 0)
        for gateway in self.gateways:
            self.start_gateway(gateway)
        self.wait(lambda: len(self.api('GET', '/gateways?limit=200') or []) == 2, 'production Gateway connections')
        self.routes = [{'route': {'id': self.route_id, 'domain_id': self.domain['id'], 'path': '/', 'match': 'prefix',
                         'kind': 'proxy', 'external_url': f'http://{self.backend}:8080', 'websocket_enabled': True,
                         'enabled': True, 'source': 'API'}, 'expected_revision': 0}]
        self.stage = 'initial_release'
        self.publish('one', self.routes)
        self.routes = []
        sandbox = self.api('GET', self.base + '/sandbox')
        self.api('PUT', self.base + '/sandbox', {'gateway_id': self.gateways[2]['id'], 'checks': [
            {'route_id': self.route_id, 'path': '/', 'method': 'GET', 'scheme': 'http', 'expected_status': 200}]}, sandbox['revision'])
        self.context = ssl.create_default_context(cafile=str(self.work / 'tls.crt'))
        for gateway in self.gateways[:2]:
            self.wait(lambda: request(gateway['ports']['http']) is not None, 'initial HTTP')
            request(gateway['ports']['https'], self.context)
        self.balancer = Balancer([gateway['ports'] for gateway in self.gateways[:2]])
        self.sampler = Sampler(self.balancer, self.context)

    def start_gateway(self, gateway):
        directory = gateway['directory']
        environment = {'NSC_AGENT__MODE': 'sandbox' if directory == 'sandbox' else 'production',
                       'NSC_STORAGE__DRIVER': 'postgres', 'NSC_STORAGE__DSN': self.dsn,
                       'NSC_AGENT__GATEWAY_ID': gateway['id'], 'NSC_AGENT__POOL_ID': self.pool['id'],
                       'NSC_AGENT__NAME': directory, 'NSC_AGENT__CONTROLLER_ADDRESS': self.controller + ':9443',
                       'NSC_AGENT__SERVER_NAME': self.controller, 'NSC_AGENT__CA_CERT_FILE': '/run/nsc/ca.crt',
                       'NSC_AGENT__TLS_CERT_FILE': '/run/nsc/' + directory + '.crt',
                       'NSC_AGENT__TLS_KEY_FILE': '/run/nsc/' + directory + '.key',
                       'NSC_AGENT__STATE_DIR': '/var/lib/nsc', 'NSC_AGENT__HEARTBEAT_INTERVAL': '1s',
                       'NSC_NGINX__GENERATION_DIR': '/var/lib/nsc/generations', 'NSC_NGINX__WORKER_PROCESSES': '1'}
        http_port, https_port = 80, 443
        self.run(gateway['name'], self.args.gateway_image, environment, ('--config=',),
                 [(self.work / 'secrets', '/run/nsc:ro'), (self.work / directory, '/var/lib/nsc')], [http_port, https_port])
        gateway['ports'] = {'http': self.port(gateway['name'], http_port), 'https': self.port(gateway['name'], https_port)}

    def validate(self, draft, expected='SUCCEEDED'):
        def connected():
            try:
                return self.api('POST', self.base + '/validations', {}, draft['revision'])
            except AssertionError:
                error = json.loads((self.work / 'api-error.json').read_text())
                if error.get('params', {}).get('reason') != 'configured sandbox is offline':
                    raise
                return None
        task = self.wait(connected, 'sandbox connection')
        def terminal():
            value = self.api('GET', self.base + '/validations/' + task['id'])
            if value['status'] not in ('SUCCEEDED', 'FAILED', 'EXPIRED', 'CANCELLED'):
                return None
            assert value['status'] == expected, 'unexpected validation result: ' + value['status']
            return value
        return self.wait(terminal, 'sandbox validation')

    def publish(self, version, routes=()):
        draft = self.api('GET', self.base + '/draft')
        draft = self.api('PUT', self.base + '/draft', {'snippets': [{'scope': 'http',
             'content': f'add_header X-Acceptance {version} always; proxy_buffering off;'}], 'routes': list(routes)}, draft['revision'])
        validation = self.validate(draft)
        after = time.time()
        release = self.api('POST', self.base + '/releases', {'validation_id': validation['id']},
                           draft['revision'], 'gateway-acceptance-' + version)
        def completed():
            value = self.api('GET', self.base + '/releases/' + release['id'])
            if value['status'] in ('FAILED', 'PARTIAL', 'CANCELLED'):
                raise AssertionError('production release failed: ' + value['status'])
            return value if value['status'] == 'SUCCEEDED' else None
        release = self.wait(completed, 'production release')
        self.revision = release['config_revision']
        self.confirm_all(after)
        return release

    def confirm_all(self, after):
        self.confirmations = []
        for gateway in self.gateways[:2]:
            def confirmed():
                value = self.api('GET', '/gateways/' + gateway['id'])
                return value if confirms_gateway(value, self.revision, after) else None
            value = self.wait(confirmed, 'fresh exact Gateway evidence')
            self.confirmations.append({'gateway_id': gateway['id'], 'revision': self.revision,
                                      'model_digest': value['model_digest'], 'evidence': value['evidence']})

    def scenario(self, name, action, fault=False):
        self.stage = name
        print('gateway_acceptance stage=' + name, flush=True)
        start = time.monotonic()
        action()
        while time.monotonic() - start < 2:
            time.sleep(0.1)
        metrics = self.sampler.report(start, time.monotonic(), fault)
        self.summary['scenarios'].append({'name': name, 'status': 'PASS', 'traffic': metrics,
                                          'confirmations': self.confirmations})

    def good_update(self):
        streams = [Stream(self.balancer.ports['http']), Stream(self.balancer.ports['http'], True)]
        self.publish('two')
        for stream in streams:
            stream.finish()
        for gateway in self.gateways[:2]:
            assert request(gateway['ports']['http']).get('X-Acceptance') == 'two'
            assert request(gateway['ports']['https'], self.context).get('X-Acceptance') == 'two'

    def rejected_candidate(self):
        before = list(self.confirmations)
        sandbox_port = self.gateways[2]['ports']['http']
        draft = self.api('GET', self.base + '/draft')
        draft = self.api('PUT', self.base + '/draft', {'snippets': [{'scope': 'http', 'content':
            'proxy_buffers 2 1k; proxy_buffer_size 8k; proxy_busy_buffers_size 8k;'}]}, draft['revision'])
        task = self.validate(draft, 'FAILED')
        assert task['results'][self.gateways[2]['id']]['status'] == 'FAILED'
        assert 'nginx -t' in task['results'][self.gateways[2]['id']].get('error', ''), 'failure was not real NGINX syntax rejection'
        for gateway in self.gateways[:2]:
            assert request(gateway['ports']['http']).get('X-Acceptance') == 'two'
        self.confirm_all(time.time() - 2)
        assert [value['model_digest'] for value in before] == [value['model_digest'] for value in self.confirmations]
        # A held sandbox review is disposable, not a committed production LKG.
        # The next validation restores its original empty generation first.
        connection = http.client.HTTPConnection('127.0.0.1', sandbox_port, timeout=3)
        try:
            connection.request('GET', '/', headers={'Host': HOST})
            response = connection.getresponse()
            response.read()
            assert response.status == 404 and response.getheader('X-Acceptance') is None
        finally:
            connection.close()

    def replace_one(self, index, controller_online=True):
        gateway = self.gateways[index]
        expected = self.confirmations[index]
        # Active finite streams complete on their original process before removal.
        stream = Stream(gateway['ports']['http'], True)
        self.balancer.drain(index)
        stream.finish()
        self.docker('stop', '-t', '10', gateway['name'])
        self.docker('rm', gateway['name'])
        self.start_gateway(gateway)
        self.balancer.backends[index] = gateway['ports']
        self.wait(lambda: request(gateway['ports']['http']) is not None, 'persisted LKG cold-start')
        request(gateway['ports']['https'], self.context)
        state = json.loads(self.docker('exec', gateway['name'], 'cat', '/var/lib/nsc/generations/state.json'))
        assert state['config_revision'] == expected['revision']
        assert state['model_digest'] == expected['model_digest']
        assert state['rendered_digest'] == expected['evidence']['rendered_digest']
        if controller_online:
            self.confirm_all(time.time() - 2)
        self.balancer.admit(index)

    def controller_outage(self):
        self.docker('stop', '-t', '10', self.controller)
        self.replace_one(0, False)
        time.sleep(2)
        self.docker('start', self.controller)
        self.api_port = self.port(self.controller, 8080)
        self.wait(self.ready, 'controller reconnect')
        self.confirm_all(time.time())

    def gateway_fault(self):
        gateway = self.gateways[0]
        started = time.monotonic()
        self.docker('kill', gateway['name'])
        # A failed target must disappear from the LB health set within the bound.
        self.wait(lambda: 0 not in self.balancer.healthy, 'LB excludes failed Gateway', timeout=5)
        assert time.monotonic() - started < 5, 'Gateway fault recovery exceeded five seconds'
        self.summary['fault_recovery_seconds'] = time.monotonic() - started
        time.sleep(2)
        self.docker('start', gateway['name'])
        gateway['ports'] = {'http': self.port(gateway['name'], 80), 'https': self.port(gateway['name'], 443)}
        self.balancer.backends[0] = gateway['ports']
        self.wait(lambda: request(gateway['ports']['http']) is not None, 'faulted Gateway LKG recovery')
        self.confirm_all(time.time())

    def independent_balancers(self):
        backend_file = self.work / 'lb-backends.json'
        backend_file.write_text(json.dumps([gateway['ports'] for gateway in self.gateways[:2]]))
        backend_file.chmod(0o600)
        endpoints = []
        for index in range(2):
            ports_file = self.work / f'lb-{index}-ports.json'
            with (self.work / f'lb-{index}.log').open('w') as log:
                process = subprocess.Popen(['python3', str(self.repository / 'scripts/gateway_lb_fixture.py'),
                                            '--backends-file', str(backend_file), '--ports-file', str(ports_file)],
                                           stdout=log, stderr=subprocess.STDOUT)
            self.lb_processes.append(process)
            self.wait(lambda: ports_file.exists() and process.poll() is None, 'independent LB process', timeout=10)
            endpoints.append(json.loads(ports_file.read_text())['ports'])
        for ports in endpoints:
            request(ports['http'])
            request(ports['https'], self.context)
        sampler = Sampler(SimpleNamespace(ports=endpoints[1]), self.context)
        started = time.monotonic()
        try:
            self.lb_processes[0].terminate()
            self.lb_processes[0].wait(timeout=10)
            assert self.lb_processes[1].poll() is None, 'surviving LB process exited'
            failed_endpoint = False
            try:
                request(endpoints[0]['http'])
            except (OSError, http.client.HTTPException):
                failed_endpoint = True
            assert failed_endpoint, 'terminated LB endpoint remained reachable'
            # Operators/DNS/VIP failover must select the surviving endpoint; this
            # test establishes its availability, not automatic client migration.
            time.sleep(2)
            request(endpoints[1]['http'])
            request(endpoints[1]['https'], self.context)
        finally:
            sampler.close()
            self.lb_samples.extend(sampler.samples)
        self.summary['independent_load_balancers'] = {
            'processes_distinct': self.lb_processes[0].pid != self.lb_processes[1].pid,
            'failed_endpoint_unreachable': failed_endpoint,
            'surviving_endpoint_traffic': sampler.report(started, time.monotonic()),
            'client_failover': 'EXPLICIT_ENDPOINT_SELECTION',
            'failure_domains': 'SEPARATE_PROCESSES_ON_ONE_FIXTURE_HOST'}

    def cleanup(self):
        if self.sampler:
            self.sampler.close()
        if self.balancer:
            self.balancer.close()
        for process in self.lb_processes:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
        if self.summary['status'] != 'PASS':
            for name in set(self.containers):
                result = subprocess.run(['docker', 'logs', '--tail', '100', name], capture_output=True, text=True)
                (self.work / (name.rsplit('-', 1)[-1] + '.log')).write_text(result.stdout + result.stderr)
        for name in reversed(list(dict.fromkeys(self.containers))):
            subprocess.run(['docker', 'rm', '-f', name], capture_output=True)
        subprocess.run(['docker', 'network', 'rm', self.network], capture_output=True)
        output = Path(self.args.output).resolve()
        output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.summary.update({'finished_at': utc_now(), 'last_stage': self.stage})
        output.write_text(json.dumps(self.summary, indent=2) + '\n')
        output.chmod(0o600)
        if self.sampler:
            samples = output.with_name('requests.jsonl')
            samples.write_text(''.join(json.dumps(sample) + '\n' for sample in self.sampler.samples))
            samples.chmod(0o600)
        if self.lb_samples:
            samples = output.with_name('lb-requests.jsonl')
            samples.write_text(''.join(json.dumps(sample) + '\n' for sample in self.lb_samples))
            samples.chmod(0o600)
        if self.summary['status'] == 'PASS':
            subprocess.run(['docker', 'run', '--rm', '--user', '0', '-v', f'{self.work}:/fixture',
                            self.cleanup_image, 'sh', '-c', 'rm -rf /fixture/*'], capture_output=True)
            shutil.rmtree(self.work, ignore_errors=True)
        else:
            print('private diagnostics retained: ' + str(self.work), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    version = (Path(__file__).resolve().parent.parent / 'VERSION').read_text().strip()
    parser.add_argument('--controller-image', default=os.environ.get('NSC_CONTROLLER_IMAGE', 'nsc-controller:v' + version))
    parser.add_argument('--gateway-image', default=os.environ.get('NSC_GATEWAY_IMAGE', 'nsc-gateway:v' + version))
    parser.add_argument('--output', default='coverage/gateway-acceptance/' + datetime.datetime.now().strftime('%Y%m%dT%H%M%S') + '/summary.json')
    parser.add_argument('--verify-lb-redundancy', action='store_true', help='also verify two separate LB processes and surviving endpoint access')
    args = parser.parse_args()
    fixture = Fixture(args)
    try:
        fixture.prepare()
        fixture.scenario('valid_configuration_update_and_streams', fixture.good_update)
        fixture.scenario('invalid_candidate_preserves_production_lkg', fixture.rejected_candidate)
        fixture.scenario('serial_gateway_replacement_with_independent_state', lambda: [fixture.replace_one(index) for index in range(2)])
        fixture.scenario('controller_outage_and_lkg_cold_start', fixture.controller_outage)
        fixture.scenario('gateway_crash_and_lb_recovery', fixture.gateway_fault, True)
        if args.verify_lb_redundancy:
            fixture.scenario('independent_lb_process_failure', fixture.independent_balancers)
        fixture.summary['status'] = 'PASS'
    except Exception as error:
        fixture.summary.update({'status': 'FAIL', 'error': str(error)})
        raise
    finally:
        fixture.cleanup()
    print('gateway_acceptance status=PASS report=' + args.output, flush=True)


if __name__ == '__main__':
    main()
