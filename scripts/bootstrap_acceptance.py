#!/usr/bin/env python3
"""Bootstrap acceptance in disposable SSH-enabled nested Docker nodes.

Only returned fixture container/network IDs are cleaned up. The host Swarm and
business services are never initialized, joined, updated or inspected.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile
import time


class BootstrapFixture:
    def __init__(self, args):
        self.args = args
        self.repository = Path(__file__).resolve().parent.parent
        self.work = Path(tempfile.mkdtemp(prefix='nsc-bootstrap-acceptance-'))
        self.work.chmod(0o700)
        self.name = 'nsc-bootstrap-' + secrets.token_hex(6)
        self.containers = []
        self.network = None
        self.nodes = []
        self.summary = {'schema_version': 1, 'status': 'RUNNING', 'checks': {},
                        'limitations': ['Nested nodes share one physical host; host and zone isolation are not established.',
                                        'OVH DNS verification is DISABLED.'], 'images': {}}

    def command(self, *args, timeout=180):
        result = subprocess.run(args, capture_output=True, timeout=timeout)
        if result.returncode:
            (self.work / 'private-command-error.log').write_bytes(result.stderr)
            raise AssertionError('fixture command failed; inspect private diagnostics')
        return result.stdout.decode().strip()

    def docker(self, *args, timeout=180):
        return self.command('docker', *args, timeout=timeout)

    def inner(self, index, *args):
        return self.docker('exec', self.nodes[index], 'docker', *args)

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
        shutil.copyfile(binary, self.work / 'nsc-controller')
        (self.work / 'nsc-controller').chmod(0o755)
        self.command('ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(self.work / 'ssh-key'))
        self.network = self.docker('network', 'create', '--label', 'io.nsc.bootstrap.fixture=' + self.name, self.name)
        registry = self.docker('run', '-d', '--network', self.network, '--label', 'io.nsc.bootstrap.fixture=' + self.name,
                               images['registry'])
        self.containers.append(registry)
        self.registry_address = self.address(registry) + ':5000'
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
        self.configuration = {'name': 'nsc', 'mode': 'single' if self.args.nodes == 1 else 'multi',
                              'maintenance_node': 'node-1', 'data_node': 'node-1', 'node_dir': '/var/lib/docker/nsc-bootstrap',
                              'console_port': 8443, 'known_hosts_file': '/fixture/known_hosts', 'images': self.refs,
                              'nodes': [{'name': 'node-' + str(index + 1), 'address': self.address(node),
                                         'ssh_user': 'root', 'ssh_port': 22, 'ssh_key_file': '/fixture/ssh-key',
                                         'manager': True} for index, node in enumerate(self.nodes)]}
        (self.work / 'install.json').write_text(json.dumps(self.configuration))
        (self.work / 'install.json').chmod(0o600)

    def cli(self, command, *args, timeout=600):
        return self.docker('exec', self.nodes[0], '/fixture/nsc-controller', 'bootstrap', command,
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
                (self.work / 'private-image-transfer.log').write_bytes(export_error + load_error)
                raise AssertionError('fixture image transfer failed; inspect private diagnostics')
        finally:
            for process in (load, export):
                if process is not None and process.poll() is None:
                    process.kill()
                    process.wait(timeout=15)

    def browser(self):
        password = self.docker('exec', self.nodes[0], 'cat', '/fixture/state/materials/admin-password')
        payload = {'url': 'https://' + self.address(self.nodes[0]) + ':8443',
                   'password': password, 'ip': self.address(self.nodes[0]),
                   'certificate_id': self.record['certificate_id'], 'pool_id': self.record['pool_id']}
        result = subprocess.run(['node', 'scripts/bootstrap_browser.mjs'],
                                input=json.dumps(payload).encode(), capture_output=True, timeout=120)
        (self.work / 'private-browser.log').write_bytes(result.stderr)
        assert result.returncode == 0, 'browser regression failed; inspect private diagnostics'
        self.summary['checks'].update({name: 'PASS' for name in json.loads(result.stdout)['checks']})

    def restore_archive(self):
        self.summary['stage'] = 'isolated_archive_restore'
        record = self.record
        receipt = record['resources']['backup']
        archive_name = receipt['id']
        assert Path(archive_name).name == archive_name
        dump = subprocess.run(['docker', 'exec', self.nodes[0], 'cat', '/fixture/state/backups/' + archive_name],
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
            (self.work / 'private-restore.log').write_bytes(result.stderr)
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
    parser.add_argument('--output', type=Path, default=Path('coverage/bootstrap/current/summary.json'))
    parser.add_argument('--keep', action='store_true')
    parser.add_argument('--browser', action='store_true', help='run fresh, isolated browser checks')
    args = parser.parse_args()
    fixture = BootstrapFixture(args)
    try:
        fixture.prepare()
        fixture.install()
        if args.browser:
            fixture.browser()
        fixture.restore_archive()
        assert fixture.summary['source_fingerprint'] == fixture.command('node', 'scripts/readiness/gate.mjs', 'fingerprint'), 'source changed during acceptance'
        fixture.summary['status'] = 'PASS'
    except Exception as error:
        fixture.summary['status'] = 'FAIL'
        fixture.summary['reason'] = type(error).__name__
        # Raw commands, private addresses, host paths and credentials stay private.
        (fixture.work / 'private-error.log').write_text(str(error))
        for index, node in enumerate(fixture.nodes):
            result = subprocess.run(['docker', 'exec', node, 'sh', '-c', 'cat /fixture/state/diagnostics/docker-response-*.json 2>/dev/null || true'], capture_output=True, timeout=30)
            (fixture.work / ('private-api-' + str(index) + '.log')).write_bytes(result.stdout + result.stderr)
            result = subprocess.run(['docker', 'exec', node, 'sh', '-c', 'for nsc_service in $(docker service ls -q); do docker service logs --raw --tail 100 "$nsc_service"; done'], capture_output=True, timeout=30)
            (fixture.work / ('private-services-' + str(index) + '.log')).write_bytes(result.stdout + result.stderr)
    finally:
        fixture.cleanup()
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(fixture.summary, indent=2) + '\n')
    print(json.dumps(fixture.summary))
    return 0 if fixture.summary['status'] == 'PASS' else 1


if __name__ == '__main__':
    raise SystemExit(main())
