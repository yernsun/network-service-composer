#!/usr/bin/env python3
"""NGINX release assertions used by the isolated Compose regression."""
import atexit,json,os,subprocess,time,urllib.request,urllib.error,datetime
completed=False
from pathlib import Path
api,pool,production,network,controller,work,binary,image=[os.environ['NSC_E2E_'+key] for key in ['API','POOL','GATEWAY','NETWORK','CONTROLLER','WORK','BINARY','IMAGE']]
root=Path(work);name=controller+'-sandbox';secrets=root/'sandbox-secrets';state=root/'sandbox-state';secrets.mkdir();state.mkdir();state.chmod(0o777)
def command(*args):
 result=subprocess.run(args,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 if result.returncode:raise RuntimeError('isolated NGINX fixture command failed; inspect its owned container diagnostics')
 return result.stdout.strip()
def cleanup():
 if not completed:subprocess.run(['docker','logs','--tail','80',name])
 subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
atexit.register(cleanup)
def request(method,path,body=None,revision=None,idem=None):
 headers={'Authorization':'Bearer e2e-admin-token','Content-Type':'application/json'}
 if revision is not None:headers['If-Match']='"rev-'+str(revision)+'"'
 if idem:headers['Idempotency-Key']=idem
 data=json.dumps(body).encode() if body is not None else None
 with urllib.request.urlopen(urllib.request.Request(api+'/api/v1'+path,method=method,data=data,headers=headers),timeout=30) as r:return json.load(r)['data']
def wait(fn,label,timeout=100):
 end=time.monotonic()+timeout
 while time.monotonic()<end:
  try:
   result=fn()
   if result:return result
  except (urllib.error.HTTPError,urllib.error.URLError):pass
  time.sleep(1)
 raise AssertionError('Timed out: '+label)
def header(container):
 port=json.loads(command('docker','inspect',container))[0]['NetworkSettings']['Ports']['80/tcp'][0]['HostPort']
 with urllib.request.urlopen(urllib.request.Request('http://'+os.environ.get('NSC_E2E_HOST','127.0.0.1')+':'+port+'/',headers={'Host':'compose.e2e.test'}),timeout=5) as r:return r.headers.get('X-Delivery')
started=time.time()
# The stored READY tuple may predate the controller restart in the parent test.
# Wait for a fresh heartbeat before creating a review bound to this connection.
production_env=dict(value.split('=',1) for value in json.loads(command('docker','inspect',production))[0]['Config']['Env'])
production_id=production_env['NSC_AGENT__GATEWAY_ID']
wait(lambda:datetime.datetime.fromisoformat(request('GET','/gateways/'+production_id)['last_seen_at'].replace('Z','+00:00')).timestamp()>=started,'fresh production heartbeat')
gateway=command(binary,'id','gateway')
command(binary,'pki','issue-agent','--ca-cert',str(root/'secrets/ca.crt'),'--ca-key',str(root/'secrets/ca.key'),'--cert',str(secrets/'agent.crt'),'--key',str(secrets/'agent.key'),'--pool-id',pool,'--gateway-id',gateway,'--valid-for','2h')
(secrets/'ca.crt').write_bytes((root/'secrets/ca.crt').read_bytes())
for f in secrets.iterdir():f.chmod(0o644)
route=next(r for r in request('GET','/routes?limit=200') if r['path']=='/')
base='/gateway-pools/'+pool+'/nginx'
request('PUT',base+'/sandbox',{'gateway_id':gateway,'checks':[{'route_id':route['id'],'path':'/','method':'GET','scheme':'http','expected_status':200}]},0)
# Copy only the private leader-discovery store settings, never arbitrary source
# environment or credentials into output. Dedicated sandbox state remains local.
store={key:production_env[key] for key in ('NSC_STORAGE__DRIVER','NSC_STORAGE__DSN','NSC_STORAGE__HA') if key in production_env}
if not store.get('NSC_STORAGE__DRIVER') or not store.get('NSC_STORAGE__DSN'):raise RuntimeError('production Gateway lacks explicit shared Controller leader store')
store['NSC_STORAGE__AUTO_MIGRATE']='false'
if any('\n' in value or '\r' in value for value in store.values()):raise RuntimeError('invalid leader-store environment value')
store_file=root/'sandbox-store.env';store_file.write_text(''.join(key+'='+value+'\n' for key,value in store.items()));store_file.chmod(0o600)
publish_host=os.environ.get('NSC_E2E_PUBLISH_HOST','127.0.0.1')
args=['docker','run','-d','--name',name,'--network',network,'--env-file',str(store_file),'-p',publish_host+'::80','-p',publish_host+'::443','--sysctl','net.ipv4.ip_unprivileged_port_start=0','-v',str(secrets)+':/run/nsc:ro','-v',str(state)+':/var/lib/nsc']
for key,value in {'MODE':'sandbox','GATEWAY_ID':gateway,'POOL_ID':pool,'NAME':'isolated-sandbox','CONTROLLER_ADDRESS':controller+':9443','SERVER_NAME':controller,'CA_CERT_FILE':'/run/nsc/ca.crt','TLS_CERT_FILE':'/run/nsc/agent.crt','TLS_KEY_FILE':'/run/nsc/agent.key','STATE_DIR':'/var/lib/nsc','HEARTBEAT_INTERVAL':'1s'}.items():args+=['-e','NSC_AGENT__'+key+'='+value]
args+=['-e','NSC_NGINX__WORKER_PROCESSES=1','-e','NSC_NGINX__GENERATION_DIR=/var/lib/nsc/generations',image,'--config='];command(*args)
def validate(draft):
 task=wait(lambda:request('POST',base+'/validations',{},draft['revision']),'sandbox online')
 def finished():
  v=request('GET',base+'/validations/'+task['id'])
  if v['status'] in ['FAILED','EXPIRED','CANCELLED']:raise AssertionError(v)
  return v if v['status']=='SUCCEEDED' else None
 return wait(finished,'validation result')
def publish(draft,task,tag):
 release=request('POST',base+'/releases',{'validation_id':task['id']},draft['revision'],tag)
 wait(lambda:request('GET',base+'/releases/'+release['id'])['status']=='SUCCEEDED','production release')
 return release
draft=request('PUT',base+'/draft',{'snippets':[{'scope':'http','content':'add_header X-Delivery one always;'}]},0)
task=validate(draft);assert task['results'][gateway]['checks']['fixture_passed'];assert header(name)=='one';assert header(production) is None
assert len(request('GET','/gateways?limit=200'))==1,'sandbox leaked into production inventory'
release=publish(draft,task,'first-release');assert header(production)=='one'
draft=request('GET',base+'/draft');draft=request('PUT',base+'/draft',{'snippets':[{'scope':'http','content':'add_header X-Delivery two always;'}]},draft['revision']);task=validate(draft);publish(draft,task,'second-release');assert header(production)=='two'
draft=request('GET',base+'/draft');draft=request('POST',base+'/versions/'+release['version_id']+'/restore',{},draft['revision']);task=validate(draft);publish(draft,task,'restore-release');assert header(production)=='one'
# Restarting the sandbox invalidates an otherwise successful review.
draft=request('GET',base+'/draft');task=validate(draft);command('docker','restart',name);wait(lambda:request('GET',base+'/validations/'+task['id'])['status']=='EXPIRED','restart invalidation')
assert header(production)=='one'
completed=True
print('nginx_e2e sandbox_fixture=ok dedicated_port=ok production_preflight=ok publish=ok history_restore=ok restart_invalidation=ok inventory_isolation=ok',flush=True)
