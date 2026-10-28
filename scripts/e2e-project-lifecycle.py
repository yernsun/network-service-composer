#!/usr/bin/env python3
"""Real deletion and retained-volume checks inside e2e-workspace's private daemon."""
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.error
import urllib.request
import uuid

fixture = json.loads(Path(os.environ['NSC_WORKSPACE_FIXTURE']).read_text())
report = Path(os.environ['NSC_WORKSPACE_REPORT'])
endpoint = fixture['private_docker']
if not endpoint.startswith('tcp://127.0.0.1:') or endpoint.rsplit(':', 1)[-1] in ['2375', '2376']:
    raise RuntimeError('Expected the disposable daemon with an ephemeral loopback port')
docker = ['docker', '--host', endpoint]
def command(*args):
    done = subprocess.run(docker + list(args), text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if done.returncode:
        raise RuntimeError(done.stdout[-3000:])
    return done.stdout.strip()
if command('info', '--format', '{{.ID}}') != fixture['private_daemon_id']:
    raise RuntimeError('Private daemon identity changed')

class APIError(RuntimeError):
    def __init__(self, status, message):
        super().__init__(message)
        self.status = status

def api(method, path, body=None, headers=None, expected=200):
    req = urllib.request.Request(fixture['api'] + '/api/v1' + path, method=method,
        data=None if body is None else json.dumps(body).encode(), headers={'Authorization': 'Bearer ' + os.environ['E2E_ADMIN_PASSWORD'], 'Content-Type': 'application/json', **(headers or {})})
    try:
        with urllib.request.urlopen(req, timeout=45) as response:
            assert response.status == expected, (path, response.status, expected)
            return json.load(response)['data'] if response.status != 204 else None
    except urllib.error.HTTPError as error:
        content = error.read().decode()
        if error.code == expected:
            return json.loads(content)
        raise APIError(error.code, f'{method} {path}: {error.code} {content}') from error

def wait(read, status, timeout=180):
    until = time.monotonic() + timeout
    while time.monotonic() < until:
        value = read()
        if value.get('status') == status:
            return value
        if value.get('status') in ['FAILED', 'UNKNOWN', 'BLOCKED', 'ROLLBACK_FAILED']:
            raise RuntimeError(json.dumps(value))
        time.sleep(.5)
    raise RuntimeError('Timed out waiting for ' + status)

def deploy(prefix, slow=False, anonymous=False):
    slug = prefix + '-' + uuid.uuid4().hex[:8]
    p = api('POST', '/projects', {'name': slug, 'slug': slug, 'initial_environment': {'name': 'production'}}, {'Idempotency-Key': slug}, 201)
    e = api('GET', '/environments/' + p['initial_environment_id'])
    compose = 'services:\n  web:\n    image: ' + fixture['image'] + '\n    expose: ["80"]\n    volumes: ["data:/data"]\nvolumes:\n  data: {}\n'
    if anonymous:
        compose = compose.replace('data:/data', '/data').replace('volumes:\n  data: {}\n', '')
    if slow:
        compose = compose.replace('    expose:', '    stop_grace_period: 30s\n    command: ["/bin/sh", "-c", "trap \'\' TERM QUIT; nginx -g \'daemon off;\' & wait"]\n    expose:')
    reviewed = api('POST', '/environments/' + e['id'] + '/preflight', {'compose_yaml': compose})
    op = api('POST', '/environments/' + e['id'] + '/deployments', {'preflight_id': reviewed['preflight_id']}, {'If-Match': '"rev-' + str(e['revision']) + '"', 'Idempotency-Key': slug + '-deploy'}, 202)
    wait(lambda: api('GET', '/operations/' + op['id']), 'SUCCEEDED')
    return p, api('GET', '/environments/' + e['id'])

results = []
def record(value):
    results.append(value)
    (report / 'lifecycle-runtime-results.json').write_text(json.dumps(results, indent=2) + '\n')

def preview_ready(project, timeout=45, force=True):
    until = time.monotonic() + timeout
    while time.monotonic() < until:
        preview = api('POST', '/projects/' + project['id'] + '/deletion-previews', {'force': force})
        if not preview['blockers']:
            return preview
        time.sleep(1)
    raise RuntimeError('Preview blockers: ' + json.dumps(preview['blockers']))

def submit(project, preview):
    mode = preview['force']
    for attempt in range(3):
        try:
            task = api('DELETE', '/projects/' + project['id'], {'preview_id': preview['id'], 'force': mode, 'confirm_slug': project['slug']},
                {'If-Match': '"rev-' + str(preview['project_revision']) + '"', 'Idempotency-Key': project['id'] + '-delete'}, 202)
            task['_preview_retries'] = attempt
            return task
        except APIError as error:
            # A 412 rejected the transaction. Reopen and confirm a fresh preview
            # of this isolated project; never retry an unknown execution result.
            if error.status != 412 or attempt == 2:
                raise
            preview = preview_ready(project, force=mode)

def resume(project, task):
    reviewed = preview_ready(project)
    return api('POST', '/project-deletions/' + task['id'] + '/resume', {'preview_id': reviewed['id'], 'force': True, 'confirm_slug': project['slug']}, {'If-Match': '"rev-' + str(task['revision']) + '"'}, 202)

for force in [False, True]:
    p, env = deploy('force-delete' if force else 'normal-delete')
    volume = env['project_name'] + '_data'
    command('run', '--rm', '-v', volume + ':/data', fixture['image'], 'sh', '-c', 'printf retained-project-data > /data/sentinel')
    blocked = api('POST', '/projects/' + p['id'] + '/deletion-previews', {'force': False})
    assert any(b['code'] == 'project.deletion.running' for b in blocked['blockers']), blocked
    if not force:
        # Prepare actual stopped objects in this private daemon. Application
        # STOP removes Swarm services itself, which would not exercise ordinary
        # project deletion's removal of an existing stopped service object.
        if fixture['swarm']:
            objects = command('service', 'ls', '-q', '--filter', 'label=com.docker.stack.namespace=' + env['project_name']).splitlines()
            assert objects, 'No owned Swarm service to stop'
            for key in objects:
                command('service', 'scale', '--detach=true', key + '=0')
        else:
            objects = command('ps', '-aq', '--filter', 'label=com.docker.compose.project=' + env['project_name']).splitlines()
            assert objects, 'No owned Compose container to stop'
            command('stop', '--time', '30', *objects)
    # A successful stop operation does not prove all Swarm tasks have exited.
    # Obtain fresh runtime evidence until the ordinary-deletion precondition holds.
    preview = preview_ready(p, force=force)
    task = submit(p, preview)
    body = {'preview_id': task['preview']['id'], 'force': force, 'confirm_slug': p['slug']}
    headers = {'If-Match': '"rev-' + str(task['preview']['project_revision']) + '"', 'Idempotency-Key': p['id'] + '-delete'}
    final = wait(lambda: api('GET', '/project-deletions/' + task['id']), 'SUCCEEDED')
    assert api('DELETE', '/projects/' + p['id'], body, headers, 202)['id'] == task['id']
    api('GET', '/projects/' + p['id'], expected=404)
    command('volume', 'inspect', volume)
    assert command('run', '--rm', '-v', volume + ':/data:ro', fixture['image'], 'cat', '/data/sentinel') == 'retained-project-data'
    command('image', 'inspect', fixture['image'])
    if fixture['swarm']:
        assert not command('service', 'ls', '-q', '--filter', 'label=com.docker.stack.namespace=' + env['project_name'])
        assert not command('ps', '-aq', '--filter', 'label=com.docker.swarm.service.name=' + env['project_name'] + '_web')
    else:
        assert not command('ps', '-aq', '--filter', 'label=com.docker.compose.project=' + env['project_name'])
    # Subsequent discovery must not recreate a live project/service relationship.
    time.sleep(3)
    api('GET', '/projects/' + p['id'], expected=404)
    record({'mode': 'force' if force else 'normal', 'task_id': task['id'], 'status': final['status'], 'volume': volume, 'sentinel': 'retained', 'runtime': 'swarm' if fixture['swarm'] else 'compose', 'reviewed_precondition_retries': task['_preview_retries']})

# Stop only this fixture's Gateway after a reviewed deletion starts. Workload
# removal must remain recorded, while missing loaded-config evidence blocks
# finalization. Restarting the Gateway plus a new preview permits continuation.
p, env = deploy('gateway-failure', slow=True)
services = api('GET', '/services?limit=200')
service = next(s for s in services if s.get('application_id') == env['application_id'])
host = p['slug'] + '.example.test'
# Use the managed binding operation: the Controller owns attachment to the
# protected Gateway network. Its bounded fixture deadline includes the
# deliberate 30-second graceful exit during Swarm rolling convergence.
binding = api('POST', '/services/' + service['id'] + '/domain-bindings', {'domain': {'host': host, 'entrypoints': ['http'], 'tls': {'mode': 'disabled'}}, 'route': {'path': '/', 'backend': {'port': 80, 'protocol': 'http'}}}, expected=202)
wait(lambda: api('GET', '/operations/' + binding['operation']['id']), 'SUCCEEDED')
domain = next(d for d in api('GET', '/domains?limit=200') if d['host'] == host)
task = submit(p, preview_ready(p))
read_task = lambda: api('GET', '/project-deletions/' + task['id'])
wait(read_task, 'RUNNING')
command('stop', '--time', '1', 'production')
try:
    failed = wait(read_task, 'UNKNOWN')
    assert failed['phase'] == 'INGRESS' and all(a['status'] == 'SUCCEEDED' for a in failed['preview']['applications']), failed
    api('GET', '/projects/' + p['id'])
    blocked = api('POST', '/projects/' + p['id'] + '/deletion-previews', {'force': True})
    assert any(b['code'] == 'project.deletion.gateway_unavailable' for b in blocked['blockers']), blocked
finally:
    command('start', 'production')
task = resume(p, failed)
final = wait(read_task, 'SUCCEEDED')
assert len(final['attempts']) == 2 and final['attempts'][0]['status'] == 'UNKNOWN', final
api('GET', '/domains/' + domain['id'], expected=404)
record({'scenario': 'controlled gateway outage after real workload removal', 'task_id': task['id'], 'status': final['status'], 'completed_targets_retained': True, 'fresh_preview_resume': True})

# Abrupt Controller loss leaves a durable RUNNING task. The replacement worker
# must mark it unknown, and a read alone must never resend a destructive command.
p, env = deploy('controller-interruption', slow=True)
task = submit(p, preview_ready(p))
read_task = lambda: api('GET', '/project-deletions/' + task['id'])
wait(read_task, 'RUNNING')
command('kill', '--signal', 'KILL', 'controller')
command('start', 'controller')
until = time.monotonic() + 30
while True:
    try:
        interrupted = read_task()
        if interrupted['status'] == 'UNKNOWN':
            break
    except (urllib.error.URLError, ConnectionError, RuntimeError):
        pass
    if time.monotonic() > until:
        raise RuntimeError('Controller did not recover the interrupted deletion as unknown')
    time.sleep(1)
assert interrupted['error_code'] == 'project.deletion.interrupted', interrupted
api('POST', '/applications/' + env['application_id'] + '/operations', {'action': 'start'}, {'Idempotency-Key': p['id'] + '-blocked-start'}, expected=409)
# The daemon may still be completing the already accepted graceful stop. This
# wait issues reads only, then the fresh reviewed resume reconciles actual state.
time.sleep(32)
assert read_task()['status'] == 'UNKNOWN'
task = resume(p, read_task())
final = wait(read_task, 'SUCCEEDED')
assert len(final['attempts']) == 2 and final['attempts'][0]['status'] == 'UNKNOWN'
record({'scenario': 'controlled Controller interruption during real deletion', 'task_id': task['id'], 'status': final['status'], 'unknown_requires_resume': True, 'concurrent_start_blocked': True})

# Do not pin the anonymous volume with a second container while checking the
# blocker: that would mask Swarm's automatic anonymous-volume removal behavior.
p, env = deploy('anonymous-volume', anonymous=True)
selector = ('com.docker.swarm.service.name=' + env['project_name'] + '_web') if fixture['swarm'] else ('com.docker.compose.project=' + env['project_name'])
containers = command('ps', '-q', '--filter', 'label=' + selector).splitlines()
assert len(containers) == 1, containers
container = containers[0]
mounted = json.loads(command('inspect', container))[0]['Mounts']
volume = next(m['Name'] for m in mounted if m['Destination'] == '/data')
command('exec', container, 'sh', '-c', 'printf retained-anonymous-data > /data/sentinel')
if fixture['swarm']:
    for force in [False, True]:
        preview = api('POST', '/projects/' + p['id'] + '/deletion-previews', {'force': force})
        assert any(b['code'] == 'project.deletion.data_retention' for b in preview['blockers']), preview
        rejected = api('DELETE', '/projects/' + p['id'], {'preview_id': preview['id'], 'force': force, 'confirm_slug': p['slug']},
            {'If-Match': '"rev-' + str(preview['project_revision']) + '"', 'Idempotency-Key': p['id'] + '-unsafe-' + str(force)}, expected=400)
        assert rejected['code'] == 'project.deletion.data_retention', rejected
    assert command('exec', container, 'cat', '/data/sentinel') == 'retained-anonymous-data'
    assert not api('GET', '/projects/' + p['id']).get('deletion_id')
    command('volume', 'inspect', volume)
    record({'scenario': 'Swarm anonymous volume blocks ordinary and forced deletion', 'status': 'PASSED', 'workload_unchanged': True, 'volume': volume, 'sentinel': 'retained'})
else:
    task = submit(p, preview_ready(p))
    wait(lambda: api('GET', '/project-deletions/' + task['id']), 'SUCCEEDED')
    command('volume', 'inspect', volume)
    assert command('run', '--rm', '-v', volume + ':/data:ro', fixture['image'], 'cat', '/data/sentinel') == 'retained-anonymous-data'
    api('GET', '/projects/' + p['id'], expected=404)
    record({'scenario': 'Compose removal retains anonymous volume', 'status': 'SUCCEEDED', 'volume': volume, 'sentinel': 'retained'})
print(json.dumps(results), flush=True)
