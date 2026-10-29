"""Run official marketplace acceptance in disposable Docker-in-Docker nodes."""
import atexit,base64,json,os,pathlib,subprocess,time,uuid,urllib.request,urllib.error
from postgres_fixture import wait_for_postgres, start_migrated_controllers
prefix='nsc-market-swarm-'+uuid.uuid4().hex[:8]
work=pathlib.Path('/tmp/'+prefix);work.mkdir();os.chmod(work,0o700)
created_nodes=[];created_network=None
network=prefix+'-bridge';nodes=[prefix+'-manager',prefix+'-worker'];secret='fixture-'+uuid.uuid4().hex
state={'prefix':prefix,'work':str(work),'nodes':nodes,'network':network,'secret':secret}
def cmd(*args):
 p=subprocess.run(args,capture_output=True,text=True)
 if p.returncode:raise RuntimeError(' '.join(args[:3])+': '+p.stderr[-1000:])
 return p.stdout.strip()
def inner(n,*args):return cmd('docker','exec',n,'docker',*args)
def cleanup():
 for n in created_nodes:subprocess.run(['docker','rm','-fv',n],capture_output=True)
 if created_network:subprocess.run(['docker','network','rm',created_network],capture_output=True)
atexit.register(cleanup)
created_network=cmd('docker','network','create',network)
for n in nodes:
 created_nodes.append(cmd('docker','run','-d','--privileged','--name',n,'--hostname',n,'--network',network,'-e','DOCKER_TLS_CERTDIR=','-p','127.0.0.1::8080','-p','127.0.0.1::2375','docker:28-dind','--tls=false'))
 for i in range(60):
  try:inner(n,'info');break
  except RuntimeError:time.sleep(1)
 else:raise RuntimeError('nested docker not ready')
ips=[json.loads(cmd('docker','inspect',n))[0]['NetworkSettings']['Networks'][network]['IPAddress'] for n in nodes]
inner(nodes[0],'swarm','init','--advertise-addr',ips[0]);token=inner(nodes[0],'swarm','join-token','-q','worker');inner(nodes[1],'swarm','join','--token',token,ips[0]+':2377')
# Supply repository-built images; the isolated daemons never mount a host socket.
version=pathlib.Path('VERSION').read_text().strip()
image=os.environ.get('NSC_CONTROLLER_IMAGE','nsc-controller:v'+version)
gateway_image=os.environ.get('NSC_GATEWAY_IMAGE','nsc-gateway:v'+version)
binary=str(pathlib.Path(os.environ.get('NSC_CONTROLLER_BINARY','bin/nsc-controller')).resolve())
state.update({'binary':binary,'gateway_image':gateway_image})
volume_only=os.environ.get('NSC_VOLUME_ACCEPTANCE')=='true'
retention_only=os.environ.get('NSC_RETENTION_ACCEPTANCE')=='true'
if volume_only and retention_only:raise RuntimeError('Select only one focused acceptance profile')
# Archive loading drops RepoDigests and digest-qualified references' tags.
# Resolve the locked fixture database content before saving and run that exact
# loaded config ID; nested daemons must never pull an unpinned database tag.
postgres_image='postgres:18.6-alpine3.24@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2'
postgres_image_id=cmd('docker','image','inspect','--format','{{.Id}}',postgres_image)
market=json.loads(pathlib.Path('internal/core/builtin/market.yaml').read_text());images={image,postgres_image}
import re
if volume_only or retention_only:images.add('alpine:3.24')
if retention_only:images.add('registry:2')
for app in ([] if volume_only or retention_only else market['apps']):
 v=app['versions'][-1];path=v['files'].get('swarm',v['files']['compose']); path=path if isinstance(path,str) else path['path']
 images.update(re.findall(r'image:\s*(\S+)',(pathlib.Path('internal/core/builtin')/path).read_text()))
archive=work/'images.tar';cmd('docker','save','-o',str(archive),*images)
for n in nodes:

 with archive.open('rb') as data:
  subprocess.run(['docker','exec','-i',n,'docker','load'],stdin=data,stdout=subprocess.DEVNULL,check=True)
 if inner(n,'image','inspect','--format','{{.Id}}',postgres_image_id)!=postgres_image_id:raise RuntimeError('Locked fixture database content was not loaded')
# Certificates and node identity are provisioned independently of managed apps.
pki=work/'pki';pki.mkdir();cmd(binary,'pki','init-ca','--cert',str(pki/'ca.crt'),'--key',str(pki/'ca.key'),'--common-name',prefix,'--valid-for','2h')
bindings=[]
for n,ip in zip(nodes,ips):
 info=json.loads(inner(n,'info','--format','{{json .}}')); bindings.append({'instance_id':n,'node_id':info['Swarm']['NodeID'],'engine_id':info['ID'],'cluster_id':info['Swarm'].get('Cluster',{}).get('ID') or json.loads(inner(nodes[0],'info','--format','{{json .Swarm.Cluster}}'))['ID'],'url':'https://'+ip+':9444'})
 cmd(binary,'pki','issue-controller','--instance-id',n,'--ca-cert',str(pki/'ca.crt'),'--ca-key',str(pki/'ca.key'),'--cert',str(pki/(n+'.crt')),'--key',str(pki/(n+'.key')),'--common-name',n,'--ip',ip,'--dns',n,'--valid-for','2h')
inner(nodes[0],'run','-d','--name','fixture-db','-p','5439:5432','-e','POSTGRES_DB=nsc','-e','POSTGRES_USER=nsc','-e','POSTGRES_PASSWORD='+secret,postgres_image_id)
wait_for_postgres(inner,nodes[0])
for n,ip in zip(nodes,ips):
 config={'management':{'enabled':True,'deploy_timeout':'8m'},'storage':{'driver':'postgres','dsn':'postgres://nsc:'+secret+'@'+ips[0]+':5439/nsc?sslmode=disable','ha':True,'auto_migrate':False},'security':{'admin_token':secret,'session_key':secret+'session','primary_key_id':'v1','encryption_keys':{'v1':base64.b64encode(b'01234567890123456789012345678901').decode()}},'server':{'http_listen':':8080','grpc_listen':':9443','grpc_tls_cert_file':'/run/nsc/'+n+'.crt','grpc_tls_key_file':'/run/nsc/'+n+'.key','grpc_client_ca_file':'/run/nsc/ca.crt'},'controller':{'instance_id':n,'local_gateway':False,'nodes':bindings,'internal':{'listen':':9444','url':'https://'+ip+':9444','grpc_address':ip+':9443','ca_file':'/run/nsc/ca.crt','cert_file':'/run/nsc/'+n+'.crt','key_file':'/run/nsc/'+n+'.key'}}}
 (work/(n+'.json')).write_text(json.dumps(config));os.chmod(work/(n+'.json'),0o600);cmd('docker','exec',n,'mkdir','-p','/fixture');cmd('docker','cp',str(pki),n+':/fixture/pki');cmd('docker','cp',str(work/(n+'.json')),n+':/fixture/controller.json');cmd('docker','exec',n,'chmod','-R','go-rwx','/fixture')
start_migrated_controllers(inner,nodes,image)
# Both HA controllers must have started before marketplace behavior is tested.
# A failed collector controller is a fixture startup failure, not storage proof.
health_opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
for n in nodes:
 health_port=json.loads(cmd('docker','inspect',n))[0]['NetworkSettings']['Ports']['8080/tcp'][0]['HostPort']
 for attempt in range(60):
  if inner(n,'inspect','--format','{{.State.Status}}','fixture-controller')=='exited':raise RuntimeError('fixture controller exited during startup: '+n)
  try:
   with health_opener.open('http://127.0.0.1:'+health_port+'/healthz',timeout=2) as response:
    if response.status==200:break
  except (OSError,urllib.error.URLError):pass
  time.sleep(1)
 else:raise RuntimeError('fixture controller startup timed out: '+n)
port=json.loads(cmd('docker','inspect',nodes[0]))[0]['NetworkSettings']['Ports']['8080/tcp'][0]['HostPort'];state.update({'base':'http://127.0.0.1:'+port,'ips':ips,'bindings':bindings,'image':image});(work/'state.json').write_text(json.dumps(state));os.chmod(work/'state.json',0o600)
print(str(work/'state.json'),flush=True)
helpers=pathlib.Path(__file__).resolve().parent/'market-swarm'
for name in (['retention.py'] if retention_only else ['volumes.py'] if volume_only else ['acceptance.py','wordpress_https.py','compose_regression.py','failures.py','updates.py','volumes.py']):
 subprocess.run(['python3','-B',str(helpers/name),str(work/'state.json')],check=True)
 if name=='acceptance.py' and os.environ.get('NSC_MARKET_BROWSER')=='true':subprocess.run(['node',str(helpers/'browser.mjs'),str(work/'state.json')],check=True)
print(json.dumps({'isolated_remote_retention' if retention_only else 'isolated_volume_api' if volume_only else 'isolated_multi_node_swarm': 'passed'}),flush=True)
