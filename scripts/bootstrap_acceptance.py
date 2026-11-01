#!/usr/bin/env python3
"""Bootstrap acceptance in disposable SSH-enabled nested Docker nodes.

Only returned fixture container/network IDs are cleaned up. The host Swarm and
business services are never initialized, joined, updated or inspected.
"""
import argparse
import base64
import hashlib
import http.client
import io
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile
import ssl
import urllib.request
import urllib.error
import time
import traceback


class BootstrapFixture:
    def __init__(self, args):
        self.args = args
        self.repository = Path(__file__).resolve().parent.parent
        self.work = Path(tempfile.mkdtemp(prefix='nsc-bootstrap-acceptance-'))
        self.work.chmod(0o700)
        self.name = 'nsc-bootstrap-' + secrets.token_hex(6)
        self.containers = []
        self.created_images = []
        self.network = None
        self.nodes = []
        self.summary = {'schema_version': 1, 'status': 'RUNNING', 'checks': {},
                        'limitations': ['Nested nodes share one physical host; host and zone isolation are not established.',
                                        'OVH DNS verification is DISABLED.'], 'images': {}}

    def private_log(self, name, data):
        try:
            (self.work / name).write_bytes(data)
        except OSError:
            self.summary['diagnostics_unavailable'] = True

    def command(self, *args, timeout=180):
        result = subprocess.run(args, capture_output=True, timeout=timeout)
        if result.returncode:
            self.private_log('private-command-error.log', result.stderr)
            raise AssertionError('fixture command failed; inspect private diagnostics')
        return result.stdout.decode().strip()

    def docker(self, *args, timeout=180):
        return self.command('docker', *args, timeout=timeout)

    def inner(self, index, *args):
        return self.docker('exec', self.nodes[index], 'docker', '--config', '/fixture/registry-client', *args)

    def address(self, container):
        value = json.loads(self.docker('inspect', container))[0]
        return next(iter(value['NetworkSettings']['Networks'].values()))['IPAddress']

    def wait(self, predicate, timeout=120):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                if predicate():
                    return
            except AssertionError:
                pass
            time.sleep(0.5)
        raise AssertionError('fixture prerequisite did not become ready')

    def prepare(self):
        self.summary['stage'] = 'prerequisites'
        for name, image in [('controller', self.args.controller_image), ('gateway', self.args.gateway_image),
                            ('fixture', self.args.fixture_image)]:
            self.summary['images'][name] = self.docker('image', 'inspect', image, '--format', '{{.Id}}')
        images = json.loads((self.repository / 'build/image-lock.json').read_text())['images']
        for key in ('postgres', 'registry'):
            self.docker('image', 'inspect', images[key])
        self.summary['source_fingerprint'] = self.command('node', 'scripts/readiness/gate.mjs', 'fingerprint')
        for name, image in [('controller', self.args.controller_image), ('gateway', self.args.gateway_image)]:
            labels = json.loads(self.docker('image', 'inspect', image))[0]['Config']['Labels']
            assert labels.get('org.nsc.source.fingerprint') == self.summary['source_fingerprint'], 'image source fingerprint differs from candidate'
        extract = self.docker('create', '--label', 'io.nsc.bootstrap.fixture=' + self.name, self.args.controller_image)
        self.containers.append(extract)
        self.docker('cp', extract + ':/usr/local/bin/nsc-controller', str(self.work / 'image-controller'))
        binary = self.args.controller_binary or self.work / 'image-controller'
        supplied = binary.read_bytes()
        assert hashlib.sha256(supplied).digest() == hashlib.sha256((self.work / 'image-controller').read_bytes()).digest(), 'CLI binary differs from the bound Controller image'
        self.summary['controller_binary_sha256'] = hashlib.sha256(supplied).hexdigest()
        if binary == self.work / 'image-controller':
            os.link(binary, self.work / 'nsc-controller')
        else:
            shutil.copyfile(binary, self.work / 'nsc-controller')
        (self.work / 'nsc-controller').chmod(0o755)
        self.command('ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(self.work / 'ssh-key'))
        self.network = self.docker('network', 'create', '--label', 'io.nsc.bootstrap.fixture=' + self.name, self.name)
        registry_password = secrets.token_urlsafe(32)
        hashed = subprocess.run(['go', 'run', './tools/bootstrapfixtureauth'], cwd=self.repository, input=registry_password.encode(), capture_output=True, timeout=120)
        assert hashed.returncode == 0, 'disposable registry credential hashing failed'
        (self.work / 'registry-users').write_text('fixture:' + hashed.stdout.decode().strip() + '\n')
        (self.work / 'registry-users').chmod(0o600)
        registry = self.docker('run', '-d', '--network', self.network, '--tmpfs', '/var/lib/registry:rw,nosuid,nodev,size=1g', '--label', 'io.nsc.bootstrap.fixture=' + self.name,
                               '--mount', 'type=bind,source=' + str(self.work / 'registry-users') + ',target=/run/registry-users,readonly',
                               '-e', 'REGISTRY_AUTH=htpasswd', '-e', 'REGISTRY_AUTH_HTPASSWD_REALM=bootstrap-fixture', '-e', 'REGISTRY_AUTH_HTPASSWD_PATH=/run/registry-users', images['registry'])
        self.containers.append(registry)
        self.registry_address = self.address(registry) + ':5000'
        (self.work / 'registry-client').mkdir(mode=0o700)
        (self.work / 'registry-client' / 'config.json').write_text(json.dumps({'auths': {self.registry_address: {'auth': base64.b64encode(('fixture:' + registry_password).encode()).decode()}}}))
        (self.work / 'registry-client' / 'config.json').chmod(0o600)
        registry_password = None
        for index in range(self.args.nodes):
            container = self.docker('run', '-d', '--privileged', '--network', self.network,
                                    '--label', 'io.nsc.bootstrap.fixture=' + self.name, '-e', 'DOCKER_TLS_CERTDIR=',
                                    '--tmpfs', '/var/lib/docker:rw,exec,nosuid,nodev,size=4g,mode=0711',
                                    '--mount', 'type=bind,source=' + str(self.work) + ',target=/fixture',
                                    self.args.fixture_image, '--insecure-registry=' + self.registry_address)
            self.containers.append(container)
            self.nodes.append(container)
            self.wait(lambda index=index: self.inner(index, 'info', '--format', '{{.ID}}'))
        known = []
        for node in self.nodes:
            pub = self.docker('exec', node, 'cat', '/etc/ssh/ssh_host_ed25519_key.pub')
            known.append(self.address(node) + ' ' + pub)
        (self.work / 'known_hosts').write_text('\n'.join(known) + '\n')
        (self.work / 'known_hosts').chmod(0o600)
        for node in self.nodes:
            context = self.command('ssh', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
                '-o', 'UserKnownHostsFile=' + str(self.work / 'known_hosts'), '-i', str(self.work / 'ssh-key'),
                'root@' + self.address(node), 'printenv DOCKER_CONTEXT')
            assert context == 'nsc-unrelated-fixture', 'SSH Docker context isolation fixture is not active'
        self.summary['checks']['ssh_context_redirect_fixture_active'] = 'PASS'
        self.summary['stage'] = 'immutable_fixture_images'
        self.stream_images(images['postgres'])
        self.refs = {}
        for component, source in [('controller', self.args.controller_image), ('gateway', self.args.gateway_image),
                                  ('postgresql', images['postgres'])]:
            tag = self.registry_address + '/' + component + ':fixture'
            source_id = self.docker('image', 'inspect', source, '--format', '{{.Id}}')
            self.inner(0, 'tag', source_id, tag)
            self.inner(0, 'push', tag)
            digests = json.loads(self.inner(0, 'image', 'inspect', tag))[0]['RepoDigests']
            self.refs[component] = next(value for value in digests if value.startswith(self.registry_address + '/'))
        try:
            with urllib.request.urlopen('http://' + self.registry_address + '/v2/', timeout=8):
                raise AssertionError('disposable registry accepted an unauthenticated request')
        except urllib.error.HTTPError as response:
            assert response.code == 401, 'disposable registry authentication was not enforced'
        self.summary['checks']['disposable_private_registry_authentication'] = 'PASS'
        self.configuration = {'name': 'nsc', 'mode': 'single' if self.args.nodes == 1 else 'multi',
                              'maintenance_node': 'node-1', 'data_node': 'node-1', 'node_dir': '/var/lib/docker/nsc-bootstrap',
                              'console_port': 8443, 'known_hosts_file': '/fixture/known_hosts', 'images': self.refs, 'registry_auth_file': '/fixture/registry-client/config.json',
                              'nodes': [{'name': 'node-' + str(index + 1), 'address': self.address(node),
                                         'ssh_user': 'root', 'ssh_port': 22, 'ssh_key_file': '/fixture/ssh-key',
                                         'manager': True} for index, node in enumerate(self.nodes)]}
        (self.work / 'install.json').write_text(json.dumps(self.configuration))
        (self.work / 'install.json').chmod(0o600)

    def cli(self, command, *args, timeout=600, user=None):
        return self.docker('exec', '--user', str(self.args.cli_user if user is None else user), self.nodes[0], '/fixture/nsc-controller', 'bootstrap', command,
                           '--config', '/fixture/install.json', '--state-dir', '/fixture/state', *args, timeout=timeout)

    def install(self):
        self.summary['stage'] = 'plan_and_bootstrap'
        planned = self.cli('init')
        plan = json.loads(planned.splitlines()[0])
        # Approval of the exact digest is mandatory. No generic --yes is used.
        self.cli('init', '--plan', '/fixture/state/plan.json', '--approve', plan['hash'],
                 '--idempotency-key', 'bootstrap-fixture-initial', '--timeout', '8m')
        record = json.loads(self.cli('status'))
        assert record['operations'][-1]['state'] == 'SUCCEEDED'
        assert len(record['resources']) >= 20
        self.summary['checks']['single_migration_and_durable_handoff'] = 'PASS'
        self.record = record
        self.cli('verify', '--timeout', '2m')
        self.summary['checks']['actual_login_manager_loaded_nginx_and_https'] = 'PASS'
        services = json.loads(self.inner(0, 'service', 'inspect', *[
            record['resources'][key]['id'] for key in ('start:controller', 'service:postgresql', 'start:nginx', 'start:nginx-sandbox')]))
        assert len(services) == 4
        assert services[0]['Spec']['Mode'] == {'Global': {}}
        assert services[2]['Spec']['Mode'] == {'Global': {}}
        assert services[1]['Spec']['TaskTemplate']['Placement']['Constraints'] == [
            'node.id == ' + record['nodes']['node-1']['node_id']]
        assert services[3]['Spec']['EndpointSpec'].get('Ports', []) == []
        self.summary['checks']['fixed_database_and_isolated_sandbox'] = 'PASS'
        before = {key: value['id'] for key, value in record['resources'].items()}
        self.cli('init', '--plan', '/fixture/state/plan.json', '--approve', plan['hash'],
                 '--idempotency-key', 'bootstrap-fixture-initial')
        after = json.loads(self.cli('status'))
        assert before == {key: value['id'] for key, value in after['resources'].items()}
        self.summary['checks']['duplicate_request_retains_resource_ids'] = 'PASS'

    def stream_images(self, postgres):
        # A bounded pipe avoids a second full image archive on the host disk.
        export = subprocess.Popen(['docker', 'save', self.args.controller_image,
                                   self.args.gateway_image, postgres], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        load = None
        try:
            load = subprocess.Popen(['docker', 'exec', '-i', self.nodes[0], 'docker', 'load'],
                                    stdin=export.stdout, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            export.stdout.close()
            _, load_error = load.communicate(timeout=180)
            _, export_error = export.communicate(timeout=30)
            if load.returncode or export.returncode:
                self.private_log('private-image-transfer.log', export_error + load_error)
                raise AssertionError('fixture image transfer failed; inspect private diagnostics')
        finally:
            for process in (load, export):
                if process is not None and process.poll() is None:
                    process.kill()
                    process.wait(timeout=15)

    def raw_engine(self, index, path, method='GET', body=None):
        payload = json.dumps(body).encode() if body is not None else b''
        request = (method + ' /v1.47' + path + ' HTTP/1.1\r\nHost: docker\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: ' + str(len(payload)) + '\r\n\r\n').encode() + payload
        result = subprocess.run(['docker', 'exec', '-i', self.nodes[index], 'docker', 'system', 'dial-stdio'],
                                input=request, capture_output=True, timeout=30)
        class ResponseSocket:
            def makefile(self, *_args):
                return io.BytesIO(result.stdout)
        response = http.client.HTTPResponse(ResponseSocket())
        response.begin()
        assert 200 <= response.status < 300, 'owned Docker operation is unavailable'
        data = response.read()
        return json.loads(data) if data else {}

    def api(self, index, path):
        certificate = self.docker('exec', self.nodes[0], 'cat', getattr(self, 'active_certificate_file', '/fixture/state.offline/materials/console.crt'))
        password = self.docker('exec', self.nodes[0], 'cat', '/fixture/state.offline/materials/admin-password')
        request = urllib.request.Request('https://' + self.address(self.nodes[index]) + ':8443/api/v1' + path,
                                         headers={'Authorization': 'Bearer ' + password, 'Cache-Control': 'no-cache'})
        context = ssl.create_default_context(cadata=certificate)
        with urllib.request.urlopen(request, context=context, timeout=8) as response:
            return json.load(response)['data']

    def sql(self, statement):
        database = self.record['resources']['service:postgresql']['id']
        containers = self.inner(0, 'ps', '-q', '--filter', 'label=com.docker.swarm.service.id=' + database).splitlines()
        assert len(containers) == 1, 'fixed database container identity is unavailable'
        return self.inner(0, 'exec', containers[0], 'psql', '-U', 'nsc', '-d', 'nsc', '-At', '-v', 'ON_ERROR_STOP=1', '-c', statement)

    def recovery(self):
        assert len(self.nodes) == 3
        self.summary['stage'] = 'multi_node_failure_and_recovery'
        known = [self.record['resources'][key]['id'] for key in ('start:controller', 'service:postgresql', 'start:nginx', 'start:nginx-sandbox')]
        self.private_log('private-before-recovery-specifications-1.47.json', json.dumps([self.raw_engine(0, '/services/' + identifier) for identifier in known]).encode())
        managers = json.loads(self.inner(0, 'node', 'inspect', *[value['node_id'] for value in self.record['nodes'].values()]))
        assert len(managers) == 3 and all(item['Spec']['Role'] == 'manager' for item in managers)
        leader = next(item['ID'] for item in managers if item.get('ManagerStatus', {}).get('Leader'))
        self.summary['checks']['three_authenticated_managers_and_controllers'] = 'PASS'
        failed = next((index for index in (1, 2) if self.record['nodes']['node-' + str(index + 1)]['node_id'] == leader), 2)
        survivor = 1 if failed != 1 else 2
        def degraded_but_operational():
            try:
                layers = {item['name']: item for item in self.api(survivor, '/system/ha')['layers']}
                return layers['controller']['state'] != 'READY' and layers['execution']['state'] == 'READY' and layers['swarm_quorum']['state'] == 'READY'
            except (OSError, urllib.error.URLError):
                return False
        failover_started = time.monotonic()
        self.docker('pause', self.nodes[failed])
        try:
            self.wait(degraded_but_operational, timeout=120)
            # Manager health and routing-mesh endpoint convergence are separate.
            # Wait for the real failed-node decision, then require consecutive
            # actual NGINX responses rather than one lucky surviving endpoint.
            failed_id = self.record['nodes']['node-' + str(failed + 1)]['node_id']
            self.wait(lambda: self.inner(survivor, 'node', 'inspect', failed_id, '--format', '{{.Status.State}}') == 'down', timeout=120)
            consecutive = 0
            def stable_ingress():
                nonlocal consecutive
                good = False
                try:
                    with urllib.request.urlopen('http://' + self.address(self.nodes[survivor]) + '/', timeout=8) as response:
                        good = response.status == 404 and 'nginx' in response.headers.get('Server', '').lower()
                except urllib.error.HTTPError as response:
                    good = response.code == 404 and 'nginx' in response.headers.get('Server', '').lower()
                except (OSError, urllib.error.URLError):
                    pass
                consecutive = consecutive + 1 if good else 0
                return consecutive >= 3
            self.wait(stable_ingress, timeout=120)
            self.summary.setdefault('timings', {})['non_database_node_failover_seconds'] = round(time.monotonic() - failover_started, 3)
        finally:
            self.docker('unpause', self.nodes[failed])
        self.summary['checks']['non_database_node_loss_keeps_verified_execution_and_ingress'] = 'PASS'
        self.wait(lambda: self.verify_complete(), timeout=150)
        original = self.record['resources']['volume:postgresql']['id']
        identities = hashlib.sha256(self.sql("SELECT payload FROM control_documents WHERE id='community:identities'").encode()).hexdigest()
        certificate = self.docker('exec', self.nodes[0], 'cat', getattr(self, 'active_certificate_file', '/fixture/state.offline/materials/console.crt'))
        password = self.docker('exec', self.nodes[0], 'cat', '/fixture/state.offline/materials/admin-password')
        tls_context = ssl.create_default_context(cadata=certificate)
        self.docker('pause', self.nodes[0])
        try:
            def new_manager_leader():
                nodes = json.loads(self.inner(1, 'node', 'inspect', *[value['node_id'] for value in self.record['nodes'].values()]))
                return any(item['ID'] != leader and item.get('ManagerStatus', {}).get('Leader') and item.get('ManagerStatus', {}).get('Reachability') == 'reachable' for item in nodes)
            self.wait(new_manager_leader, timeout=90)
            # The fixed database cannot be recreated using an empty local volume
            # on a surviving node. Controller redundancy is not database HA.
            for index in (1, 2):
                result = subprocess.run(['docker', 'exec', self.nodes[index], 'docker', 'volume', 'inspect', original], capture_output=True, timeout=15)
                assert result.returncode != 0, 'a database volume was relocated to a different node'
            request = urllib.request.Request('https://' + self.address(self.nodes[1]) + ':8443/api/v1/system/ha', headers={'Authorization': 'Bearer ' + password})
            try:
                with urllib.request.urlopen(request, context=tls_context, timeout=12) as response:
                    assert response.status != 200, 'database outage was reported as healthy'
            except (OSError, urllib.error.URLError):
                pass
            self.summary['checks']['manager_leader_switch_and_database_single_point_failure'] = 'PASS'
        finally:
            self.docker('unpause', self.nodes[0])
        self.wait(lambda: self.verify_complete(), timeout=180)
        assert hashlib.sha256(self.sql("SELECT payload FROM control_documents WHERE id='community:identities'").encode()).hexdigest() == identities
        assert self.record['resources']['volume:postgresql']['id'] == original
        self.summary['checks']['original_database_identity_and_volume_recover_without_replacement'] = 'PASS'

    def verify_complete(self):
        try:
            self.cli('verify', '--timeout', '30s', timeout=45)
            return True
        except AssertionError:
            return False

    def adopt_legacy(self):
        self.make_legacy_fixture()
        self.summary['stage'] = 'verified_legacy_adoption'
        service_ids = {key: self.record['resources']['service:postgresql' if key == 'postgresql' else 'start:' + key]['id']
                       for key in ('controller', 'postgresql', 'nginx', 'nginx-sandbox')}
        before = self.inner(0, 'service', 'inspect', *service_ids.values())
        identities = self.sql("SELECT payload FROM control_documents WHERE id='community:identities'")
        original_applications = self.sql('SELECT id FROM applications ORDER BY id')
        configuration = json.loads(json.dumps(self.configuration))
        # The service config belongs to the non-root Controller (0400). The
        # operator supplies a private readable copy, with registered secrets
        # still resolved through the verified SSH/mount boundary.
        original_config = subprocess.run(['docker', 'exec', self.nodes[0], 'cat', self.legacy_controller_path], capture_output=True, check=True, timeout=30).stdout
        (self.work / 'adopt-controller.yaml').write_bytes(original_config)
        (self.work / 'adopt-controller.yaml').chmod(0o600)
        configuration['adoption'] = {'installation_id': self.record['id'],
            'controller_config_file': '/fixture/adopt-controller.yaml',
            'cluster_id': self.record['cluster_id'], 'service_ids': service_ids,
            'data_node_id': self.record['nodes']['node-1']['node_id'], 'volume': self.record['resources']['volume:postgresql']['id']}
        (self.work / 'adopt.json').write_text(json.dumps(configuration))
        (self.work / 'adopt.json').chmod(0o600)
        arguments = ('--config', '/fixture/adopt.json', '--state-dir', '/fixture/rejected-copy')
        try:
            self.cli('adopt', *arguments)
            raise AssertionError('an existing installation acquired a second maintenance directory')
        except AssertionError as error:
            if str(error) != 'fixture command failed; inspect private diagnostics':
                raise
        # This fixture alone simulates a pre-bootstrap deployment by removing its
        # installation metadata. Services, data, identities and registrations stay.
        # Never run this SQL against an installation outside these owned nodes.
        self.sql("DELETE FROM control_documents WHERE id='system:installation'")
        configuration['adoption'].pop('installation_id')
        (self.work / 'adopt.json').write_text(json.dumps(configuration))
        arguments = ('--config', '/fixture/adopt.json', '--state-dir', '/fixture/legacy-adopt-state')
        plan = json.loads(self.cli('adopt', *arguments).splitlines()[0])
        assert before == self.inner(0, 'service', 'inspect', *service_ids.values()), 'adoption planning changed Docker objects'
        self.cli('adopt', *arguments, '--plan', '/fixture/legacy-adopt-state/plan.json', '--approve', plan['hash'], '--idempotency-key', 'adopt-existing-fixture')
        adopted = json.loads(self.cli('status', '--state-dir', '/fixture/legacy-adopt-state'))
        assert adopted['operations'][-1]['state'] == 'SUCCEEDED'
        assert identities == self.sql("SELECT payload FROM control_documents WHERE id='community:identities'")
        assert original_applications == self.sql('SELECT id FROM applications ORDER BY id')
        assert before == self.inner(0, 'service', 'inspect', *service_ids.values()), 'adoption changed existing service specifications'
        marker = json.loads(self.sql("SELECT payload FROM control_documents WHERE id='system:installation'"))
        assert marker['health'] == 'UNVERIFIED' and marker['database']['single_point_of_failure']
        assert not marker.get('certificate_id'), 'HTTP-only adoption claimed an independent HTTPS asset'
        assert self.sql("SELECT count(*) FROM certificates WHERE id='" + self.record['certificate_id'] + "'") == '1'
        self.summary['checks']['legacy_adoption_preserves_services_data_bindings_and_administrator'] = 'PASS'
        self.summary['checks']['copied_maintenance_directory_cannot_take_over'] = 'PASS'
        self.prepare_adopted_fixture(adopted, service_ids, identities)

    def make_legacy_fixture(self):
        self.summary['stage'] = 'legacy_http_fixture_preparation'
        executor = self.record['resources']['reserve:executor']['id']
        controller = self.record['resources']['reserve:controller']['id']
        service = self.raw_engine(0, '/services/' + controller)
        config_target = '/etc/nsc/controller.yaml'
        source = next(value['Source'] for value in service['Spec']['TaskTemplate']['ContainerSpec']['Mounts'] if value['Target'] == config_target)
        self.legacy_controller_path = self.configuration['node_dir'] + '/' + self.record['id'] + '/controller-legacy.yaml'
        identity = service['Spec']['TaskTemplate']['ContainerSpec']['User']
        for node in self.nodes:
            raw = self.docker('exec', node, 'cat', source)
            converted = subprocess.run(['node', 'scripts/bootstrap_legacy_fixture.mjs'], cwd=self.repository,
                    input=json.dumps({'configuration': raw, 'executor': executor}).encode(), capture_output=True, timeout=30)
            assert converted.returncode == 0, 'owned legacy configuration transform failed'
            result = subprocess.run(['docker', 'exec', '-i', node, 'sh', '-ec', 'umask 077; cat >"$1"; chown "$2" "$1"; chmod 0400 "$1"', 'sh', self.legacy_controller_path, identity], input=converted.stdout, capture_output=True, timeout=30)
            assert result.returncode == 0, 'owned legacy configuration write failed'
        spec = service['Spec']
        for value in spec['TaskTemplate']['ContainerSpec']['Mounts']:
            if value['Target'] == config_target:
                value['Source'] = self.legacy_controller_path
        spec['TaskTemplate']['ForceUpdate'] = spec['TaskTemplate'].get('ForceUpdate', 0) + 1
        spec['EndpointSpec']['Ports'] = [value for value in spec['EndpointSpec']['Ports'] if value['TargetPort'] != 8443]
        self.raw_engine(0, '/services/' + controller + '/update?version=' + str(service['Version']['Index']) + '&registryAuthFrom=spec', 'POST', spec)
        self.wait(lambda: self.raw_engine(0, '/services/' + controller).get('UpdateStatus', {}).get('State') == 'completed', timeout=180)
        for resource, collection in [('reserve:executor', 'services'), ('secret:maintenance', 'secrets'), ('control', 'configs')]:
            identifier = self.record['resources'][resource]['id']
            actual = self.raw_engine(0, '/' + collection + '/' + identifier)
            assert actual['ID'] == identifier and actual['Spec']['Labels']['io.nsc.installation'] == self.record['id']
            self.raw_engine(0, '/' + collection + '/' + identifier, 'DELETE')

    def prepare_adopted_fixture(self, adopted, service_ids, identities):
        self.summary['stage'] = 'legacy_cli_to_web_maintenance'
        release = {'version': 'v0.1.0', 'protocol': 2, 'schema': 18, 'configuration': 1, 'postgresql_major': 18,
                   'architectures': ['amd64', 'arm64'], 'images': self.refs}
        (self.work / 'prepared-release.json').write_text(json.dumps(release))
        (self.work / 'prepared-release.json').chmod(0o600)
        arguments = ('--config', '/fixture/adopt.json', '--state-dir', '/fixture/legacy-adopt-state', '--prepare-web',
                     '--ca-cert-file', '/fixture/state.offline/materials/ca.crt', '--ca-key-file', '/fixture/state.offline/materials/ca.key')
        plan = json.loads(self.cli('upgrade', *arguments, '--release', '/fixture/prepared-release.json').splitlines()[0])
        assert plan['scope']['preparation']['trust_anchor_sha256']
        self.cli('upgrade', *arguments, '--plan', '/fixture/legacy-adopt-state/plan.json', '--approve', plan['hash'],
                 '--idempotency-key', 'prepare-legacy-fixture', '--timeout', '12m', timeout=750)
        prepared = json.loads(self.cli('status', '--state-dir', '/fixture/legacy-adopt-state'))
        assert prepared['operations'][-1]['state'] == 'SUCCEEDED' and prepared['id'] == adopted['id']
        assert prepared['database'] == adopted['database'] and not prepared['history']
        assert {key: prepared['resources']['service:postgresql' if key == 'postgresql' else 'reserve:' + key]['id'] for key in service_ids} == service_ids
        assert identities == self.sql("SELECT payload FROM control_documents WHERE id='community:identities'")
        self.active_certificate_file = '/fixture/legacy-adopt-state.offline/materials/console.crt'
        owner = self.api(0, '/system/installation')
        assert owner['id'] == adopted['id'] and owner['health'] == 'VERIFIED' and set(owner['allowed_operations']) >= {'verify', 'repair', 'upgrade'} and 'rollback' not in owner['allowed_operations']
        plan = self.mutation('/system/installation/plans', {'action': 'verify', 'revision': owner['revision']}, 'prepared-web-verify-plan')
        job = self.mutation('/system/installation/operations', {'plan_hash': plan['hash'], 'revision': plan['revision']}, 'prepared-web-verify')
        verified = self.wait_job(job['id'])
        assert verified['execution_mode'] == 'service' and identities == self.sql("SELECT payload FROM control_documents WHERE id='community:identities'")
        self.summary['checks']['legacy_http_adoption_to_independent_https_and_web_maintenance'] = 'PASS'

    def browser(self):
        self.summary['stage'] = 'initial_browser'
        password = self.docker('exec', self.nodes[0], 'cat', '/fixture/state.offline/materials/admin-password')
        payload = {'url': 'https://' + self.address(self.nodes[0]) + ':8443',
                   'password': password, 'ip': self.address(self.nodes[0]),
                   'certificate_id': self.record['certificate_id'], 'pool_id': self.record['pool_id']}
        result = subprocess.run(['node', 'scripts/bootstrap_browser.mjs'],
                                input=json.dumps(payload).encode(), capture_output=True, timeout=120)
        self.private_log('private-browser.log', result.stderr)
        assert result.returncode == 0, 'browser regression failed; inspect private diagnostics'
        self.summary['checks'].update({name: 'PASS' for name in json.loads(result.stdout)['checks']})

    def restore_archive(self):
        self.summary['stage'] = 'isolated_archive_restore'
        record = self.record
        receipt = record['resources']['backup']
        archive_name = receipt['id']
        assert Path(archive_name).name == archive_name
        dump = subprocess.run(['docker', 'exec', self.nodes[0], 'cat', '/fixture/state.offline/backups/' + archive_name],
                              capture_output=True, check=True, timeout=30).stdout
        assert hashlib.sha256(dump).hexdigest() == receipt['spec_digest']
        # The temporary restore database has no network interface or published
        # port. Local trust is confined to this owned, disposable test container.
        restore = self.inner(0, 'run', '-d', '--network', 'none', '--label', 'io.nsc.bootstrap.fixture=' + self.name,
                             '--tmpfs', '/var/lib/postgresql:rw,nosuid,nodev,size=256m',
                             '-e', 'POSTGRES_USER=nsc', '-e', 'POSTGRES_DB=nsc',
                             '-e', 'POSTGRES_HOST_AUTH_METHOD=trust', self.refs['postgresql'])
        try:
            # The entrypoint's temporary initialization server also answers pg_isready.
            # Restore only after PID 1 has exec'd the final PostgreSQL server.
            self.wait(lambda: self.inner(0, 'exec', restore, 'sh', '-c',
                                         'test "$(cat /proc/1/comm)" = postgres && pg_isready -U nsc -d nsc'))
            result = subprocess.run(['docker', 'exec', '-i', self.nodes[0], 'docker', 'exec', '-i', restore,
                                     'pg_restore', '--exit-on-error', '--no-owner', '-U', 'nsc', '-d', 'nsc'],
                                    input=dump, capture_output=True, timeout=120)
            self.private_log('private-restore.log', result.stderr)
            assert result.returncode == 0, 'isolated archive restoration failed'
            def sql(statement):
                return self.inner(0, 'exec', restore, 'psql', '-U', 'nsc', '-d', 'nsc', '-At', '-v', 'ON_ERROR_STOP=1', '-c', statement)
            assert sql("SELECT version_id FROM goose_db_version WHERE is_applied ORDER BY id DESC LIMIT 1") == '18'
            assert sql("SELECT count(*) FROM gateway_pools WHERE id='" + record['pool_id'] + "'") == '1'
            assert sql("SELECT count(*) FROM runtimes WHERE id='" + record['runtime_id'] + "' AND cluster_id='" + record['cluster_id'] + "'") == '1'
            assert sql("SELECT count(*) FROM certificates WHERE id='" + record['certificate_id'] + "'") == '1'
            assert sql("SELECT count(*) FROM control_documents WHERE id='community:identities'") == '1'
            self.summary['checks']['initial_database_archive_restores_original_resource_ids'] = 'PASS'
        finally:
            self.inner(0, 'rm', '-fv', restore)

    def mutation(self, path, body, key):
        certificate = self.docker('exec', self.nodes[0], 'cat', getattr(self, 'active_certificate_file', '/fixture/state.offline/materials/console.crt'))
        password = self.docker('exec', self.nodes[0], 'cat', '/fixture/state.offline/materials/admin-password')
        request = urllib.request.Request('https://' + self.address(self.nodes[0]) + ':8443/api/v1' + path,
                        data=json.dumps(body).encode(), method='POST',
                        headers={'Authorization': 'Bearer ' + password, 'Content-Type': 'application/json', 'Idempotency-Key': key})
        with urllib.request.urlopen(request, context=ssl.create_default_context(cadata=certificate), timeout=30) as response:
            return json.load(response)['data']

    def maintenance_image(self, source, suffix, binary=None, broken=False):
        context = self.work / ('build-' + suffix)
        context.mkdir(mode=0o700)
        instructions = ['ARG BASE_IMAGE', 'FROM ${BASE_IMAGE}']
        if broken:
            (context / 'controller').write_text('#!/bin/sh\nexit 23\n')
            instructions += ['COPY --chmod=0555 controller /usr/local/bin/nsc-controller']
        elif binary:
            shutil.copyfile(binary, context / 'controller')
            instructions += ['COPY --chmod=0555 controller /usr/local/bin/nsc-controller']
        instructions += ['LABEL io.nsc.bootstrap.fixture="' + suffix + '"']
        (context / 'Dockerfile').write_text('\n'.join(instructions) + '\n')
        # Build derivative fixtures inside the owned node. Its Docker store and
        # registry are tmpfs-backed; host cache and unrelated images stay intact.
        tag = self.name + ':' + suffix
        self.inner(0, 'build', '--build-arg', 'BASE_IMAGE=' + source, '-t', tag, '/fixture/' + context.name)
        image = self.inner(0, 'image', 'inspect', tag, '--format', '{{.Id}}')
        controller = context / 'controller'
        if controller.exists():
            (context / 'controller.sha256').write_text(hashlib.sha256(controller.read_bytes()).hexdigest() + '\n')
            controller.unlink()
        remote = self.registry_address + '/' + suffix + ':fixture'
        self.inner(0, 'tag', image, remote)
        self.inner(0, 'push', remote)
        values = json.loads(self.inner(0, 'image', 'inspect', remote))[0]['RepoDigests']
        reference = next(value for value in values if value.startswith(self.registry_address + '/'))
        self.inner(0, 'image', 'rm', '--force', image)
        return reference

    def wait_job(self, identifier, terminal='SUCCEEDED', timeout=420):
        def complete():
            try:
                value = self.api(0, '/system/installation/operations/' + identifier)
                if value['state'] in ('FAILED', 'BLOCKED') and value['state'] != terminal:
                    self.private_log('private-maintenance-job.json', json.dumps(value).encode())
                    raise RuntimeError('maintenance ended before verified completion')
                return value['state'] == terminal
            except (OSError, urllib.error.URLError):
                return False
        self.wait(complete, timeout=timeout)
        return self.api(0, '/system/installation/operations/' + identifier)

    def maintenance(self):
        self.summary['stage'] = 'independent_system_maintenance'
        def ready():
            try:
                value = self.api(0, '/system/installation')
                self.private_log('private-maintenance-readiness.json', json.dumps(value).encode())
                return value['health'] == 'VERIFIED' and set(value['allowed_operations']) >= {'verify', 'repair', 'upgrade'}
            except (OSError, urllib.error.URLError):
                return False
        self.wait(ready, timeout=90)
        installed = self.api(0, '/system/installation')
        executor = installed['executor']['id']
        reserved = self.raw_engine(0, '/services/' + executor)
        assert reserved['Spec']['Mode'].get('ReplicatedJob')
        assert reserved['Spec']['TaskTemplate']['Placement']['Constraints'] == ['node.id == 0000000000000000000000000']
        assert not self.inner(0, 'ps', '-q', '--filter', 'label=com.docker.swarm.service.id=' + executor)
        mounts = reserved['Spec']['TaskTemplate']['ContainerSpec']['Mounts']
        assert not any('.offline' in entry.get('Source', '') or 'ssh-key' in entry.get('Source', '') for entry in mounts)
        before_database = installed['database']
        original_admin = self.sql("SELECT payload FROM control_documents WHERE id='community:identities'")
        applications = self.api(0, '/applications?runtime_id=' + installed['runtime_id'] + '&limit=200')
        system = [value for value in applications if value['system']]
        assert len(system) == 4 and all(value['management_mode'] == 'system' and value['installation_id'] == installed['id'] for value in system)
        self.summary['checks']['four_owned_system_applications_and_independent_secret_only_executor'] = 'PASS'
        plan = self.mutation('/system/installation/plans', {'action': 'repair', 'revision': installed['revision'], 'components': ['controller']}, 'repair-plan')
        key = secrets.token_hex(16)
        job = self.mutation('/system/installation/operations', {'plan_hash': plan['hash'], 'revision': plan['revision']}, key)
        duplicate = self.mutation('/system/installation/operations', {'plan_hash': plan['hash'], 'revision': plan['revision']}, key)
        assert duplicate['id'] == job['id']
        def running_executor():
            containers = self.inner(0, 'ps', '-q', '--filter', 'label=com.docker.swarm.service.id=' + executor).splitlines()
            state = json.loads(self.cli('status'))
            return containers and state['operations'][-1]['plan']['action'] == 'repair' and state['operations'][-1]['state'] == 'RUNNING'
        self.wait(running_executor, timeout=90)
        containers = self.inner(0, 'ps', '-q', '--filter', 'label=com.docker.swarm.service.id=' + executor).splitlines()
        assert len(containers) == 1
        self.inner(0, 'kill', containers[0])
        def outcome_unknown():
            try:
                return self.api(0, '/system/installation/operations/' + job['id'])['state'] == 'UNKNOWN'
            except (OSError, urllib.error.URLError):
                return False
        self.wait(outcome_unknown, timeout=120)
        resumed = self.mutation('/system/installation/operations', {'operation_id': job['id'], 'plan_hash': plan['hash'], 'revision': plan['revision']}, secrets.token_hex(16))
        assert resumed['id'] == job['id']
        self.wait_job(job['id'])
        self.summary['checks']['duplicate_submission_and_stopped_executor_resume_same_owned_objects'] = 'PASS'
        target_binary = self.work / 'maintenance-controller'
        environment = os.environ.copy(); environment['CGO_ENABLED'] = '0'
        build = subprocess.run(['go', 'build', '-trimpath', '-buildvcs=false', '-ldflags=-s -w -X main.version=v0.1.0-maintenance-fixture', '-o', str(target_binary), './cmd/nsc-controller'], cwd=self.repository, env=environment, capture_output=True, timeout=180)
        assert build.returncode == 0, 'isolated maintenance candidate build failed'
        target = {'version': 'v0.1.0-maintenance-fixture', 'protocol': 2, 'schema': 18, 'configuration': 1, 'postgresql_major': 18, 'architectures': ['amd64', 'arm64'],
                  'images': {'controller': self.maintenance_image(self.args.controller_image, 'upgrade-controller', binary=target_binary),
                             'gateway': self.maintenance_image(self.args.gateway_image, 'upgrade-gateway'), 'postgresql': self.refs['postgresql']}}
        installed = self.api(0, '/system/installation')
        incompatible = dict(target, schema=19)
        try:
            self.mutation('/system/installation/plans', {'action': 'upgrade', 'revision': installed['revision'], 'release': incompatible}, 'incompatible-manifest')
            raise AssertionError('unknown schema upgrade was admitted')
        except urllib.error.HTTPError as response:
            assert response.code == 409
        self.summary['checks']['unknown_schema_and_database_transition_blocked'] = 'PASS'
        password = self.docker('exec', self.nodes[0], 'cat', '/fixture/state.offline/materials/admin-password')
        browser = subprocess.run(['node', 'scripts/bootstrap_maintenance_browser.mjs'], cwd=self.repository,
                input=json.dumps({'url': installed['console_urls'][0], 'password': password, 'release': target}).encode(), capture_output=True, timeout=420)
        if browser.returncode:
            self.private_log('private-maintenance-browser.log', browser.stderr)
            raise AssertionError('maintenance browser acceptance failed')
        self.summary['checks']['real_upgrade_console_reconnect_session_and_responsive_progress'] = json.loads(browser.stdout)['status']
        installed = self.api(0, '/system/installation')
        assert installed['release']['version'] == target['version'] and installed['database'] == before_database
        controller_containers = self.inner(0, 'ps', '-q', '--filter', 'label=com.docker.swarm.service.id=' + installed['components']['controller']['id']).splitlines()
        assert len(controller_containers) == 1 and self.inner(0, 'exec', controller_containers[0], '/usr/local/bin/nsc-controller', 'version') == target['version']
        bad = dict(target); bad['images'] = dict(target['images'])
        bad['images']['controller'] = self.maintenance_image(self.args.controller_image, 'bad-controller', broken=True)
        plan = self.mutation('/system/installation/plans', {'action': 'upgrade', 'revision': installed['revision'], 'release': bad}, 'bad-image-plan')
        failed = self.mutation('/system/installation/operations', {'plan_hash': plan['hash'], 'revision': plan['revision']}, 'bad-image-maintenance')
        def failed_offline():
            current = json.loads(self.cli('status'))
            return current['operations'][-1]['plan']['hash'] == plan['hash'] and current['operations'][-1]['state'] == 'FAILED'
        self.wait(failed_offline, timeout=180)
        assert self.raw_engine(0, '/services/' + installed['components']['controller']['id'])['UpdateStatus']['State'] == 'paused'
        self.summary['checks']['bad_image_pauses_without_claiming_success_or_replacing_database'] = 'PASS'
        for number in (1, 2):
            user = 0 if number == 1 else self.args.cli_user
            value = json.loads(self.cli('rollback', user=user).splitlines()[0])
            self.cli('rollback', '--plan', '/fixture/state/plan.json', '--approve', value['hash'], '--idempotency-key', 'compatible-rollback-' + str(number), '--timeout', '6m', user=user)
        self.record = json.loads(self.cli('status'))
        owners = self.docker('exec', self.nodes[0], 'stat', '-c', '%u', '/fixture/state/installation.json', '/fixture/state/executor.lock', '/fixture/state/plan.json').splitlines()
        assert owners == [str(self.args.cli_user)] * 3, 'root recovery changed metadata ownership'
        self.summary['checks']['root_recovery_preserves_original_maintenance_owner'] = 'PASS'
        final = self.api(0, '/system/installation')
        assert final['release']['images'] == self.refs and final['database'] == before_database
        assert self.sql("SELECT payload FROM control_documents WHERE id='community:identities'") == original_admin
        self.summary['checks']['offline_cli_compatible_rollbacks_preserve_original_admin_bindings_and_data'] = 'PASS'

    def cleanup(self):
        failures = []
        for container in reversed(self.containers):
            try:
                result = subprocess.run(['docker', 'rm', '-fv', container], capture_output=True, timeout=45)
                if result.returncode:
                    failures.append('container_cleanup_unconfirmed')
            except subprocess.TimeoutExpired:
                failures.append('container_cleanup_timeout')
        if self.network:
            try:
                result = subprocess.run(['docker', 'network', 'rm', self.network], capture_output=True, timeout=45)
                if result.returncode:
                    failures.append('network_cleanup_unconfirmed')
            except subprocess.TimeoutExpired:
                failures.append('network_cleanup_timeout')
        for tag, identifier in reversed(self.created_images):
            try:
                # Delete only the exact image ID created for this invocation.
                current = self.docker('image', 'inspect', tag, '--format', '{{.Id}}')
                if current != identifier:
                    failures.append('image_cleanup_identity_changed')
                    continue
                self.docker('image', 'rm', tag)
            except (AssertionError, subprocess.TimeoutExpired):
                failures.append('image_cleanup_unconfirmed')
        if failures:
            self.summary['status'] = 'FAIL'
            self.summary['cleanup'] = failures
        else:
            self.summary['checks']['owned_fixture_cleanup'] = 'PASS'
        if not self.args.keep:
            shutil.rmtree(self.work, ignore_errors=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--controller-image', required=True)
    parser.add_argument('--gateway-image', required=True)
    parser.add_argument('--controller-binary', type=Path, help='optional binary; must exactly match the bound Controller image')
    parser.add_argument('--fixture-image', default='nsc-bootstrap-ssh-fixture:local')
    parser.add_argument('--nodes', type=int, choices=(1, 3), default=1)
    parser.add_argument('--cli-user', type=int, choices=(0, 1000), default=0, help='exercise a non-root maintenance owner and root recovery')
    parser.add_argument('--output', type=Path, default=Path('coverage/bootstrap/current/summary.json'))
    parser.add_argument('--keep', action='store_true')
    parser.add_argument('--recovery', action='store_true', help='test three-node failure, recovery and legacy adoption')
    parser.add_argument('--maintenance', action='store_true', help='verify the independent system executor, real image upgrades, interruption and compatible rollback')
    parser.add_argument('--browser', action='store_true', help='run fresh, isolated browser checks')
    args = parser.parse_args()
    fixture = BootstrapFixture(args)
    try:
        fixture.prepare()
        fixture.install()
        if args.browser:
            fixture.browser()
        fixture.restore_archive()
        if args.maintenance:
            fixture.maintenance()
        if args.recovery:
            fixture.recovery()
            fixture.adopt_legacy()
        assert fixture.summary['source_fingerprint'] == fixture.command('node', 'scripts/readiness/gate.mjs', 'fingerprint'), 'source changed during acceptance'
        fixture.summary['status'] = 'FAIL' if fixture.summary.get('diagnostics_unavailable') else 'PASS'
    except Exception as error:
        fixture.summary['status'] = 'FAIL'
        fixture.summary['reason'] = type(error).__name__
        # Raw commands, private addresses, host paths and credentials stay private.
        fixture.private_log('private-error.log', traceback.format_exc().encode())
        if getattr(fixture, 'record', None):
            try:
                fixture.private_log('private-maintenance-documents.jsonl', fixture.sql("SELECT payload FROM control_documents WHERE kind IN ('system-installation','system-installation-job') ORDER BY id").encode())
            except (AssertionError, OSError, subprocess.TimeoutExpired):
                fixture.summary['diagnostics_unavailable'] = True
        for index, node in enumerate(fixture.nodes):
            result = subprocess.run(['docker', 'exec', node, 'sh', '-c', 'cat /fixture/state.offline/diagnostics/docker-response-*.json 2>/dev/null || true'], capture_output=True, timeout=30)
            fixture.private_log('private-api-' + str(index) + '.log', result.stdout + result.stderr)
            result = subprocess.run(['docker', 'exec', node, 'sh', '-c', 'for nsc_service in $(docker service ls -q); do docker service logs --raw --tail 100 "$nsc_service"; done'], capture_output=True, timeout=30)
            fixture.private_log('private-services-' + str(index) + '.log', result.stdout + result.stderr)
            if getattr(fixture, 'record', None):
                service_ids = [fixture.record['resources'][key]['id'] for key in ('start:controller', 'service:postgresql', 'start:nginx', 'start:nginx-sandbox') if key in fixture.record['resources']]
                result = subprocess.run(['docker', 'exec', node, 'docker', 'service', 'inspect', *service_ids], capture_output=True, timeout=30)
                fixture.private_log('private-specifications-' + str(index) + '.json', result.stdout)
                try:
                    fixture.private_log('private-specifications-1.47-' + str(index) + '.json', json.dumps([fixture.raw_engine(index, '/services/' + identifier) for identifier in service_ids]).encode())
                except (AssertionError, OSError, ValueError, subprocess.TimeoutExpired):
                    fixture.summary['diagnostics_unavailable'] = True
    finally:
        fixture.cleanup()
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(fixture.summary, indent=2) + '\n')
    print(json.dumps(fixture.summary))
    return 0 if fixture.summary['status'] == 'PASS' else 1


if __name__ == '__main__':
    raise SystemExit(main())
