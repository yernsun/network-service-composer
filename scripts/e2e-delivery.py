#!/usr/bin/env python3
"""Isolated Compose + Registry regression. Never adopts existing applications."""
import atexit
import base64
import hashlib
import hmac
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

root = Path(__file__).resolve().parent.parent
locked_images = json.loads((root / 'build/image-lock.json').read_text())['images']
dind_image = locked_images['docker_dind']
registry_image = locked_images['registry']
work = Path(tempfile.mkdtemp(prefix='nsc-delivery-e2e.'))
suffix = uuid.uuid4().hex[:8]
controller = 'nsc-delivery-controller-' + suffix
registry = 'nsc-delivery-registry-' + suffix
image = os.environ.get('NSC_CONTROLLER_IMAGE', 'nsc-controller:delivery')
base_image = os.environ.get('NSC_GATEWAY_IMAGE', 'nsc-gateway:delivery')
swarm = os.environ.get('NSC_DELIVERY_SWARM') == 'true'
private = os.environ.get('NSC_DELIVERY_PRIVATE') == 'true'
daemon = None
fixture_docker = ['docker']
networks = []
containers = []
projects = []
images = []
admin = 'isolated-delivery-' + suffix

def command(*args):
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if result.returncode:
        raise RuntimeError(result.stdout[-6000:])
    return result.stdout.strip()

def cleanup():
    for project, compose in projects:
        subprocess.run(fixture_docker+(['stack','rm',project] if swarm else ['compose','-p',project,'-f',str(compose),'down','--volumes','--remove-orphans']), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for name in reversed(containers):
        subprocess.run(['docker', 'rm', '-fv', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for network in networks:
        subprocess.run(['docker','network','rm',network],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    for ref in images:
        subprocess.run(fixture_docker+['image', 'rm', ref], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    shutil.rmtree(work, ignore_errors=True)
atexit.register(cleanup)

def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]

api_port, registry_port = port(), port()
api = f'http://127.0.0.1:{api_port}'
repo = f'127.0.0.1:{registry_port}/fixture'
config = work / 'controller.yaml'
credentials = work / 'registry.json'
credentials.write_text('{"auths":{}}')
storage_driver='sqlite'
storage_dsn='file:/tmp/delivery.db?_pragma=foreign_keys(1)&_time_format=sqlite'
config.write_text('registry_credentials:\n  isolated:\n    config_file: /fixtures/registry.json\n    insecure: true\n')
work.chmod(0o755)
credentials.chmod(0o444)
config.chmod(0o444)
registry_options=[]
if private:
    (work/'htpasswd').write_text('ci:$2a$04$J4qGclMegomaKXT/PWNi..UJId/l82oJLhrY.8Hv1DN1O.151BUzC\n')
    registry_options=['-v',str(work/'htpasswd')+':/auth/htpasswd:ro','-e','REGISTRY_AUTH=htpasswd','-e','REGISTRY_AUTH_HTPASSWD_REALM=Isolated','-e','REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd']
containers.append(registry)
command('docker', 'run', '-d', '--name', registry, '--network', 'host', '-e', f'REGISTRY_HTTP_ADDR=127.0.0.1:{registry_port}', *registry_options, registry_image)
if swarm:
    # A separate daemon owns this Swarm; never initialize/join the host Swarm.
    network='nsc-delivery-swarm-'+suffix
    command('docker','network','create',network);networks.append(network)
    # Registry listens on loopback in Compose mode; recreate the isolated fixture on this network.
    command('docker','rm','-f',registry)
    command('docker','run','-d','--name',registry,'--network',network,*registry_options,registry_image)
    registry_ip=json.loads(command('docker','inspect',registry))[0]['NetworkSettings']['Networks'][network]['IPAddress']
    repo=registry_ip+':5000/fixture'
    daemon='nsc-delivery-daemon-'+suffix;containers.append(daemon)
    command('docker','run','-d','--privileged','--name',daemon,'--network',network,'-p','127.0.0.1::2375','-e','DOCKER_TLS_CERTDIR=',dind_image,'--tls=false','--insecure-registry='+registry_ip+':5000')
    daemon_port=json.loads(command('docker','inspect',daemon))[0]['NetworkSettings']['Ports']['2375/tcp'][0]['HostPort']
    fixture_docker=['docker','--host','tcp://127.0.0.1:'+daemon_port]
    for _ in range(60):
        try:command(*fixture_docker,'info');break
        except RuntimeError:time.sleep(1)
    command(*fixture_docker,'swarm','init')
    # Import the already built fixture base without pulling production images.
    archive=work/'gateway.tar'
    command('docker','save','-o',str(archive),base_image)
    command(*fixture_docker,'load','-i',str(archive))
if swarm:
    database='nsc-delivery-postgres-'+suffix;containers.append(database)
    command('docker','run','-d','--name',database,'-p','127.0.0.1::5432','-e','POSTGRES_USER=nsc','-e','POSTGRES_PASSWORD=isolated','-e','POSTGRES_DB=nsc',locked_images['postgres'])
    for _ in range(60):
        try:command('docker','exec',database,'pg_isready','-U','nsc','-d','nsc');break
        except RuntimeError:time.sleep(1)
    database_port=json.loads(command('docker','inspect',database))[0]['NetworkSettings']['Ports']['5432/tcp'][0]['HostPort']
    storage_driver='postgres';storage_dsn='postgres://nsc:isolated@127.0.0.1:'+database_port+'/nsc?sslmode=disable'
if private:
    auth={'auths':{repo.split('/')[0]:{'auth':base64.b64encode(b'ci:isolated-password').decode()}}}
    credentials.chmod(0o644);credentials.write_text(json.dumps(auth));credentials.chmod(0o444)
    docker_config=work/'docker-config';docker_config.mkdir();(docker_config/'config.json').write_text(json.dumps(auth))
    fixture_docker[1:1]=['--config',str(docker_config)]
internal_options = []
if swarm:
    # Shared-store Controllers require their authenticated peer transport even
    # when this isolated fixture has only one Controller and disables Gateway RPC.
    instance = 'delivery-' + suffix
    internal_port = port()
    pki = ['docker', 'run', '--rm', '--network', 'none', '--user', f'{os.getuid()}:{os.getgid()}',
           '-v', str(work)+':/fixtures', image, 'pki']
    command(*pki, 'init-ca', '--cert', '/fixtures/ca.crt', '--key', '/fixtures/ca.key',
            '--common-name', 'NSC isolated delivery CA', '--valid-for', '24h')
    command(*pki, 'issue-controller', '--instance-id', instance, '--ca-cert', '/fixtures/ca.crt',
            '--ca-key', '/fixtures/ca.key', '--cert', '/fixtures/controller.crt', '--key', '/fixtures/controller.key',
            '--common-name', 'localhost', '--dns', 'localhost', '--valid-for', '2h')
    internal_options = ['-e', 'NSC_CONTROLLER__INSTANCE_ID='+instance,
        '-e', 'NSC_CONTROLLER__INTERNAL__LISTEN=127.0.0.1:'+str(internal_port),
        '-e', 'NSC_CONTROLLER__INTERNAL__URL=https://localhost:'+str(internal_port),
        '-e', 'NSC_CONTROLLER__INTERNAL__CA_FILE=/fixtures/ca.crt',
        '-e', 'NSC_CONTROLLER__INTERNAL__CERT_FILE=/fixtures/controller.crt',
        '-e', 'NSC_CONTROLLER__INTERNAL__KEY_FILE=/fixtures/controller.key']
containers.append(controller)
command('docker', 'run', '-d', '--name', controller, '--network', 'host', '--user', '0',
        '-v', str(work)+':/fixtures:ro', '-v', '/var/run/docker.sock:/var/run/docker.sock',
        '-e', 'NSC_STORAGE__DRIVER='+storage_driver, '-e', 'NSC_STORAGE__DSN='+storage_dsn,
        '-e', 'NSC_STORAGE__HA='+('true' if swarm else 'false'), '-e', 'NSC_SECURITY__ADMIN_TOKEN='+admin,
        '-e', 'NSC_SECURITY__SESSION_KEY=01234567890123456789012345678901',
        '-e', 'NSC_SECURITY__PRIMARY_KEY_ID=v1', '-e', 'NSC_SECURITY__ENCRYPTION_KEYS={"v1":"'+base64.b64encode(bytes(32)).decode()+'"}',
        '-e', 'NSC_SERVER__HTTP_LISTEN=127.0.0.1:'+str(api_port), '-e', 'NSC_SERVER__GRPC_LISTEN=',
        '-e', 'NSC_METRICS__LISTEN=127.0.0.1:0', '-e', 'NSC_MANAGEMENT__ENABLED=true',
        '-e', 'NSC_MANAGEMENT__DEPLOY_TIMEOUT=30s', *internal_options, image, 'serve', '--config=/fixtures/controller.yaml')

def request(method, path, body=None, revision=None, idem=None, headers=None):
    data = json.dumps(body).encode() if body is not None else None
    h = {'Authorization': 'Bearer '+admin, 'Content-Type': 'application/json'}
    if revision is not None: h['If-Match'] = '"rev-'+str(revision)+'"'
    if idem: h['Idempotency-Key'] = idem
    if headers: h.update(headers)
    try:
        with urllib.request.urlopen(urllib.request.Request(api+'/api/v1'+path, data=data, method=method, headers=h), timeout=60) as response:
            return json.load(response)['data']
    except urllib.error.HTTPError as error:
        raise RuntimeError(f'{method} {path}: HTTP {error.code}: {error.read().decode()}') from None

def wait(fn, label, timeout=100):
    end = time.monotonic()+timeout
    last = None
    while time.monotonic()<end:
        try:
            last=fn()
            if last:return last
        except (urllib.error.URLError, ConnectionError): pass
        time.sleep(1)
    raise AssertionError(f'Timed out: {label}; last={last}')

wait(lambda: urllib.request.urlopen(api+'/readyz',timeout=2).status==200, 'controller ready')
wait(lambda: urllib.request.urlopen(urllib.request.Request('http://'+repo.split('/')[0]+'/v2/',headers={'Authorization':'Basic '+base64.b64encode(b'ci:isolated-password').decode()} if private else {}),timeout=2).status==200, 'registry ready')

def push(version, tags, broken=False):
    context=work / ('image-'+version);context.mkdir(exist_ok=True)
    (context/'index.html').write_text(version)
    (context/'nginx.conf').write_text('pid /tmp/fixture.pid; error_log /dev/stderr; events {} http { access_log off; server { listen 8080; location / { root /fixture; } } }')
    entry='exit 42' if broken else 'nginx -c /fixture/nginx.conf -g \"daemon off;\"'
    (context/'Dockerfile').write_text('FROM '+base_image+'\nUSER root\nCOPY index.html nginx.conf /fixture/\nUSER nginx\nENTRYPOINT ["sh","-c",'+json.dumps(entry)+']\n')
    target=repo+':'+tags[0]
    command(*fixture_docker,'build','-t',target,str(context));images.append(target)
    for tag in tags:
        ref=repo+':'+tag
        if ref!=target:command(*fixture_docker,'tag',target,ref);images.append(ref)
        command(*fixture_docker,'push',ref)
    print('pushed',version,flush=True)

push('one',['dev','test','prod'])
network='nsc-delivery-validation-'+suffix
if swarm:command(*fixture_docker,'network','create','--driver','overlay','--attachable',network)
try:command('docker','network','inspect',network)
except RuntimeError:
    command('docker','network','create','--label','io.network-service-composer.e2e=true',network)
    networks.append(network)
pool=request('POST','/gateway-pools',{'name':'Delivery '+suffix,'backend_network':network,'challenge_upstream':'http://controller:8080'})
runtime=request('POST','/runtimes',{'pool_id':pool['id'],'name':'Isolated Docker '+suffix,'runtime_type':'swarm' if swarm else 'docker','endpoint':'tcp://127.0.0.1:'+daemon_port if swarm else 'unix:///var/run/docker.sock','management_enabled':True})
project=request('POST','/projects',{'name':'Delivery regression','slug':'delivery-'+suffix})
request('PUT','/projects/'+project['id']+'/variables',{'values':[{'key':'SHARED','value':'one','secret':False,'mask':False}]},0)
environments={}
for name in ['dev','test','prod']:
    env=request('POST','/projects/'+project['id']+'/environments',{'name':name,'runtime_id':runtime['id'],'registry_credential_ref':'isolated'})
    compose='services:\n  web:\n    image: '+repo+':'+name+'\n    expose: ["8080"]\n    environment:\n      SHARED: ${SHARED}\n    healthcheck:\n      test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/"]\n      interval: 1s\n      timeout: 1s\n      retries: 5\n'
    compose_file=work/(name+'.yaml');compose_file.write_text(compose);projects.append((env['project_name'],compose_file))
    review=request('POST','/environments/'+env['id']+'/preflight',{'compose_yaml':compose})
    operation=request('POST','/environments/'+env['id']+'/deployments',{'preflight_id':review['preflight_id']},env['revision'],'initial-'+name)
    finished=wait(lambda: (o if (o:=request('GET','/operations/'+operation['id']))['status'] not in ['QUEUED','RUNNING'] else None),name+' deployment')
    assert finished['status']=='SUCCEEDED',finished
    env=request('GET','/environments/'+env['id']);app=request('GET','/applications/'+env['application_id']);revision=request('GET','/applications/'+app['id']+'/revisions/'+app['active_revision_id'])
    assert '@sha256:' in revision['configuration_source']['images']['web'],revision
    environments[name]=(env,app['active_revision_id'])
    request('PUT','/environments/'+env['id']+'/update-policy',{'enabled':True,'poll':name=='test','webhook':name!='test','automatic':name!='prod','interval_seconds':60,'services':['web']},0)
print('three independent environments deployed',flush=True)
# Pending variable changes must never be picked up by image-only deployment.
request('PUT','/projects/'+project['id']+'/variables',{'values':[{'key':'SHARED','value':'unpublished','secret':False,'mask':False}]},1)
push('two',['dev','test','prod'])
for name in ['dev','prod']:
    env,_=environments[name]
    hook=request('POST','/environments/'+env['id']+'/webhook',{},0)
    body=b'{"services":["web"]}';timestamp=str(int(time.time()));event=uuid.uuid4().hex
    signature=hmac.new(hook['secret'].encode(),timestamp.encode()+b'.'+event.encode()+b'.'+body,hashlib.sha256).hexdigest()
    h={'Content-Type':'application/json','X-NSC-Timestamp':timestamp,'X-NSC-Event-ID':event,'X-NSC-Signature':signature}
    with urllib.request.urlopen(urllib.request.Request(api+'/api/v1/hooks/image-updates/'+hook['hook']['hook_id'],data=body,headers=h),timeout=10) as response:assert response.status==202

def updated(name):
    env,base=environments[name];app=request('GET','/applications/'+env['application_id'])
    return app if app['active_revision_id']!=base and app['status']=='RUNNING' else None
for name in ['dev','test']:
    app=wait(lambda:updated(name),name+' automatic update',150)
    revision=request('GET','/applications/'+app['id']+'/revisions/'+app['active_revision_id'])
    assert revision['configuration_source']['public_variables_version']==1,revision
    env,_=environments[name]
    containers_for_app=command(*fixture_docker,'ps','-q','--filter',('label=com.docker.stack.namespace=' if swarm else 'label=com.docker.compose.project=')+env['project_name']).split()
    assert len(containers_for_app)==1,containers_for_app
    actual_env=json.loads(command(*fixture_docker,'inspect',containers_for_app[0]))[0]['Config']['Env']
    assert 'SHARED=one' in actual_env and 'SHARED=unpublished' not in actual_env
    print(name,'automatically updated with frozen variables',flush=True)
env,base=environments['prod']
assert request('GET','/applications/'+env['application_id'])['active_revision_id']==base
candidate=wait(lambda: next((u for u in request('GET','/environments/'+env['id']+'/image-updates') if u['status']=='AVAILABLE'),None),'prod candidate')
request('POST','/environments/'+env['id']+'/image-updates/'+candidate['id'],{'action':'deploy'},candidate['revision'])
wait(lambda:updated('prod'),'prod approved update')
print('prod waited for explicit approval',flush=True)
env,_=environments['dev'];good=request('GET','/applications/'+env['application_id'])['active_revision_id']
push('broken',['dev'],True)
request('POST','/environments/'+env['id']+'/image-checks',{})
failed=wait(lambda: next((u for u in request('GET','/environments/'+env['id']+'/image-updates') if u['status'] in ['ROLLED_BACK','ROLLBACK_FAILED','FAILED']),None),'failed update rollback',150)
assert failed['status']=='ROLLED_BACK',failed
assert request('GET','/applications/'+env['application_id'])['active_revision_id']==good
request('POST','/environments/'+env['id']+'/image-checks',{})
time.sleep(7)
repeated=request('GET','/environments/'+env['id']+'/image-updates')
assert next(u for u in repeated if u['id']==failed['id'])['operation_id']==failed['operation_id']
print('failed image restored the exact previous digest and was not retried',flush=True)
print('Delivery '+('Swarm' if swarm else 'Compose')+' / Registry E2E passed',flush=True)
