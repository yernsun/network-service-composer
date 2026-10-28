#!/usr/bin/env python3
"""Restore an explicit backup into a protected, internal-only rehearsal target.

This command never discovers or reads a live installation. Supply a consistent
archive, its expected checksum and the matching private configuration/material.
The caller owns backup creation and application-data compatibility review.
"""

import argparse
import base64
import copy
import datetime
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import time
import uuid


def write_private(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')
    path.chmod(0o600)


def archive_digest(path):
    digest = hashlib.sha256()
    with Path(path).open('rb') as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def validate_archive(path, expected):
    if not Path(path).is_file() or Path(path).is_symlink():
        raise ValueError('backup must be an explicit regular file')
    if len(expected) != 64 or any(character not in '0123456789abcdef' for character in expected):
        raise ValueError('expected backup SHA-256 is required')
    if archive_digest(path) != expected:
        raise ValueError('backup checksum does not match; restore was not started')


def recovery_config(source, driver, database, password):
    config = copy.deepcopy(source)
    storage = config.setdefault('storage', {})
    storage.update({'driver': driver, 'auto_migrate': False, 'ha': driver != 'sqlite'})
    if driver == 'postgres':
        storage['dsn'] = f'postgres://nsc:{password}@{database}:5432/nsc?sslmode=disable'
    elif driver == 'mysql':
        storage['dsn'] = f'nsc:{password}@tcp({database}:3306)/nsc?parseTime=true&multiStatements=true&charset=utf8mb4'
    elif driver == 'sqlite':
        storage['dsn'] = 'file:/drill/restored.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_time_format=sqlite'
    else:
        raise ValueError('unsupported backup driver')
    config.setdefault('controller', {})['recovery_mode'] = True
    config.setdefault('management', {})['enabled'] = False
    config.setdefault('server', {}).update({'http_listen': ':8080', 'http_tls_cert_file': '', 'http_tls_key_file': ''})
    # A stale copied bootstrap token must not authorize this restore. Existing
    # persisted identities are verified separately using the supplied token.
    security = config.setdefault('security', {})
    security['admin_token'] = secrets.token_urlsafe(32)
    security['admin_token_file'] = ''
    return config


PROBE = r"""
import { readFileSync } from 'node:fs';
const settings = JSON.parse(readFileSync('/drill/probe-input.json', 'utf8'));
const base = `http://${settings.controller}:8080`;
const results = {};
let cookies = '';
async function check(name, path, expected, options = {}) {
  const {cookieOnly, ...fetchOptions} = options;
  const response = await fetch(base + path, {
    ...fetchOptions,
    headers: { ...(settings.token && !cookieOnly ? {Authorization:`Bearer ${settings.token}`} : {}),
      ...(cookies ? {Cookie:cookies} : {}), 'Content-Type': 'application/json', ...(options.headers || {}) },
    signal: AbortSignal.timeout(5000)
  });
  const body = await response.text();
  if (response.status !== expected) throw new Error(`${name}: unexpected HTTP status ${response.status}`);
  if (name === 'resource_write' && JSON.parse(body).code !== 'recovery.read_only') throw new Error('resource_write: wrong stable error code');
  if (name === 'existing_identity_login' && !response.headers.get('Set-Cookie')) throw new Error('existing_identity_login: missing session cookie');
  if (name === 'existing_identity_login') cookies = response.headers.getSetCookie().map(value=>value.split(';')[0]).join('; ');
  if (name !== 'health' && name !== 'ready' && name !== 'unconfigured_bootstrap' && response.headers.get('X-NSC-Recovery-Mode') !== 'read-only') throw new Error(`${name}: missing recovery header`);
  results[name] = response.status;
}
await check('health', '/healthz', 200);
await check('ready', '/readyz', 503);
if (settings.password) {
  await check('existing_identity_login', '/api/v1/auth/login', 200, {method:'POST', body:JSON.stringify({username:settings.username,password:settings.password})});
  await check('restored_identity_cookie_get', '/api/v1/gateway-pools?limit=200', 200, {cookieOnly:true});
} else {
  results.existing_identity_login = 'NOT_SUPPLIED';
}
await check('restored_identity_get', '/api/v1/gateway-pools?limit=200', 200);
await check('resource_write', '/api/v1/gateway-pools', 503, {method:'POST', body:JSON.stringify({name:'must-not-persist'})});
await check('unconfigured_bootstrap', '/api/v1/gateway-pools', 401, {headers:{Authorization:`Bearer ${settings.unconfigured_token}`}});
console.log(JSON.stringify(results));
"""


class Drill:
    def __init__(self, args):
        self.args = args
        self.repository = Path(__file__).resolve().parent.parent
        self.output = Path(args.output).resolve()
        self.output.mkdir(mode=0o700, parents=True, exist_ok=False)
        self.output.chmod(0o700)
        self.images = json.loads((self.repository / 'build/image-lock.json').read_text())['images']
        suffix = uuid.uuid4().hex[:12]
        self.network = 'nsc-recovery-net-' + suffix
        self.database = 'nsc-recovery-db-' + suffix
        self.controller = 'nsc-recovery-controller-' + suffix
        self.containers = []
        self.created_network = False
        self.password = secrets.token_urlsafe(30)
        self.sequence = 0
        self.user = f'{os.getuid()}:{os.getgid()}'
        self.report = {'format': 1, 'status': 'FAIL', 'started_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                       'checks': [], 'limitations': ['Application-specific migration compatibility is not tested by Controller envelope validation.',
                                                    'Without supplied Gateway evidence, real Gateway loading and business access remain unverified.']}

    def run(self, *arguments, check=True, stdin=None, stdin_stream=None, output=None, merge_stderr=False):
        self.sequence += 1
        result = subprocess.run(arguments, input=stdin, stdin=stdin_stream, stdout=output or subprocess.PIPE,
                                stderr=subprocess.STDOUT if merge_stderr else subprocess.PIPE, timeout=180)
        if result.returncode:
            error_file = self.output / f'command-{self.sequence}-error.log'
            error_file.write_bytes(result.stderr or b'')
            error_file.chmod(0o600)
            if check:
                raise RuntimeError(f'isolated command failed (exit {result.returncode}); private diagnostics retained')
        return result.returncode, result.stdout or b''

    def docker(self, *arguments, **kwargs):
        return self.run('docker', *arguments, **kwargs)

    def cli(self, *arguments, config='isolated-config.json', check=True):
        command = ['run', '--rm', '--user', self.user, '--network', self.network,
                   '-v', f'{self.output}:/drill', '-v', f'{self.output / "material"}:/run/secrets:ro']
        if self.args.controller_binary:
            command.extend(['-v', f'{Path(self.args.controller_binary).resolve()}:/usr/local/bin/nsc-controller:ro'])
        command.extend([self.args.image, *arguments, '--config', '/drill/' + config])
        return self.docker(*command, check=check)

    def wait(self, predicate, label, timeout=90):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if predicate():
                return
            time.sleep(0.25)
        raise RuntimeError('isolated recovery timed out: ' + label)

    def create_database(self):
        if self.driver == 'sqlite':
            return
        self.containers.append(self.database)
        common = ['run', '-d', '--name', self.database, '--network', self.network,
                  '--label', 'io.network-service-composer.e2e=true']
        if self.driver == 'postgres':
            self.docker(*common, '-e', 'POSTGRES_USER=nsc', '-e', 'POSTGRES_DB=nsc',
                        '-e', 'POSTGRES_PASSWORD=' + self.password, self.images['postgres'])
            self.wait(lambda: self.docker('exec', self.database, 'pg_isready', '-h', '127.0.0.1', '-U', 'nsc', '-d', 'nsc', check=False)[0] == 0,
                      'PostgreSQL TCP listener')
        else:
            self.docker(*common, '-e', 'MYSQL_DATABASE=nsc', '-e', 'MYSQL_USER=nsc',
                        '-e', 'MYSQL_PASSWORD=' + self.password, '-e', 'MYSQL_ROOT_PASSWORD=' + self.password,
                        self.images['mysql'])
            self.wait(lambda: self.docker('exec', '-e', 'MYSQL_PWD=' + self.password, self.database,
                                          'mysql', '-h', '127.0.0.1', '-u', 'nsc', '-D', 'nsc', '-Nse', 'SELECT 1', check=False)[0] == 0,
                      'MySQL transactional database')

    def restore(self):
        validate_archive(self.args.backup, self.args.sha256)
        source = json.loads(Path(self.args.config).read_text())
        self.driver = self.args.driver or source.get('storage', {}).get('driver')
        if self.driver not in ('postgres', 'mysql', 'sqlite'):
            raise ValueError('backup driver must be postgres, mysql or sqlite')
        if source.get('storage', {}).get('driver') != self.driver:
            raise ValueError('backup/configuration driver mismatch')
        self.report.update({'driver': self.driver, 'backup_sha256': self.args.sha256,
                            'controller_image_id': self.docker('image', 'inspect', '-f', '{{.Id}}', self.args.image)[1].decode().strip()})
        self.args.image = self.report['controller_image_id']
        if self.args.controller_binary:
            self.report['controller_binary_sha256'] = archive_digest(self.args.controller_binary)
        archive = self.output / 'database.backup'
        shutil.copyfile(self.args.backup, archive)
        archive.chmod(0o600)
        validate_archive(archive, self.args.sha256)
        self.report['checks'].append('backup_checksum_verified_before_restore')
        material = self.output / 'material'
        material.mkdir(mode=0o700)
        if self.args.material_directory:
            source_material = Path(self.args.material_directory)
            for candidate in source_material.iterdir():
                if candidate.is_symlink() or not candidate.is_file():
                    raise ValueError('protected material must contain regular files only')
                shutil.copyfile(candidate, material / candidate.name)
                (material / candidate.name).chmod(0o600)
        self.docker('network', 'create', '--internal', '--label', 'io.network-service-composer.e2e=true', self.network)
        self.created_network = True
        self.create_database()
        if self.driver == 'postgres':
            with archive.open('rb') as source_archive:
                self.docker('exec', '-i', self.database, 'pg_restore', '-U', 'nsc', '-d', 'nsc', '--no-owner', '--no-privileges', stdin_stream=source_archive)
        elif self.driver == 'mysql':
            with archive.open('rb') as source_archive:
                self.docker('exec', '-i', '-e', 'MYSQL_PWD=' + self.password, self.database,
                            'mysql', '-u', 'nsc', '-D', 'nsc', stdin_stream=source_archive)
        else:
            shutil.copyfile(archive, self.output / 'restored.db')
            (self.output / 'restored.db').chmod(0o600)
        self.report['checks'].append('isolated_database_restore_completed')
        self.config = recovery_config(source, self.driver, self.database, self.password)
        write_private(self.output / 'isolated-config.json', self.config)
        password = Path(self.args.auth_password_file).read_text().strip() if self.args.auth_password_file else ''
        token = Path(self.args.auth_token_file).read_text().strip() if self.args.auth_token_file else ('' if password else source.get('security', {}).get('admin_token', ''))
        if not token and not password:
            raise ValueError('existing restored identity credentials are required for HTTP inspection')
        write_private(self.output / 'probe-input.json', {'controller': self.controller, 'token': token,
                                                       'password': password,
                                                       'username': self.args.auth_username or source.get('security', {}).get('admin_username', 'admin'),
                                                       'unconfigured_token': self.config['security']['admin_token']})
        (self.output / 'probe.mjs').write_text(PROBE)
        (self.output / 'probe.mjs').chmod(0o600)
        if self.args.evidence:
            shutil.copyfile(self.args.evidence, self.output / 'gateway-evidence.json')
            (self.output / 'gateway-evidence.json').chmod(0o600)
        if self.args.fixture_metadata:
            shutil.copyfile(self.args.fixture_metadata, self.output / 'fixture-metadata.json')
            (self.output / 'fixture-metadata.json').chmod(0o600)

    def verify_secrets(self):
        command = ['recovery-check']
        if self.args.evidence:
            command.extend(['--evidence', '/drill/gateway-evidence.json'])
        _, verified = self.cli(*command)
        self.report['verification'] = json.loads(verified)
        self.report['checks'].append('read_only_envelope_and_snapshot_check')
        wrong = copy.deepcopy(self.config)
        security = wrong['security']
        if security.get('encryption_keys_file'):
            key_file = self.output / 'material' / Path(security['encryption_keys_file']).name
            security['encryption_keys'] = json.loads(key_file.read_text())
            security['encryption_keys_file'] = ''
        security['encryption_keys'][security['primary_key_id']] = base64.b64encode(secrets.token_bytes(32)).decode()
        write_private(self.output / 'wrong-key-config.json', wrong)
        total = sum(self.report['verification']['decrypted_values'].values())
        code, _ = self.cli('recovery-check', config='wrong-key-config.json', check=False)
        if total > 0:
            error = (self.output / f'command-{self.sequence}-error.log').read_bytes() if code else b''
            if code == 0 or b'cannot decrypt' not in error:
                raise RuntimeError('wrong keyring accepted encrypted backup material')
            self.report['checks'].append('wrong_keyring_failed_closed')
        else:
            self.report['key_negative'] = 'NOT_APPLICABLE_NO_ENCRYPTED_MATERIAL'
        if self.report['verification']['pools']:
            pool = self.report['verification']['pools'][0]
            # Contract-negative evidence is deliberately synthetic. It cannot
            # establish real Gateway loading or make absent evidence verified.
            write_private(self.output / 'synthetic-newer-gateway.json', [{'gateway_id': 'synthetic-negative-only',
                'pool_id': pool['id'], 'applied_revision': pool['revision'] + 1, 'model_digest': pool['model_digest']}])
            code, _ = self.cli('recovery-check', '--evidence', '/drill/synthetic-newer-gateway.json', check=False)
            error = (self.output / f'command-{self.sequence}-error.log').read_bytes() if code else b''
            if code == 0 or b'older than or differs' not in error:
                raise RuntimeError('database older than synthetic Gateway revision was accepted')
            self.report['checks'].append('synthetic_newer_gateway_revision_rejected')
            self.report['negative_gateway_evidence_origin'] = 'SYNTHETIC_CONTRACT_CASE'

    def start_controller(self, name, config):
        self.containers.append(name)
        command = ['run', '-d', '--user', self.user, '--name', name, '--network', self.network,
                   '--label', 'io.network-service-composer.e2e=true', '-v', f'{self.output}:/drill',
                   '-v', f'{self.output / "material"}:/run/secrets:ro']
        if self.args.controller_binary:
            command.extend(['-v', f'{Path(self.args.controller_binary).resolve()}:/usr/local/bin/nsc-controller:ro'])
        self.docker(*command, self.args.image, 'serve', '--config', '/drill/' + config)

    def inspect_fixture(self, action='inspect'):
        if not self.args.fixture_helper:
            return None
        _, result = self.docker('run', '--rm', '--user', self.user, '--network', self.network,
                                '-v', f'{self.output}:/drill', '-v', f'{Path(self.args.fixture_helper).resolve()}:/fixture-helper:ro',
                                '--entrypoint', '/fixture-helper', self.args.image, '--action', action,
                                '--config', '/drill/isolated-config.json', '--metadata', '/drill/fixture-metadata.json')
        return json.loads(result)

    def protected_http(self):
        _, prepared = self.cli('recovery-prepare', '--output', '/drill/review.json')
        prepared = json.loads(prepared)
        self.report['preparation'] = prepared
        before = self.inspect_fixture()
        self.start_controller(self.controller, 'isolated-config.json')
        inspect = json.loads(self.docker('inspect', self.controller)[1])[0]
        if inspect['HostConfig'].get('PortBindings') or any('docker.sock' in mount.get('Source', '') for mount in inspect['Mounts']):
            raise RuntimeError('restore fixture acquired a published port or Docker socket')
        _, network = self.docker('network', 'inspect', self.network)
        if not json.loads(network)[0]['Internal']:
            raise RuntimeError('restore fixture network permits external egress')
        result = None
        def probe():
            nonlocal result
            code, raw = self.docker('run', '--rm', '--network', self.network, '-v', f'{self.output}:/drill:ro',
                                    self.images['node_frontend_builder'], 'node', '/drill/probe.mjs', check=False)
            if code == 0:
                result = json.loads(raw)
                return True
            return False
        self.wait(probe, 'protected HTTP contract', timeout=30)
        self.report['http'] = result
        self.docker('stop', '-t', '10', self.controller)
        _, after = self.cli('recovery-prepare', '--output', '/drill/review-after-inspection.json')
        if json.loads(after)['fingerprint'] != prepared['fingerprint']:
            raise RuntimeError('protected inspection changed restored business/control/audit state')
        inspected = self.inspect_fixture()
        if before is not None and inspected != before:
            raise RuntimeError('protected inspection changed pending operations or restored data')
        self.report['checks'].extend(['protected_http_and_restored_auth_verified', 'protected_inspection_database_unchanged',
                                      'internal_network_no_socket_no_published_ports'])
        self.report['fixture_before_inspection'] = before
        normal = copy.deepcopy(self.config)
        normal['controller']['recovery_mode'] = False
        write_private(self.output / 'normal-config.json', normal)
        blocked = self.controller + '-blocked-normal'
        self.start_controller(blocked, 'normal-config.json')
        self.wait(lambda: not json.loads(self.docker('inspect', blocked)[1])[0]['State']['Running'], 'startup barrier refusal', timeout=10)
        _, logs = self.docker('logs', blocked, merge_stderr=True)
        (self.output / 'blocked-startup.log').write_bytes(logs)
        (self.output / 'blocked-startup.log').chmod(0o600)
        state = json.loads(self.docker('inspect', blocked)[1])[0]['State']
        if state['ExitCode'] == 0 or b'held for recovery review' not in logs:
            raise RuntimeError('normal startup did not reject the durable recovery barrier')
        self.report['checks'].append('normal_startup_cannot_bypass_preparation_barrier')

    def review_fixture(self):
        if not self.args.fixture_helper:
            self.report['activation'] = 'NOT_PERFORMED_OPERATOR_REVIEW_REQUIRED'
            return
        plan = json.loads((self.output / 'review.json').read_text())
        metadata = json.loads((self.output / 'fixture-metadata.json').read_text())
        self.report['held_items'] = len(plan['items'])
        if len(plan['items']) < 4 or any(item['decision'] != 'hold' for item in plan['items']):
            raise RuntimeError('fixture restored tasks were not held by default')
        bad = copy.deepcopy(plan)
        bad['activation_reviewed'] = True
        interrupted = next(item for item in bad['items'] if item['id'] == metadata['interrupted_operation_id'])
        interrupted['decision'] = 'resume'
        write_private(self.output / 'interrupted-review.json', bad)
        code, _ = self.cli('recovery-release', '--review', '/drill/interrupted-review.json', check=False)
        if code == 0:
            raise RuntimeError('interrupted unknown operation was allowed to resume')
        bad = copy.deepcopy(plan)
        bad['activation_reviewed'] = True
        bad['fingerprint'] = '0' * 64
        write_private(self.output / 'stale-review.json', bad)
        code, _ = self.cli('recovery-release', '--review', '/drill/stale-review.json', check=False)
        if code == 0:
            raise RuntimeError('stale recovery review cleared protection')
        review = copy.deepcopy(plan)
        review['activation_reviewed'] = True
        approved = next(item for item in review['items'] if item['id'] == metadata['approved_operation_id'])
        approved['decision'] = 'resume'
        write_private(self.output / 'approved-review.json', review)
        self.cli('recovery-release', '--review', '/drill/approved-review.json')
        inspected = self.inspect_fixture()
        if inspected['prepared'] or inspected['operations'][metadata['approved_operation_id']]['held']:
            raise RuntimeError('approved operation was not selectively released')
        for key, value in inspected['operations'].items():
            if key != metadata['approved_operation_id'] and not value['held']:
                raise RuntimeError('unapproved restored operation lost its hold')
        claimed = self.inspect_fixture('claim-approved')
        if claimed['operations'][metadata['approved_operation_id']]['status'] != 'RUNNING':
            raise RuntimeError('explicitly approved queue item was not claimable')
        normal = self.controller + '-reviewed-normal'
        self.start_controller(normal, 'normal-config.json')
        self.wait(lambda: self.docker('run', '--rm', '--network', self.network, self.images['node_frontend_builder'],
            'node', '-e', f'fetch("http://{normal}:8080/readyz").then(r=>process.exit(r.status===200?0:1)).catch(()=>process.exit(1))',
            check=False)[0] == 0, 'explicit normal restart after review', timeout=30)
        time.sleep(2)
        # Freeze only this owned process's concurrent lease/audit bookkeeping;
        # Docker pause retains its live SQL advisory lock / SQLite process lock.
        # This makes complete before/after database fingerprints comparable.
        self.docker('pause', normal)
        try:
            active_before = self.inspect_fixture()
            code, _ = self.cli('recovery-prepare', '--output', '/drill/active-normal-plan.json', check=False)
            if code == 0 or (self.output / 'active-normal-plan.json').exists():
                raise RuntimeError('active normal Controller owner did not block recovery preparation before output')
            active_after = self.inspect_fixture()
            if active_after['fingerprint'] != active_before['fingerprint'] or active_after['prepared']:
                raise RuntimeError('refused active Controller preparation modified permanent business state')
            self.report['checks'].append('normal_controller_owner_lock_blocks_prepare_without_database_or_output_changes')
            self.report['active_owner_evidence'] = 'READY_200_THEN_PROCESS_PAUSED_WITH_LOCK_RETAINED_FOR_COMPLETE_FINGERPRINT_COMPARISON'
        finally:
            self.docker('unpause', normal)
        self.docker('stop', '-t', '10', normal)
        preserved = self.inspect_fixture()
        before = self.report['fixture_before_inspection']['operations']
        for key, value in preserved['operations'].items():
            if key != metadata['approved_operation_id']:
                expected = copy.deepcopy(before[key])
                expected['held'] = True
                if value != expected:
                    raise RuntimeError('normal startup rewrote unapproved restored operation history')
        self.report['checks'].extend(['interrupted_and_stale_reviews_failed_closed', 'selective_release_only_approved_queue',
                                      'unapproved_holds_and_history_survive_normal_restart', 'fixture_business_rows_restored'])
        self.report['activation'] = 'ISOLATED_FIXTURE_ONLY'

    def cleanup(self):
        cleanup = []
        for name in reversed(self.containers):
            code, _ = self.docker('rm', '-f', name, check=False)
            cleanup.append({'resource': 'owned_container', 'exit_code': code})
        if self.created_network:
            code, _ = self.docker('network', 'rm', self.network, check=False)
            cleanup.append({'resource': 'owned_internal_network', 'exit_code': code})
        self.report['cleanup'] = cleanup
        if any(item['exit_code'] != 0 for item in cleanup):
            self.report['status'] = 'FAIL'
            self.report['error'] = 'owned fixture resources did not clean up'
        self.report['finished_at'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        write_private(self.output / 'report.json', self.report)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--backup', required=True, help='explicit consistent database archive')
    parser.add_argument('--sha256', required=True, help='expected archive checksum from the private backup inventory')
    parser.add_argument('--config', required=True, help='matching private JSON configuration')
    parser.add_argument('--driver', choices=('postgres', 'mysql', 'sqlite'))
    parser.add_argument('--image', required=True, help='candidate Controller image with protected recovery commands')
    parser.add_argument('--controller-binary', help='optional current static candidate binary mounted over the image entrypoint')
    parser.add_argument('--material-directory', help='separately protected regular files referenced below /run/secrets')
    parser.add_argument('--auth-token-file', help='existing restored identity token; never a command argument')
    parser.add_argument('--auth-password-file', help='existing restored user password for cookie login; never a command argument')
    parser.add_argument('--auth-username', help='existing restored username; defaults to the backed-up administrator name')
    parser.add_argument('--evidence', help='optional actual Gateway revision/digest array')
    parser.add_argument('--output', required=True, help='new private report directory')
    parser.add_argument('--fixture-helper', help=argparse.SUPPRESS)
    parser.add_argument('--fixture-metadata', help=argparse.SUPPRESS)
    args = parser.parse_args()
    drill = Drill(args)
    try:
        drill.restore()
        drill.verify_secrets()
        drill.protected_http()
        drill.review_fixture()
        drill.report['status'] = 'PASS'
    except Exception as error:
        drill.report['error'] = str(error)
    finally:
        drill.cleanup()
    print('recovery_drill status=' + drill.report['status'] + ' report=' + str(drill.output / 'report.json'))
    return 0 if drill.report['status'] == 'PASS' else 1


if __name__ == '__main__':
    sys.exit(main())
