#!/usr/bin/env python3
"""Real PostgreSQL/MySQL/SQLite backups and protected restores in isolated fixtures."""

import argparse
import base64
import datetime
import hashlib
import json
import os
from pathlib import Path
import secrets
import sqlite3
import subprocess
import sys
import time
import uuid


def private_json(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')
    path.chmod(0o600)


class RecoveryFixture:
    def __init__(self, args):
        self.args = args
        self.repository = Path(__file__).resolve().parent.parent
        self.images = json.loads((self.repository / 'build/image-lock.json').read_text())['images']
        self.output = Path(args.output).resolve()
        self.output.mkdir(parents=True, mode=0o700, exist_ok=False)
        self.output.chmod(0o700)
        self.user = f'{os.getuid()}:{os.getgid()}'
        self.summary = {'format': 1, 'status': 'FAIL', 'started_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                        'drivers': [], 'scope': 'ISOLATED_CONTROL_DATABASE_AND_FIXTURE_BUSINESS_ROWS',
                        'real_application_migration_compatibility': 'NOT_TESTED', 'gateway_loading': 'NOT_TESTED',
                        'ovh': 'DISABLED'}
        self.sequence = 0
        self.helper = Path(args.helper).resolve() if args.helper else self.output / 'fixture-helper'

    def run(self, *arguments, stdout=None, check=True, env=None):
        self.sequence += 1
        result = subprocess.run(arguments, stdout=stdout or subprocess.PIPE, stderr=subprocess.PIPE, timeout=240,
                                cwd=self.repository, env=env)
        if result.returncode:
            path = self.output / f'command-{self.sequence}-error.log'
            path.write_bytes(result.stderr)
            path.chmod(0o600)
            if check:
                raise RuntimeError(f'isolated fixture command failed (exit {result.returncode}); see private diagnostics')
        return result.returncode, result.stdout or b''

    def build_helper(self):
        # The helper executes inside the Alpine controller image, even when the
        # runner has glibc and enables CGO by default.
        self.run(os.environ.get('GO', 'go'), 'build', '-trimpath', '-buildvcs=false',
                 '-o', str(self.helper), './scripts/recovery-fixture',
                 env={**os.environ, 'CGO_ENABLED': '0'})

    def docker(self, *arguments, **kwargs):
        return self.run('docker', *arguments, **kwargs)

    def wait(self, predicate, label):
        deadline = time.monotonic() + 90
        while time.monotonic() < deadline:
            if predicate():
                return
            time.sleep(0.25)
        raise RuntimeError('isolated source timed out: ' + label)

    def cli(self, directory, network, *arguments):
        command = ['run', '--rm', '--user', self.user, '--network', network, '-v', f'{directory}:/source',
                   '-v', f'{directory / "material"}:/run/secrets:ro']
        if self.args.controller_binary:
            command.extend(['-v', f'{Path(self.args.controller_binary).resolve()}:/usr/local/bin/nsc-controller:ro'])
        return self.docker(*command, self.args.image, *arguments)

    def setup_source(self, driver, directory, network, database, password):
        material = directory / 'material'
        material.mkdir(mode=0o700)
        self.docker('network', 'create', '--internal', '--label', 'io.network-service-composer.e2e=true', network)
        if driver != 'sqlite':
            command = ['run', '-d', '--name', database, '--network', network,
                       '--label', 'io.network-service-composer.e2e=true']
            if driver == 'postgres':
                self.docker(*command, '-e', 'POSTGRES_USER=nsc', '-e', 'POSTGRES_DB=nsc',
                            '-e', 'POSTGRES_PASSWORD=' + password, self.images['postgres'])
                self.wait(lambda: self.docker('exec', database, 'pg_isready', '-h', '127.0.0.1', '-U', 'nsc', '-d', 'nsc', check=False)[0] == 0,
                          'PostgreSQL TCP readiness')
                dsn = f'postgres://nsc:{password}@{database}:5432/nsc?sslmode=disable'
            else:
                self.docker(*command, '-e', 'MYSQL_DATABASE=nsc', '-e', 'MYSQL_USER=nsc',
                            '-e', 'MYSQL_PASSWORD=' + password, '-e', 'MYSQL_ROOT_PASSWORD=' + password,
                            self.images['mysql'])
                self.wait(lambda: self.docker('exec', '-e', 'MYSQL_PWD=' + password, database,
                                              'mysql', '-h', '127.0.0.1', '-u', 'nsc', '-D', 'nsc', '-Nse', 'SELECT 1', check=False)[0] == 0,
                          'MySQL transactional readiness')
                dsn = f'nsc:{password}@tcp({database}:3306)/nsc?parseTime=true&multiStatements=true&charset=utf8mb4'
        else:
            dsn = 'file:/source/source.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_time_format=sqlite'
        token = secrets.token_urlsafe(32)
        (directory / 'auth-token').write_text(token)
        (directory / 'auth-token').chmod(0o600)
        config = {
            'storage': {'driver': driver, 'dsn': dsn, 'ha': driver != 'sqlite', 'auto_migrate': False},
            'server': {'http_listen': ':8080', 'grpc_listen': '', 'public_url': 'http://controller.example.test:8080'},
            'management': {'enabled': True},
            'security': {'admin_username': 'admin', 'admin_token': token, 'session_key': secrets.token_hex(16),
                         'primary_key_id': 'backup', 'encryption_keys': {'backup': base64.b64encode(secrets.token_bytes(32)).decode()}},
            'controller': {'recovery_mode': False, 'local_gateway': False},
        }
        if driver != 'sqlite':
            self.cli(directory, network, 'pki', 'init-ca', '--cert', '/source/material/ca.crt', '--key', '/source/material/ca.key',
                     '--common-name', 'isolated recovery fixture', '--valid-for', '24h')
            self.cli(directory, network, 'pki', 'issue-controller', '--instance-id', 'recovery-fixture',
                     '--ca-cert', '/source/material/ca.crt', '--ca-key', '/source/material/ca.key', '--cert', '/source/material/controller.crt',
                     '--key', '/source/material/controller.key', '--dns', 'controller.example.test', '--valid-for', '2h')
            config['controller'].update({'instance_id': 'recovery-fixture', 'internal': {
                'listen': ':9444', 'url': 'https://controller.example.test:9444',
                'ca_file': '/run/secrets/ca.crt', 'cert_file': '/run/secrets/controller.crt', 'key_file': '/run/secrets/controller.key'}})
        private_json(directory / 'source-config.json', config)
        self.cli(directory, network, 'migrate', '--config', '/source/source-config.json')
        self.docker('run', '--rm', '--user', self.user, '--network', network, '-v', f'{directory}:/source',
                    '-v', f'{directory / "material"}:/run/secrets:ro', '-v', f'{self.helper}:/fixture-helper:ro',
                    '--entrypoint', '/fixture-helper', self.args.image, '--action', 'seed',
                    '--config', '/source/source-config.json', '--metadata', '/source/fixture-metadata.json')
        return config

    def driver(self, driver):
        print('recovery_e2e driver=' + driver + ' stage=backup_restore', flush=True)
        directory = self.output / driver
        directory.mkdir(mode=0o700)
        suffix = uuid.uuid4().hex[:12]
        network = 'nsc-recovery-source-net-' + suffix
        database = 'nsc-recovery-source-db-' + suffix
        password = secrets.token_urlsafe(30)
        result = {'driver': driver, 'status': 'FAIL'}
        try:
            self.setup_source(driver, directory, network, database, password)
            archive = directory / 'database.backup'
            if driver == 'postgres':
                with archive.open('wb') as output:
                    self.docker('exec', database, 'pg_dump', '-U', 'nsc', '-d', 'nsc', '--format=custom',
                                '--no-owner', '--no-privileges', stdout=output)
                result['backup_method'] = 'POSTGRES_CONSISTENT_LOGICAL_DUMP'
            elif driver == 'mysql':
                with archive.open('wb') as output:
                    self.docker('exec', '-e', 'MYSQL_PWD=' + password, database, 'mysqldump', '-u', 'nsc',
                                '--single-transaction', '--skip-lock-tables', '--skip-add-locks', '--no-tablespaces', 'nsc', stdout=output)
                result['backup_method'] = 'MYSQL_TRANSACTIONAL_LOGICAL_DUMP_NO_OTHER_WRITERS'
            else:
                source = sqlite3.connect('file:' + str(directory / 'source.db') + '?mode=ro', uri=True)
                target = sqlite3.connect(archive)
                try:
                    source.backup(target)
                finally:
                    target.close()
                    source.close()
                result['backup_method'] = 'SQLITE_BACKUP_API'
            archive.chmod(0o600)
            checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
            command = ['python3', str(self.repository / 'scripts/recovery-drill.py'), '--backup', str(archive),
                       '--config', str(directory / 'source-config.json'), '--driver', driver, '--image', self.args.image,
                       '--material-directory', str(directory / 'material'), '--auth-token-file', str(directory / 'auth-token'),
                       '--auth-password-file', str(directory / 'auth-token'),
                       '--fixture-helper', str(self.helper), '--fixture-metadata', str(directory / 'fixture-metadata.json')]
            if self.args.controller_binary:
                command.extend(['--controller-binary', self.args.controller_binary])
            code, _ = self.run(*command, '--sha256', '0' * 64, '--output', str(directory / 'checksum-rejection'), check=False)
            negative = json.loads((directory / 'checksum-rejection/report.json').read_text())
            if code == 0 or negative['checks'] or negative['cleanup']:
                raise RuntimeError('checksum failure did not stop before creating restore resources')
            _, raw = self.run(*command, '--sha256', checksum, '--output', str(directory / 'restore'))
            report = json.loads((directory / 'restore/report.json').read_text())
            if report['status'] != 'PASS':
                raise RuntimeError('isolated restore did not pass')
            counts = report['verification']['decrypted_values']
            expected = {'certificate': 0, 'acme_account': 1, 'application_revision': 3,
                        'operation': 3, 'control_document': 3, 'hook': 1}
            if counts != expected or report['verification']['gateway_evidence'] != 'not_supplied':
                raise RuntimeError('HTTP-only recovery omitted encrypted categories or invented Gateway evidence')
            if report['http'] != {'health': 200, 'ready': 503, 'restored_identity_get': 200, 'existing_identity_login': 200, 'restored_identity_cookie_get': 200,
                                   'resource_write': 503, 'unconfigured_bootstrap': 401}:
                raise RuntimeError('protected HTTP contract is incomplete')
            result.update({'status': 'PASS', 'backup_sha256': checksum, 'checksum_failure_before_restore': True,
                           'verification': report['verification'], 'checks': report['checks'],
                           'report': driver + '/restore/report.json'})
        except Exception as error:
            result['error'] = str(error)
        finally:
            cleanup = []
            if driver != 'sqlite':
                code, _ = self.docker('rm', '-f', database, check=False)
                cleanup.append(code)
            code, _ = self.docker('network', 'rm', network, check=False)
            cleanup.append(code)
            result['source_cleanup_exit_codes'] = cleanup
            if any(cleanup):
                result['status'] = 'FAIL'
            self.summary['drivers'].append(result)
        if result['status'] != 'PASS':
            raise RuntimeError(driver + ' recovery fixture failed; inspect private report')

    def execute(self):
        _, commit = self.run('git', 'rev-parse', 'HEAD')
        self.summary['source_commit'] = commit.decode().strip()
        self.summary['source_scripts_sha256'] = {name: hashlib.sha256((self.repository / name).read_bytes()).hexdigest()
            for name in ('scripts/recovery-drill.py', 'scripts/e2e-recovery.py', 'scripts/recovery-fixture/main.go')}
        self.summary['controller_image_id'] = self.docker('image', 'inspect', '-f', '{{.Id}}', self.args.image)[1].decode().strip()
        self.args.image = self.summary['controller_image_id']
        if self.args.controller_binary:
            self.summary['controller_binary_sha256'] = hashlib.sha256(Path(self.args.controller_binary).read_bytes()).hexdigest()
        if not self.args.helper:
            self.build_helper()
        self.summary['fixture_helper_sha256'] = hashlib.sha256(self.helper.read_bytes()).hexdigest()
        for driver in [self.args.driver] if self.args.driver else ('postgres', 'mysql', 'sqlite'):
            self.driver(driver)
        self.summary['status'] = 'PASS'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    version = (Path(__file__).resolve().parent.parent / 'VERSION').read_text().strip()
    parser.add_argument('--image', default=os.environ.get('NSC_CONTROLLER_IMAGE', 'nsc-controller:v' + version))
    parser.add_argument('--controller-binary', help='optional current static candidate binary for fixture development')
    parser.add_argument('--helper', help='optional already compiled current scripts/recovery-fixture helper')
    parser.add_argument('--driver', choices=('postgres', 'mysql', 'sqlite'), help='default: exercise all three stores')
    parser.add_argument('--output', default='coverage/recovery-e2e/' + datetime.datetime.now().strftime('%Y%m%dT%H%M%S') + '-' + str(os.getpid()))
    args = parser.parse_args()
    fixture = RecoveryFixture(args)
    try:
        fixture.execute()
    except Exception as error:
        fixture.summary['error'] = str(error)
    finally:
        fixture.summary['finished_at'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        private_json(fixture.output / 'summary.json', fixture.summary)
    print('recovery_e2e status=' + fixture.summary['status'] + ' report=' + str(fixture.output / 'summary.json'), flush=True)
    return 0 if fixture.summary['status'] == 'PASS' else 1


if __name__ == '__main__':
    sys.exit(main())
