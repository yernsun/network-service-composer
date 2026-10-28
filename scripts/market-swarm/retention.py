"""Verify remote data identity and destructive admission in disposable nodes."""
import hashlib
import os
import atexit

from common import *

report = pathlib.Path('coverage/remote-retention') / prefix
report.mkdir(parents=True, mode=0o700)
os.chmod(report, 0o700)
results = []
summary_path = report / 'summary.json'


def save(status):
    summary = {'status': status, 'cases': results,
               'controller_binary_sha256': hashlib.sha256(pathlib.Path(state['binary']).read_bytes()).hexdigest(),
               'controller_image_id': cmd('docker', 'image', 'inspect', '--format', '{{.Id}}', state['image'])}
    summary_path.write_text(json.dumps(summary, indent=2) + '\n')
    os.chmod(summary_path, 0o600)


def retain_failure():
    if summary_path.exists() and json.loads(summary_path.read_text())['status'] == 'RUNNING':
        save('FAIL')


save('RUNNING')
atexit.register(retain_failure)


def record(scenario):
    results.append({'scenario': scenario, 'status': 'PASS'})
    save('RUNNING')


def eventually(read, label, timeout=90):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = read()
        if value:
            return value
        time.sleep(1)
    raise RuntimeError('Timed out: ' + label)


def rejects_delete(project, preview):
    request = urllib.request.Request(base + '/api/v1/projects/' + project['id'],
        method='DELETE', data=json.dumps({'preview_id': preview['id'], 'force': preview['force'],
                                         'confirm_slug': project['slug']}).encode(),
        headers={'Authorization': 'Bearer ' + secret, 'Content-Type': 'application/json',
                 'If-Match': '"rev-' + str(preview['project_revision']) + '"',
                 'Idempotency-Key': str(uuid.uuid4())})
    try:
        with opener.open(request, timeout=30) as response:
            raise RuntimeError('Unsafe deletion accepted: ' + str(response.status))
    except urllib.error.HTTPError as error:
        problem = json.load(error)
        if error.code != 400 or problem.get('code') != 'project.deletion.data_retention':
            raise RuntimeError('Wrong deletion rejection: ' + str(error.code) + ' ' + str(problem.get('code')))


def deployment(project):
    environment = api('GET', '/environments/' + project['initial_environment_id'])
    compose = ('services:\n  data:\n    image: ' + image +
               '\n    command: ["sleep", "600"]\n    volumes: ["data:/data"]\n'
               '    deploy:\n      replicas: 1\n      placement:\n        constraints: ["node.id == ' + node + '"]\n'
               '      update_config: {order: stop-first}\n      rollback_config: {order: stop-first}\n'
               'volumes:\n  data: {}\n')
    reviewed = api('POST', '/environments/' + environment['id'] + '/preflight',
                   {'compose_yaml': compose, 'deployment_plan': {'data_node_id': node}})
    wait(api('POST', '/environments/' + environment['id'] + '/deployments',
             {'preflight_id': reviewed['preflight_id']}, {'If-Match': '"rev-' + str(environment['revision']) + '"'}))
    environment = api('GET', '/environments/' + environment['id'])
    return environment, environment['application_id'], environment['project_name'] + '_data'


def create_project(suffix):
    name = prefix + '-' + suffix
    return api('POST', '/projects', {'name': name, 'slug': name, 'default_runtime_id': runtime['id'],
                                    'initial_environment': {'name': 'production'}})


def sentinel(volume):
    # An absent volume must not be recreated by Docker's mount convenience.
    inner(worker, 'volume', 'inspect', volume)
    return inner(worker, 'run', '--rm', '-v', volume + ':/data:ro', image, 'cat', '/data/sentinel')


for attempt in range(90):
    try:
        api('GET', '/settings')
        break
    except RuntimeError:
        time.sleep(1)
else:
    raise RuntimeError('Controller not ready')

# Archive loading drops RepoDigests. Push the cached content into a loopback
# registry on each private Engine; the resulting canonical reference is valid
# for both preflight's registry pin and Swarm's actual immutable image pull.
tag = '127.0.0.1:5009/nsc-retention:fixture'
digests = []
for target in [manager, worker]:
    inner(target, 'run', '-d', '--name', 'fixture-registry', '-p', '127.0.0.1:5009:5000', 'registry:2')
    inner(target, 'tag', 'alpine:3.24', tag)
    for attempt in range(15):
        try:
            inner(target, 'push', tag)
            break
        except RuntimeError:
            time.sleep(1)
    else:
        raise RuntimeError('Private registry fixture did not accept cached image')
    digests.append(inner(target, 'image', 'inspect', tag, '--format', '{{index .RepoDigests 0}}'))
if digests[0] != digests[1]:
    raise RuntimeError('Private node registries returned different image content')
image = digests[0]
network = prefix + '-retention-backend'
inner(manager, 'network', 'create', '--driver', 'overlay', '--attachable', network)
pool = api('POST', '/gateway-pools', {'name': prefix + '-retention', 'backend_network': network})
runtime = api('POST', '/runtimes', {'name': prefix + '-retention', 'pool_id': pool['id'],
                                   'runtime_type': 'swarm', 'endpoint': 'node://' + state['bindings'][0]['node_id'],
                                   'management_enabled': True})

project = create_project('retained')
environment, application, volume = deployment(project)
service = environment['project_name'] + '_data'
container = inner(worker, 'ps', '-q', '--filter', 'label=com.docker.swarm.service.name=' + service)
if not container:
    raise RuntimeError('Expected actual data container on remote Worker')
inner(worker, 'exec', container, 'sh', '-c', 'printf retained-original > /data/sentinel')
original = json.loads(inner(worker, 'volume', 'inspect', volume))[0]
wait(api('POST', '/applications/' + application + '/operations', {'action': 'stop'}))
if inner(manager, 'service', 'ls', '-q', '--filter', 'name=' + service):
    raise RuntimeError('STOP left a managed service')
wait(api('POST', '/applications/' + application + '/operations', {'action': 'start'}))
current = json.loads(inner(worker, 'volume', 'inspect', volume))[0]
if current['CreatedAt'] != original['CreatedAt'] or sentinel(volume) != 'retained-original':
    raise RuntimeError('STOP/START replaced remote retained data')
record('provisioned remote STOP/START preserves original volume identity and sentinel')

# Only the collector stops; the real Worker Engine and workload stay running.
# Cached encrypted observations expire and cannot authorize ordinary or force deletion.
inner(worker, 'stop', '--time', '5', 'fixture-controller')
try:
    time.sleep(35)
    service_id = inner(manager, 'service', 'inspect', service, '--format', '{{.ID}}')
    for force in [False, True]:
        preview = api('POST', '/projects/' + project['id'] + '/deletion-previews', {'force': force})
        if not any(item['code'] == 'project.deletion.data_retention' for item in preview['blockers']):
            raise RuntimeError('Expired remote observation did not block deletion')
        rejects_delete(project, preview)
    if inner(manager, 'service', 'inspect', service, '--format', '{{.ID}}') != service_id or sentinel(volume) != 'retained-original':
        raise RuntimeError('Blocked deletion changed the workload or retained data')
    if api('GET', '/projects/' + project['id']).get('deletion_id'):
        raise RuntimeError('Rejected deletion installed a project guard')
    record('offline collector and expired reports block ordinary/force deletion without mutation')
finally:
    inner(worker, 'start', 'fixture-controller')

preview = eventually(lambda: (lambda value: value if not value['blockers'] else None)(
    api('POST', '/projects/' + project['id'] + '/deletion-previews', {'force': True})), 'fresh remote deletion proof')
task = api('DELETE', '/projects/' + project['id'],
           {'preview_id': preview['id'], 'force': True, 'confirm_slug': project['slug']},
           {'If-Match': '"rev-' + str(preview['project_revision']) + '"'})


def completed_deletion():
    value = api('GET', '/project-deletions/' + task['id'])
    if value['status'] in ['FAILED', 'BLOCKED', 'UNKNOWN']:
        raise RuntimeError('Deletion did not retain a verified result: ' + value['status'])
    return value if value['status'] == 'SUCCEEDED' else None


eventually(completed_deletion, 'real remote task/container removal', timeout=180)
if sentinel(volume) != 'retained-original' or inner(manager, 'service', 'ls', '-q', '--filter', 'name=' + service):
    raise RuntimeError('Force deletion lost data or left the service')
record('fresh reviewed force deletion confirms remote exit and preserves data')

project = create_project('recreated')
environment, application, volume = deployment(project)
wait(api('POST', '/applications/' + application + '/operations', {'action': 'stop'}))
for container in inner(worker, 'ps', '-aq', '--filter', 'label=com.docker.stack.namespace=' + environment['project_name']).split():
    if inner(worker, 'inspect', '--format', '{{.State.Running}}', container) != 'false':
        raise RuntimeError('Owned task still running before deliberate fixture data replacement')
    inner(worker, 'rm', container)
old = json.loads(inner(worker, 'volume', 'inspect', volume))[0]
inner(worker, 'volume', 'rm', volume)
time.sleep(1)
inner(worker, 'volume', 'create', '--label', 'com.docker.stack.namespace=' + environment['project_name'], volume)
inner(worker, 'run', '--rm', '-v', volume + ':/data', image, 'sh', '-c', 'printf replacement-sentinel > /data/sentinel')
replacement = json.loads(inner(worker, 'volume', 'inspect', volume))[0]
if old['CreatedAt'] == replacement['CreatedAt']:
    raise RuntimeError('Replacement fixture did not change volume creation identity')
operation = api('POST', '/applications/' + application + '/operations', {'action': 'start'})
try:
    wait(operation)
except RuntimeError as error:
    if not str(error).startswith('FAILED:'):
        raise
else:
    raise RuntimeError('START adopted same-name replacement data')
if inner(manager, 'service', 'ls', '-q', '--filter', 'label=com.docker.stack.namespace=' + environment['project_name']):
    raise RuntimeError('Rejected START created a workload')
if sentinel(volume) != 'replacement-sentinel':
    raise RuntimeError('Rejected START overwrote replacement data')
record('same-name replacement blocks START before runtime writes and retains failed operation')
save('PASS')
print(json.dumps({'remote_retention': True, 'cases': len(results), 'report': str(report)}), flush=True)
