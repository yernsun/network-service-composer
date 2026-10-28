#!/usr/bin/env python3
"""Run workspace browser acceptance inside a disposable Docker daemon.

Only the outer daemon container and its labelled network are created on the host.
Controller, gateways and all deployed workloads belong to the inner daemon.
Reports remain in coverage/workspace-e2e; fixture containers are always removed.
"""
import atexit
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

repository = Path(__file__).resolve().parent.parent
suffix = uuid.uuid4().hex[:10]
report = repository / 'coverage' / 'workspace-e2e' / suffix
report.mkdir(parents=True)
work = Path(tempfile.mkdtemp(prefix='nsc-workspace-e2e.'))
outer = 'nsc-workspace-' + suffix
network = outer + '-network'
controller_binary = Path(os.environ.get('NSC_CONTROLLER_BINARY', repository / 'bin/nsc-controller')).resolve()
agent_binary = Path(os.environ.get('NSC_AGENT_BINARY', repository / 'bin/nsc-agent')).resolve()
controller_image = os.environ.get('NSC_CONTROLLER_IMAGE', 'nsc-controller:v' + (repository / 'VERSION').read_text().strip())
gateway_image = os.environ.get('NSC_GATEWAY_IMAGE', 'nsc-gateway:v' + (repository / 'VERSION').read_text().strip())
nginx_image = os.environ.get('NSC_NGINX_IMAGE', 'nginx:alpine')
browser = os.environ.get('NSC_BROWSER_BINARY')
browser_grep = os.environ.get('NSC_WORKSPACE_GREP')
swarm = os.environ.get('NSC_LIFECYCLE_SWARM') == 'true'
runtime_only = os.environ.get('NSC_LIFECYCLE_API_ONLY') == 'true'
binding_trial = os.environ.get('NSC_BINDING_ACCEPTANCE') == 'true'
ports_trial = os.environ.get('NSC_PORT_ACCEPTANCE') == 'true'
compose_trial = os.environ.get('NSC_COMPOSE_ACCEPTANCE') == 'true' or ports_trial
if binding_trial and not swarm:
    raise RuntimeError('Binding acceptance requires NSC_LIFECYCLE_SWARM=true')
admin = 'workspace-isolated-' + suffix
created = []
inner = []
api = ''
result = {'status': 'RUNNING', 'profile': 'runtime-only' if runtime_only else 'workspace', 'report': str(report), 'started': time.time()}

def command(*args):
    completed = subprocess.run([str(arg) for arg in args], text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if completed.returncode:
        raise RuntimeError(completed.stdout[-8000:])
    return completed.stdout.strip()

def cleanup():
    if inner:
        for name in ['controller', 'production', 'sandbox']:
            log = subprocess.run(inner + ['logs', name], text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
            (report / (name + '.log')).write_text(log.stdout)
    for kind, name in reversed(created):
        subprocess.run(['docker', kind, 'rm', '-f', name] if kind == 'container' else ['docker', 'network', 'rm', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    shutil.rmtree(work, ignore_errors=True)
    result['finished'] = time.time()
    (report / 'summary.json').write_text(json.dumps(result, indent=2) + '\n')

atexit.register(cleanup)

def wait(check, label, timeout=90):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        try:
            value = check()
            if value:
                return value
        except (RuntimeError, urllib.error.URLError, ConnectionError):
            pass
        time.sleep(1)
    raise RuntimeError('Timed out: ' + label)

def request(method, path, body=None, extra=None):
    data = None if body is None else json.dumps(body).encode()
    headers = {'Authorization': 'Bearer ' + admin, 'Content-Type': 'application/json', **(extra or {})}
    with urllib.request.urlopen(urllib.request.Request(api + '/api/v1' + path, data=data, method=method, headers=headers), timeout=30) as response:
        return json.load(response)['data']

def main():
    global inner, api
    if not controller_binary.is_file() or not agent_binary.is_file():
        raise RuntimeError('Build Controller and Agent before running workspace E2E')
    if not browser or not Path(browser).is_file():
        raise RuntimeError('Set NSC_BROWSER_BINARY to an installed Chrome/Chromium')
    for image in [controller_image, gateway_image, nginx_image, 'docker:28-dind', 'postgres:18.6-alpine']:
        command('docker', 'image', 'inspect', image)
    command('docker', 'network', 'create', '--label', 'io.network-service-composer.e2e=true', network)
    created.append(('network', network))
    command('docker', 'run', '-d', '--privileged', '--name', outer, '--label', 'io.network-service-composer.e2e=true', '--network', network,
            '-p', '127.0.0.1::2375', '-e', 'DOCKER_TLS_CERTDIR=', 'docker:28-dind', '--tls=false')
    created.append(('container', outer))
    info = json.loads(command('docker', 'inspect', outer))[0]
    port = info['NetworkSettings']['Ports']['2375/tcp'][0]['HostPort']
    address = info['NetworkSettings']['Networks'][network]['IPAddress']
    inner = ['docker', '--host', 'tcp://127.0.0.1:' + port]
    wait(lambda: command(*inner, 'info'), 'private Docker daemon')
    command('docker', 'save', '-o', work / 'images.tar', controller_image, gateway_image, nginx_image, 'postgres:18.6-alpine')
    command(*inner, 'load', '-i', work / 'images.tar')
    worker_node = ''
    if swarm:
        command(*inner, 'swarm', 'init', '--advertise-addr', address)
        if binding_trial or compose_trial:
            worker = outer + '-worker'
            command('docker', 'run', '-d', '--privileged', '--name', worker, '--label', 'io.network-service-composer.e2e=true', '--network', network,
                    '-p', '127.0.0.1::2375', '-e', 'DOCKER_TLS_CERTDIR=', 'docker:28-dind', '--tls=false')
            created.append(('container', worker))
            worker_info = json.loads(command('docker', 'inspect', worker))[0]
            worker_address = worker_info['NetworkSettings']['Networks'][network]['IPAddress']
            worker_port = worker_info['NetworkSettings']['Ports']['2375/tcp'][0]['HostPort']
            worker_docker = ['docker', '--host', 'tcp://127.0.0.1:' + worker_port]
            wait(lambda: command(*worker_docker, 'info'), 'private worker daemon')
            command(*worker_docker, 'load', '-i', work / 'images.tar')
            token = command(*inner, 'swarm', 'join-token', '-q', 'worker')
            command(*worker_docker, 'swarm', 'join', '--token', token, '--advertise-addr', worker_address, address + ':2377')
            worker_node = command(*worker_docker, 'info', '--format', '{{.Swarm.NodeID}}')
            wait(lambda: command(*inner, 'node', 'inspect', worker_node, '--format', '{{.Status.State}}') == 'ready', 'private worker node')

    command(*inner, 'network', 'create', *(['--driver', 'overlay', '--attachable'] if swarm else []), 'nsc-workspace-ingress')
    gateway = address if swarm else json.loads(command(*inner, 'network', 'inspect', 'nsc-workspace-ingress'))[0]['IPAM']['Config'][0]['Gateway']
    files = work / 'files'
    files.mkdir()
    secrets = files / 'secrets'
    secrets.mkdir()
    shutil.copy2(controller_binary, files / 'nsc-controller')
    shutil.copy2(agent_binary, files / 'nsc-agent')
    command('env', 'CGO_ENABLED=0', 'go', 'build', '-o', files / 'workspace-dns', repository / 'scripts/testdata/workspace-dns/main.go')
    command(controller_binary, 'pki', 'init-ca', '--cert', secrets / 'ca.crt', '--key', secrets / 'ca.key', '--common-name', 'Workspace E2E CA', '--valid-for', '24h')
    command(controller_binary, 'pki', 'issue-controller', '--instance-id', 'controller', '--ca-cert', secrets / 'ca.crt', '--ca-key', secrets / 'ca.key', '--cert', secrets / 'controller.crt', '--key', secrets / 'controller.key', '--common-name', 'controller', '--dns', 'controller', '--valid-for', '2h')
    for file in secrets.iterdir():
        file.chmod(0o644)
    command('docker', 'cp', str(files) + '/.', outer + ':/fixtures')
    command(*inner, 'run', '-d', '--name', 'workspace-dns', '--network', 'nsc-workspace-ingress', '-v', '/fixtures:/fixtures:ro', '--entrypoint', '/fixtures/workspace-dns', controller_image)
    dns_address = json.loads(command(*inner, 'inspect', 'workspace-dns'))[0]['NetworkSettings']['Networks']['nsc-workspace-ingress']['IPAddress']
    env = {
        'NSC_STORAGE__DRIVER': 'sqlite', 'NSC_STORAGE__DSN': 'file:/tmp/workspace.db?_pragma=foreign_keys(1)&_time_format=sqlite', 'NSC_STORAGE__HA': 'false',
        'NSC_SECURITY__ADMIN_TOKEN': admin, 'NSC_SECURITY__SESSION_KEY': '01234567890123456789012345678901', 'NSC_SECURITY__PRIMARY_KEY_ID': 'v1',
        'NSC_SECURITY__ENCRYPTION_KEYS': '{"v1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}',
        'NSC_SERVER__GRPC_TLS_CERT_FILE': '/fixtures/secrets/controller.crt', 'NSC_SERVER__GRPC_TLS_KEY_FILE': '/fixtures/secrets/controller.key', 'NSC_SERVER__GRPC_CLIENT_CA_FILE': '/fixtures/secrets/ca.crt',
        'NSC_METRICS__LISTEN': '127.0.0.1:0', 'NSC_MANAGEMENT__ENABLED': 'true', 'NSC_MANAGEMENT__POLL_INTERVAL': '250ms', 'NSC_MANAGEMENT__DEPLOY_TIMEOUT': '90s',
        'NSC_CONTROLLER__INSTANCE_ID': 'controller',
        'NSC_CONTROLLER__INTERNAL__LISTEN': ':9444', 'NSC_CONTROLLER__INTERNAL__URL': 'https://controller:9444',
        'NSC_CONTROLLER__INTERNAL__CA_FILE': '/fixtures/secrets/ca.crt', 'NSC_CONTROLLER__INTERNAL__CERT_FILE': '/fixtures/secrets/controller.crt',
        'NSC_CONTROLLER__INTERNAL__KEY_FILE': '/fixtures/secrets/controller.key', 'NSC_CONTROLLER__INTERNAL__GRPC_ADDRESS': 'controller:9443',
        'NSC_CONTROLLER__DISCOVERY__RECONCILE__INTERVAL': '1s', 'NSC_CONTROLLER__DISCOVERY__FULL_RECONCILE__INTERVAL': '2s', 'NSC_CONTROLLER__ENDPOINT__READINESS__SUCCESS_THRESHOLD': '1',
    }
    command(*inner, 'run', '-d', '--name', 'database', '--network', 'nsc-workspace-ingress', '-e', 'POSTGRES_USER=nsc', '-e', 'POSTGRES_DB=nsc', '-e', 'POSTGRES_PASSWORD=' + admin, 'postgres:18.6-alpine')
    wait(lambda: command(*inner, 'exec', 'database', 'pg_isready', '-h', '127.0.0.1', '-U', 'nsc'), 'isolated PostgreSQL')
    env.update({'NSC_STORAGE__DRIVER': 'postgres', 'NSC_STORAGE__DSN': 'postgres://nsc:' + admin + '@database:5432/nsc?sslmode=disable', 'NSC_STORAGE__HA': 'true'})
    args = inner + ['run', '-d', '--name', 'controller', '--network', 'nsc-workspace-ingress', '-p', '8080:8080', '-v', '/fixtures:/fixtures:ro', '--entrypoint', '/fixtures/nsc-controller']
    for key, value in env.items():
        args.extend(['-e', key + '=' + value])
    command(*args, controller_image, 'serve', '--config=')
    controller_address = json.loads(command(*inner, 'inspect', 'controller'))[0]['NetworkSettings']['Networks']['nsc-workspace-ingress']['IPAddress']
    api = 'http://' + address + ':8080'
    wait(lambda: urllib.request.urlopen(api + '/readyz', timeout=2).status == 200, 'Controller ready')
    pool = request('POST', '/gateway-pools', {'name': 'Workspace E2E', 'backend_network': 'nsc-workspace-ingress', 'challenge_upstream': 'http://controller:8080'})
    # Overlay IPAM's gateway is not a Docker API listener. Swarm containers
    # reach this same private daemon through the outer fixture address.
    runtime_address = address if swarm else gateway
    runtime = request('POST', '/runtimes', {'pool_id': pool['id'], 'name': 'Isolated Docker', 'runtime_type': 'swarm' if swarm else 'docker', 'endpoint': 'tcp://' + runtime_address + ':2375', 'management_enabled': True})
    gateway_ids = {}
    for mode in ([] if compose_trial else ['production', 'sandbox']):
        gateway_id = command(controller_binary, 'id', 'gateway')
        gateway_ids[mode] = gateway_id
        if mode == 'sandbox':
            request('PUT', '/gateway-pools/' + pool['id'] + '/nginx/sandbox', {'gateway_id': gateway_id, 'checks': []}, {'If-Match': '"rev-0"'})
        command(controller_binary, 'pki', 'issue-agent', '--ca-cert', secrets / 'ca.crt', '--ca-key', secrets / 'ca.key', '--cert', secrets / (mode + '.crt'), '--key', secrets / (mode + '.key'), '--pool-id', pool['id'], '--gateway-id', gateway_id, '--valid-for', '2h')
        for file in [secrets / (mode + '.crt'), secrets / (mode + '.key')]:
            file.chmod(0o644)
            command('docker', 'cp', file, outer + ':/fixtures/secrets/' + file.name)
        args = inner + ['run', '-d', '--name', mode, '--network', 'host' if mode == 'sandbox' else 'nsc-workspace-ingress', '-v', '/fixtures:/fixtures:ro', '--entrypoint', '/fixtures/nsc-controller' if binding_trial else '/fixtures/nsc-agent']
        if mode == 'production':
            args.extend(['-p', '9080:80', '-p', '9443:443', '--sysctl', 'net.ipv4.ip_unprivileged_port_start=0'])
        agent_env = {'MODE': mode, 'GATEWAY_ID': gateway_id, 'POOL_ID': pool['id'], 'NAME': 'E2E ' + mode, 'CONTROLLER_ADDRESS': 'controller:9443', 'SERVER_NAME': 'controller', 'CA_CERT_FILE': '/fixtures/secrets/ca.crt', 'TLS_CERT_FILE': '/fixtures/secrets/' + mode + '.crt', 'TLS_KEY_FILE': '/fixtures/secrets/' + mode + '.key', 'STATE_DIR': '/tmp/nsc-state', 'HEARTBEAT_INTERVAL': '1s'}
        if mode == 'sandbox':
            agent_env['CONTROLLER_ADDRESS'] = controller_address + ':9443'
        for key, value in agent_env.items():
            args.extend(['-e', 'NSC_AGENT__' + key + '=' + value])
        args.extend(['-e', 'NSC_STORAGE__DRIVER=postgres', '-e', 'NSC_STORAGE__DSN=' + env['NSC_STORAGE__DSN']])
        if binding_trial:
            command(controller_binary, 'pki', 'issue-controller', '--instance-id', mode, '--ca-cert', secrets / 'ca.crt', '--ca-key', secrets / 'ca.key', '--cert', secrets / (mode + '-controller.crt'), '--key', secrets / (mode + '-controller.key'), '--common-name', mode, '--dns', mode, '--valid-for', '2h')
            for file in [secrets / (mode + '-controller.crt'), secrets / (mode + '-controller.key')]:
                file.chmod(0o644)
                command('docker', 'cp', file, outer + ':/fixtures/secrets/' + file.name)
            peer_env = {**env, 'NSC_CONTROLLER__INSTANCE_ID': mode, 'NSC_CONTROLLER__LOCAL_GATEWAY': 'true',
                        'NSC_SERVER__HTTP_LISTEN': ':18080', 'NSC_SERVER__GRPC_LISTEN': ':19443',
                        'NSC_SERVER__GRPC_TLS_CERT_FILE': '/fixtures/secrets/' + mode + '-controller.crt',
                        'NSC_SERVER__GRPC_TLS_KEY_FILE': '/fixtures/secrets/' + mode + '-controller.key',
                        'NSC_CONTROLLER__INTERNAL__LISTEN': ':19444', 'NSC_CONTROLLER__INTERNAL__URL': 'https://' + mode + ':19444',
                        'NSC_CONTROLLER__INTERNAL__GRPC_ADDRESS': mode + ':19443',
                        'NSC_CONTROLLER__INTERNAL__CERT_FILE': '/fixtures/secrets/' + mode + '-controller.crt',
                        'NSC_CONTROLLER__INTERNAL__KEY_FILE': '/fixtures/secrets/' + mode + '-controller.key'}
            if mode == 'sandbox':
                database_address = json.loads(command(*inner, 'inspect', 'database'))[0]['NetworkSettings']['Networks']['nsc-workspace-ingress']['IPAddress']
                args.extend(['--add-host', 'controller:' + controller_address, '--add-host', 'database:' + database_address])
            for key, value in peer_env.items():
                args.extend(['-e', key + '=' + value])
        command(*args, '-e', 'NSC_NGINX__WORKER_PROCESSES=1', '-e', 'NSC_NGINX__GENERATION_DIR=/tmp/nsc-state/generations', gateway_image, *(['serve'] if binding_trial else []), '--config=')
        if mode == 'sandbox':
            # This is the private daemon's host, never the developer's host.
            # Avoid Docker's special handling of localhost while retaining its
            # service DNS through the bridge resolver fixture.
            command(*inner, 'run', '-d', '--name', 'sandbox-dns', '--network', 'host', '-v', '/fixtures:/fixtures:ro', '--entrypoint', '/fixtures/workspace-dns', controller_image, '127.0.0.1:5353', dns_address + ':53')
    if not compose_trial:
        wait(lambda: request('GET', '/gateways/' + gateway_ids['production']).get('last_seen_at'), 'production gateway heartbeat')
    wait(lambda: request('GET', '/runtimes/' + runtime['id']).get('status') == 'ONLINE', 'runtime discovery online')
    wait(lambda: request('GET', '/runtimes/' + runtime['id'] + '/cluster/nodes'), 'node observation ready')
    pinned_image = json.loads(command('docker', 'image', 'inspect', nginx_image))[0]['RepoDigests'][0]
    command(*inner, 'pull', pinned_image)
    # The outer container also has Docker's embedded DNS redirection. Override
    # only this fixture namespace's UDP resolver destination after image loading.
    if not compose_trial: command('docker', 'exec', outer, 'iptables', '-t', 'nat', '-I', 'OUTPUT', '1', '-p', 'udp', '-d', '127.0.0.11', '--dport', '53', '-j', 'DNAT', '--to-destination', '127.0.0.1:5353')
    fixture = {'api': api, 'pool': pool, 'runtime': runtime, 'gateways': gateway_ids, 'traffic': 'http://' + address + ':9080', 'image': pinned_image, 'private_docker': inner[2], 'private_daemon_id': json.loads(command(*inner, 'info', '--format', '{{json .}}'))['ID'], 'swarm': swarm, 'worker_node': worker_node, 'https_address': address, 'https_port': 9443}
    fixture_path = report / 'fixture.json'
    fixture_path.write_text(json.dumps(fixture, indent=2))
    test_env = {**os.environ, 'E2E_BASE_URL': api, 'E2E_ADMIN_PASSWORD': admin, 'NSC_WORKSPACE_FIXTURE': str(fixture_path), 'NSC_WORKSPACE_REPORT': str(report)}
    result['browser_exit_code'] = None
    result['browser_status'] = 'NOT_REQUESTED'
    if not runtime_only:
        with (report / 'playwright.log').open('w') as log:
            completed = subprocess.run(['npm', 'run', 'e2e:workspace', *(['--', '--grep', browser_grep] if browser_grep else ['--', '--grep', 'port publication trial'] if ports_trial else ['--', '--grep', 'incremental compose trial'] if os.environ.get('NSC_INCREMENTAL_ACCEPTANCE') == 'true' else ['--', '--grep', 'compose placement trial'] if compose_trial else ['--', '--grep', 'service binding trial'] if binding_trial else ['--', '--grep', 'project lifecycle|orphan project'] if swarm else [])], cwd=repository / 'frontend', env=test_env, stdout=log, stderr=subprocess.STDOUT)
        result['browser_exit_code'] = completed.returncode
        result['browser_status'] = 'PASS' if completed.returncode == 0 else 'FAIL'
    # These API/runtime cases create their own projects and remain useful
    # evidence when an independent browser assertion fails.
    if binding_trial or compose_trial or browser_grep:
        result['exit_code'] = result['browser_exit_code'] or 0
        result['status'] = 'PASS' if result['exit_code'] == 0 else 'FAIL'
        print(json.dumps(result), flush=True)
        if result['exit_code']: raise RuntimeError('Deployment acceptance failed; see ' + str(report))
        return
    with (report / 'lifecycle-runtime.log').open('w') as log:
        runtime_result = subprocess.run(['python3', str(repository / 'scripts/e2e-project-lifecycle.py')], cwd=repository, env=test_env, stdout=log, stderr=subprocess.STDOUT)
    result['runtime_exit_code'] = runtime_result.returncode
    result['exit_code'] = (result['browser_exit_code'] or 0) or runtime_result.returncode
    result['status'] = 'PASS' if result['exit_code'] == 0 else 'FAIL'
    print(json.dumps(result), flush=True)
    if result['exit_code']:
        raise RuntimeError('Workspace acceptance failed; see ' + str(report))

try:
    main()
except BaseException as error:
    result['status'] = 'FAIL'
    result['error'] = str(error)
    print(json.dumps(result), flush=True)
    raise
