#!/usr/bin/env python3
"""Exercise current Controller binaries on three disposable nested Docker Engines.

The shared database has no replication or promotion in this fixture. Every Engine,
Controller, socket volume and network is owned by this run. Host workloads and
production resources are never addressed. Read the resulting limitations before
using this as physical failure-domain or end-to-end HA evidence.
"""
from pathlib import Path
import argparse,base64,hashlib,ipaddress,json,os,secrets,shutil,subprocess,tempfile,time,urllib.request,urllib.error
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--controller-image',required=True,help='Locally built current-source Controller image containing Docker Compose')
parser.add_argument('--output',required=True,type=Path)
options=parser.parse_args()
root=Path(__file__).resolve().parents[1]
output=options.output.resolve();output.parent.mkdir(parents=True,exist_ok=True)
images=json.loads((root/'build/image-lock.json').read_text())['images']
prefix='nsc-node-ha-'+secrets.token_hex(5)
work=Path(tempfile.mkdtemp(prefix=prefix+'-'));work.chmod(0o700)
docker=['docker','--host','unix:///var/run/docker.sock']
owned=[];volumes=[];net=None
password=secrets.token_hex(24);token=secrets.token_hex(24)
report={'status':'FAIL','checks':[],'production_access':False,'controller_budget_seconds':120,'cleanup':'PENDING','image':options.controller_image,'traffic_samples':[],'limitations':['Single fixture host; physical failure domains are not accepted.','Shared PostgreSQL; replication and promotion are excluded.','External ingress and Gateway LKG are verified by their separate fixture.']}
paused=[]
def d(*args,check=True,input=None,timeout=120):
 p=subprocess.run(docker+list(args),input=input,capture_output=True,timeout=timeout)
 if check and p.returncode:
  error_file=output.parent/(prefix+'-command-private.log');error_file.write_bytes(p.stderr);error_file.chmod(0o600)
  raise RuntimeError('docker command failed: '+args[0])
 return p.stdout.decode().strip()
def container(name,*args):
 owned.append(name);return d('run','-d','--pull=never','--name',name,'--label','nsc.fixture='+prefix,'--network',prefix,*args)
def wait(fn,timeout=120):
 end=time.monotonic()+timeout
 while time.monotonic()<end:
  try:
   v=fn()
   if v:return v
  except (RuntimeError,OSError,urllib.error.URLError,ValueError,KeyError,IndexError):pass
  time.sleep(.4)
 raise RuntimeError('fixture deadline exceeded')
def sql(query):return d('exec','-i',prefix+'-db','psql','-U','nsc','-d','nsc','-At','-v','ON_ERROR_STOP=1',input=query.encode())
def inner(node,*args):return d('exec',node,'docker',*args,timeout=35)
def api(base,path,body=None,method=None):
 req=urllib.request.Request(base+'/api/v1'+path,data=None if body is None else json.dumps(body).encode(),method=method,headers={'Authorization':'Bearer '+token,'Content-Type':'application/json','Idempotency-Key':prefix+'-'+secrets.token_hex(5)})
 try:
  with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req,timeout=8) as resp:raw=resp.read()
 except urllib.error.HTTPError as e:
  raw=e.read()
  try:code=json.loads(raw).get('code','unknown')
  except ValueError:code='unknown'
  raise RuntimeError('api rejected '+path+' '+str(e.code)+' '+code) from None
 return json.loads(raw)['data'] if raw else None
controllers=[];engines=[];bindings=[]
def endpoint(name):
 p=json.loads(d('inspect',name))[0]['NetworkSettings']['Ports']['8080/tcp'][0]['HostPort'];return 'http://127.0.0.1:'+p
def leader():return json.loads(sql("SELECT payload FROM control_documents WHERE id='controller:leader'"))
def current_api():
 l=leader()['instance_id'];return endpoint(l)
def operation(op,success=True):
 v=api(current_api(),'/operations/'+op)
 report['last_operation']={k:v.get(k) for k in ['id','type','status','attempt','error_code']}
 if v['status']=='RUNNING':
  evidence=api(current_api(),'/operations/'+op+'/readiness').get('execution',{})
  report['last_execution']={k:evidence.get(k) for k in ['state','reason','actual_node_id','configured_node_id']}
 if v['status']=='FAILED':raise RuntimeError('task failed '+v.get('error_code','unknown'))
 return v if (v['status']=='SUCCEEDED' if success else v['status']=='RUNNING') else None
def create_config(runtime,label):
 name=prefix+'-'+label
 op=api(current_api(),'/runtimes/'+runtime+'/docker-resources',{'kind':'config','name':name,'content_base64':base64.b64encode(b'isolated immutable configuration').decode()})
 wait(lambda:operation(op['id']))
 evidence=api(current_api(),'/operations/'+op['id']+'/readiness')['execution']
 return name,evidence
try:
 report['image_id']=d('image','inspect','--format','{{.Id}}',options.controller_image)
 report['controller_binary_sha256']=d('run','--rm','--pull=never','--network','none','--entrypoint','sha256sum',options.controller_image,'/usr/local/bin/nsc-controller').split()[0]
 net=d('network','create','--label','nsc.fixture='+prefix,prefix)
 subnet=ipaddress.ip_network(json.loads(d('network','inspect',net))[0]['IPAM']['Config'][0]['Subnet'])
 if subnet.num_addresses<16:raise RuntimeError('fixture requires at least a /28 Docker network')
 # Static endpoint addresses require an explicitly configured Docker subnet.
 assert d('network','inspect','--format','{{index .Labels \"nsc.fixture\"}}',net)==prefix
 d('network','rm',net)
 net=d('network','create','--subnet',str(subnet),'--label','nsc.fixture='+prefix,prefix)
 manager_addresses=[str(subnet.network_address+10+i) for i in range(3)]
 report['manager_addresses_fixed']=True
 env=work/'db.env';env.write_text('POSTGRES_USER=nsc\nPOSTGRES_DB=nsc\nPOSTGRES_PASSWORD='+password+'\n');env.chmod(0o600)
 container(prefix+'-db','--network-alias','database','--tmpfs','/var/lib/postgresql:rw,nosuid,nodev,size=1g','--env-file',str(env),images['postgres'])
 wait(lambda:sql('SELECT 1')=='1')
 for i in range(3):
  name=prefix+'-engine-'+str(i);engines.append(name);volume=prefix+'-socket-'+str(i);d('volume','create','--label','nsc.fixture='+prefix,volume);volumes.append(volume)
  container(name,'--ip',manager_addresses[i],'--privileged','--memory','1200m','--cpus','2','--mount','type=volume,source='+volume+',target=/var/run','--env','DOCKER_TLS_CERTDIR=',images['docker_dind'],'--tls=false')
  wait(lambda name=name:inner(name,'info','--format','{{.ID}}'))
 addresses=[json.loads(d('inspect',name))[0]['NetworkSettings']['Networks'][prefix]['IPAddress'] for name in engines]
 inner(engines[0],'swarm','init','--advertise-addr',addresses[0]);join=inner(engines[0],'swarm','join-token','-q','manager')
 for name,ip in zip(engines[1:],addresses[1:]):inner(name,'swarm','join','--advertise-addr',ip,'--token',join,addresses[0]+':2377')
 def nodes():return json.loads(inner(engines[0],'node','inspect',*inner(engines[0],'node','ls','-q').split()))
 wait(lambda:len(nodes())==3 and all(n['ManagerStatus']['Reachability']=='reachable' for n in nodes()))
 archive=work/'image.tar';d('save','-o',str(archive),images['nginx'])
 image_id=d('image','inspect','--format','{{.Id}}',images['nginx'])
 for engine in engines:
  with archive.open('rb') as stream:
   p=subprocess.run(docker+['exec','-i',engine,'docker','load'],stdin=stream,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=120)
   if p.returncode:raise RuntimeError('isolated image load failed')
  inner(engine,'tag',image_id,images['nginx'].split('@')[0])
  assert inner(engine,'image','inspect','--format','{{.Id}}',images['nginx'].split('@')[0])==image_id
 report['fixture_workload_image_id']=image_id
 for i,name in enumerate(engines):
  info=json.loads(inner(name,'info','--format','{{json .}}'))
  bindings.append({'instance_id':prefix+'-controller-'+str(i),'node_id':info['Swarm']['NodeID'],'engine_id':info['ID'],'cluster_id':info['Swarm']['Cluster']['ID'],'url':'https://'+prefix+'-controller-'+str(i)+':9444'})
 pki=work/'pki';pki.mkdir()
 def issue(*args):d('run','--rm','--pull=never','--network','none','--volume',str(pki)+':/pki',options.controller_image,'pki',*args)
 issue('init-ca','--cert','/pki/ca.crt','--key','/pki/ca.key','--common-name','isolated fixture','--valid-for','2h')
 key=base64.b64encode(os.urandom(32)).decode()
 for i,b in enumerate(bindings):
  name=b['instance_id'];controllers.append(name)
  issue('issue-controller','--instance-id',name,'--ca-cert','/pki/ca.crt','--ca-key','/pki/ca.key','--cert','/pki/'+name+'.crt','--key','/pki/'+name+'.key','--common-name',name,'--dns',name,'--valid-for','2h')
  cfg={'server':{'http_listen':':8080','grpc_listen':'','public_url':'http://'+name+':8080'},'storage':{'driver':'postgres','dsn':'postgres://nsc:'+password+'@database:5432/nsc?sslmode=disable','ha':True,'auto_migrate':True},'security':{'admin_token':token,'session_key':token,'primary_key_id':'fixture','encryption_keys':{'fixture':key}},'management':{'enabled':True,'poll_interval':'200ms','heartbeat_interval':'1s','stale_after':'6s','deploy_timeout':'2m','work_dir':'/tmp/nsc-work'},'controller':{'instance_id':name,'nodes':bindings,'discovery':{'reconcile':{'interval':'1s'},'full_reconcile':{'interval':'2s'}},'internal':{'listen':':9444','url':b['url'],'ca_file':'/run/nsc/ca.crt','cert_file':'/run/nsc/'+name+'.crt','key_file':'/run/nsc/'+name+'.key'}}}
  path=work/(name+'.json');path.write_text(json.dumps(cfg));path.chmod(0o600)
  container(name,'--memory','768m','--cpus','2','--publish','127.0.0.1::8080','--mount','type=volume,source='+volumes[i]+',target=/fixture-socket','--volume',str(pki)+':/run/nsc:ro','--volume',str(path)+':/etc/nsc/config.json:ro','--entrypoint','sh',options.controller_image,'-ec','ln -sf /fixture-socket/docker.sock /var/run/docker.sock; exec /usr/local/bin/nsc-controller serve --config /etc/nsc/config.json')
  wait(lambda name=name:api(endpoint(name),'/gateway-pools') is not None)
 pool=api(current_api(),'/gateway-pools',{'name':'Isolated execution','backend_network':prefix,'challenge_upstream':'http://controller:8080'})['id']
 for engine in engines:inner(engine,'network','create',prefix)
 swarm=api(current_api(),'/runtimes',{'name':'Swarm fixture','pool_id':pool,'runtime_type':'swarm','endpoint':'node://'+bindings[0]['node_id'],'management_enabled':True})['id']
 compose=api(current_api(),'/runtimes',{'name':'Compose fixture','pool_id':pool,'runtime_type':'docker','endpoint':'node://'+bindings[0]['node_id'],'management_enabled':True})['id']
 wait(lambda:api(current_api(),'/runtimes/'+swarm).get('cluster_id')==bindings[0]['cluster_id'])
 name,original=create_config(swarm,'original');assert original['actual_node_id']==bindings[0]['node_id']
 first_config=inner(engines[1],'config','ls','--filter','name='+name,'-q');assert first_config
 report['checks'].append('original_manager_config_created_with_current_binary')
 app=api(current_api(),'/applications',{'runtime_id':compose,'name':'Pinned Compose','project_name':prefix+'-app','compose_yaml':'services:\n  web:\n    image: '+images['nginx'].split('@')[0]+'\n    pull_policy: never\n'})
 wait(lambda:operation(app['operation']['id']))
 container_id=inner(engines[0],'ps','-q','--filter','label=com.docker.compose.project='+prefix+'-app');assert container_id
 report['checks'].append('compose_application_executed_on_original_engine')

 def ha():return api(current_api(),'/system/ha')
 def layer(name):return next(v for v in ha()['layers'] if v['name']==name)
 wait(lambda:len(layer('controller')['evidence'])==3 and all(e['state']=='READY' for e in layer('controller')['evidence']))
 assert ha()['end_to_end_verified'] is False and layer('storage')['state']=='LIMITED' and layer('ingress')['state']=='UNKNOWN'
 report['checks'].append('live_leader_scheduler_and_authenticated_follower_readiness')
 # A real health-gated Compose command stays in Docker while its Controller is
 # paused. Terminate only this fixture executor's SQL sessions, then let a new
 # term observe uncertainty before the old process is resumed.
 slow=api(current_api(),'/applications',{'runtime_id':compose,'name':'Late executor fixture','project_name':prefix+'-late','compose_yaml':'services:\n  web:\n    image: '+images['nginx'].split('@')[0]+'\n    pull_policy: never\n    healthcheck:\n      test: ["CMD-SHELL", "test -f /tmp/fixture-ready"]\n      interval: 1s\n      timeout: 1s\n      start_period: 120s\n      retries: 120\n'})
 late_id=wait(lambda:inner(engines[0],'ps','-q','--filter','label=com.docker.compose.project='+prefix+'-late'))
 old=wait(lambda:operation(slow['operation']['id'],False));old_attempt=old['attempt'];old_leader=leader()['instance_id']
 executor=bindings[0]['instance_id'];d('pause',executor);paused.append(executor)
 executor_ip=ipaddress.ip_address(json.loads(d('inspect',executor))[0]['NetworkSettings']['Networks'][prefix]['IPAddress'])
 sql("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=current_database() AND usename=current_user AND client_addr='"+str(executor_ip)+"'::inet")
 if old_leader!=executor:d('kill',old_leader)
 started=time.monotonic();wait(lambda:leader()['instance_id']!=old_leader and api(current_api(),'/gateway-pools') is not None)
 report['paused_executor_takeover_seconds']=round(time.monotonic()-started,3);assert report['paused_executor_takeover_seconds']<=120
 wait(lambda:(lambda op:op['status']=='RUNNING' and op['attempt']>old_attempt)(api(current_api(),'/operations/'+slow['operation']['id'])))
 inner(engines[0],'exec',late_id,'touch','/tmp/fixture-ready');d('unpause',executor);paused.remove(executor)
 wait(lambda:(lambda e:e.get('state')=='UNKNOWN' and e.get('reconciled_at'))(api(current_api(),'/operations/'+slow['operation']['id']+'/readiness').get('execution',{})))
 late_operation=api(current_api(),'/operations/'+slow['operation']['id']);late_app=api(current_api(),'/applications/'+slow['application']['id'])
 assert late_operation['status']=='RUNNING' and not late_app.get('active_revision_id')
 assert inner(engines[0],'ps','-aq','--filter','label=com.docker.compose.project='+prefix+'-late')==late_id
 report['checks'].append('late_executor_cannot_activate_or_replay_accepted_uncertain_work')
 for controller in controllers:d('start',controller)
 wait(lambda:all(e['state']=='READY' for e in layer('controller')['evidence']))

 former=leader()['instance_id'];started=time.monotonic();d('kill',former)
 wait(lambda:leader()['instance_id']!=former and api(current_api(),'/gateway-pools') is not None)
 report['controller_takeover_seconds']=round(time.monotonic()-started,3);assert report['controller_takeover_seconds']<=120
 report['checks'].append('controller_crash_takeover_within_budget')
 original_controller=bindings[0]['instance_id']
 if original_controller!=former:d('kill',original_controller)
 wait(lambda:leader()['instance_id'] not in [former,original_controller])
 name,alternate=create_config(swarm,'alternate');assert alternate['actual_node_id']!=bindings[0]['node_id'];assert alternate['cluster_id']==bindings[0]['cluster_id']
 report['checks'].append('same_cluster_alternate_manager_executed_frozen_task')
 d('stop','--time','2',engines[0])
 name,_=create_config(swarm,'manager-loss');assert inner(engines[1],'config','inspect',first_config)
 report['checks'].append('one_manager_loss_preserved_quorum_and_resource_identity')
 queued=api(current_api(),'/applications/'+app['application']['id']+'/operations',{'action':'STOP'})
 wait(lambda:operation(queued['id'],False))
 evidence=wait(lambda:(lambda v:v if v and v.get('state')=='WAITING' else None)(api(current_api(),'/operations/'+queued['id']+'/readiness').get('execution')))
 assert evidence['configured_node_id']==bindings[0]['node_id'] and not evidence.get('actual_node_id')
 assert all(not inner(engine,'ps','-aq','--filter','label=com.docker.compose.project='+prefix+'-app') for engine in engines[1:])
 report['checks'].append('compose_host_outage_retained_task_without_migration')
 d('start',engines[0]);d('start',original_controller)
 wait(lambda:operation(queued['id']))
 assert inner(engines[0],'ps','-aq','--filter','label=com.docker.compose.project='+prefix+'-app')==container_id
 report['checks'].append('original_engine_recovery_completed_stop_preserving_container_identity')

 for controller in controllers:d('start',controller)
 wait(lambda:all(e['state']=='READY' for e in layer('controller')['evidence']))
 service=inner(engines[2],'service','create','--detach','--no-resolve-image','--constraint','node.id=='+bindings[2]['node_id'],'--name',prefix+'-survivor',images['nginx'].split('@')[0])
 survivor=wait(lambda:inner(engines[2],'ps','-q','--filter','label=com.docker.swarm.service.name='+prefix+'-survivor'))
 wait(lambda:'Welcome to nginx' in inner(engines[2],'exec',survivor,'wget','-qO-','http://127.0.0.1'))
 d('stop','--time','2',engines[0]);d('stop','--time','2',engines[1])
 wait(lambda:next(e for e in layer('execution')['evidence'] if e['id']==swarm)['state']!='READY')
 blocked_name=prefix+'-quorum-waiting'
 blocked=api(current_api(),'/runtimes/'+swarm+'/docker-resources',{'kind':'config','name':blocked_name,'content_base64':base64.b64encode(b'fixture quorum intent').decode()})
 wait(lambda:(lambda e:e.get('state')=='WAITING')(api(current_api(),'/operations/'+blocked['id']+'/readiness').get('execution',{})))
 assert api(current_api(),'/operations/'+blocked['id'])['status']=='RUNNING'
 for sample in range(10):
  value={'sample':sample,'at':time.time(),'ok':False}
  try:value['ok']='Welcome to nginx' in inner(engines[2],'exec',survivor,'wget','-qO-','http://127.0.0.1')
  except BaseException as error:value['error']=type(error).__name__
  report['traffic_samples'].append(value)
 assert all(v['ok'] for v in report['traffic_samples'])
 assert inner(engines[2],'ps','-q','--filter','label=com.docker.swarm.service.name='+prefix+'-survivor')==survivor
 report['checks'].append('quorum_loss_blocks_nsc_management_and_preserves_listener_and_pending_intent')
 d('start',engines[1]);assert json.loads(d('inspect',engines[1]))[0]['NetworkSettings']['Networks'][prefix]['IPAddress']==manager_addresses[1];wait(lambda:operation(blocked['id']))
 assert inner(engines[2],'config','inspect',first_config)
 assert json.loads(inner(engines[2],'service','inspect',service))[0]['ID']==service
 assert inner(engines[2],'ps','-q','--filter','label=com.docker.swarm.service.name='+prefix+'-survivor')==survivor
 report['checks'].append('quorum_recovery_resumes_original_intent_with_resource_and_workload_identity_preserved')

 report['status']='PASS'
except BaseException as e:
 report['failure']=str(e) if isinstance(e,RuntimeError) else type(e).__name__
 raise
finally:
 for name in paused:d('unpause',name,check=False)
 for name in controllers:
  if name in owned:
   logs=d('logs','--tail','200',name,check=False)
   trace=d('exec',name,'cat','/tmp/nsc-fixture-cli-errors',check=False)
   (output.parent/(name+'-cli-private.log')).write_text(trace)
   # Controller logs retain only codes/IDs. Keep all fixture output private.
   (output.parent/(name+'-diagnostic.log')).write_text(logs)
 clean=True
 for name in reversed(owned):
  label=d('inspect','--format','{{index .Config.Labels "nsc.fixture"}}',name,check=False)
  if label==prefix:
   p=subprocess.run(docker+['rm','--force','--volumes',name],capture_output=True)
   clean=clean and p.returncode==0
  else:clean=False
 for volume in volumes:
  if d('volume','inspect','--format','{{index .Labels \"nsc.fixture\"}}',volume,check=False)!=prefix:clean=False;continue
  p=subprocess.run(docker+['volume','rm',volume],capture_output=True);clean=clean and p.returncode==0
 if net and d('network','inspect','--format','{{index .Labels \"nsc.fixture\"}}',net,check=False)==prefix:
  p=subprocess.run(docker+['network','rm',net],capture_output=True);clean=clean and p.returncode==0
 report['cleanup']='PASS' if clean else 'FAIL'
 if not clean:report['status']='FAIL'
 output.write_text(json.dumps(report,indent=2)+'\n');output.chmod(0o600)
 shutil.rmtree(work)
 print(json.dumps({'status':report['status'],'checks':report['checks'],'cleanup':report['cleanup'],'failure':report.get('failure')}))

if report["status"]!="PASS" or report["cleanup"]!="PASS":raise SystemExit(1)
