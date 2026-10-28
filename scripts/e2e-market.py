#!/usr/bin/env python3
"""Opt-in real Compose installation smoke test; touches only uniquely named fixtures."""
import atexit, base64, json, os, pathlib, subprocess, tempfile, time, urllib.request, uuid
suffix=uuid.uuid4().hex[:8];prefix='nsc-market-'+suffix
work=pathlib.Path(tempfile.mkdtemp(prefix=prefix));os.chmod(work,0o755)
controller=prefix+'-controller';network=prefix+'-ingress';projects=[]
image=os.environ.get('NSC_MARKET_CONTROLLER_IMAGE','nsc-controller:v0.1.0')
def command(*args):
 p=subprocess.run(args,capture_output=True,text=True)
 if p.returncode:raise RuntimeError(' '.join(args[:3])+': '+p.stderr[-1200:])
 return p.stdout.strip()
def cleanup():
 subprocess.run(['docker','rm','-f',controller],capture_output=True)
 for project in reversed(projects):
  ids=command('docker','ps','-aq','--filter','label=com.docker.compose.project='+project).split()
  if ids:subprocess.run(['docker','rm','-f',*ids],capture_output=True)
  for kind in ['network','volume']:
   ids=command('docker',kind,'ls','-q','--filter','label=com.docker.compose.project='+project).split()
   if ids:subprocess.run(['docker',kind,'rm',*ids],capture_output=True)
 subprocess.run(['docker','network','rm',network],capture_output=True)
atexit.register(cleanup)
command('docker','network','create',network)
secret='market-test-password-'+suffix
config=work/'controller.yaml';config.write_text('management:\n  enabled: true\n  deploy_timeout: 8m\n')
command('docker','run','-d','--name',controller,'--network',network,'--group-add',str(os.stat('/var/run/docker.sock').st_gid),'-p','127.0.0.1::8080','-v','/var/run/docker.sock:/var/run/docker.sock','-v',str(config)+':/etc/nsc/controller.yaml:ro',
 '-e','NSC_STORAGE__DRIVER=sqlite','-e','NSC_STORAGE__DSN=file:/tmp/market.db?_pragma=foreign_keys(1)&_time_format=sqlite','-e','NSC_STORAGE__HA=false','-e','NSC_SECURITY__ADMIN_TOKEN='+secret,'-e','NSC_SECURITY__SESSION_KEY='+secret+'-session','-e','NSC_SECURITY__PRIMARY_KEY_ID=v1','-e','NSC_SECURITY__ENCRYPTION_KEYS='+json.dumps({'v1':base64.b64encode(b'01234567890123456789012345678901').decode()}),'-e','NSC_SERVER__GRPC_LISTEN=','-e','NSC_METRICS__LISTEN=127.0.0.1:0',image)
port=json.loads(command('docker','inspect',controller))[0]['NetworkSettings']['Ports']['8080/tcp'][0]['HostPort'];base='http://127.0.0.1:'+port
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def api(method,path,body=None):
 data=None if body is None else json.dumps(body).encode()
 req=urllib.request.Request(base+path,data=data,method=method,headers={'Authorization':'Bearer '+secret,'Content-Type':'application/json','Idempotency-Key':str(uuid.uuid4())})
 try:
  with opener.open(req,timeout=90) as r:return json.load(r).get('data')
 except urllib.error.HTTPError as e:raise RuntimeError(path+': '+e.read().decode()[:1200])
for _ in range(60):
 try:api('GET','/api/v1/settings');break
 except Exception:time.sleep(1)
else:raise RuntimeError('isolated Controller not ready')
pool=api('POST','/api/v1/gateway-pools',{'name':prefix,'backend_network':network,'challenge_upstream':'http://'+controller+':8080'})
rt=api('POST','/api/v1/runtimes',{'name':prefix,'pool_id':pool['id'],'runtime_type':'docker','endpoint':'unix:///var/run/docker.sock','management_enabled':True})
versions={v['app_key']:v for v in api('GET','/api/v1/catalog/apps?limit=100')}
results=[]
installed={}
for key in ['redis','mysql','postgresql','wordpress','pgadmin','phpmyadmin']:
 project=prefix+'-'+key;projects.append(project)
 values={'REDIS_PASSWORD':secret,'MYSQL_ROOT_PASSWORD':secret+'root','MYSQL_PASSWORD':secret,'POSTGRES_PASSWORD':secret,'PGADMIN_DEFAULT_EMAIL':'admin@example.com','PGADMIN_DEFAULT_PASSWORD':secret,'PMA_HOST':'mysql','DATABASE_NETWORK':prefix+('-postgresql_default' if key=='pgadmin' else '-mysql_default')}
 manifest=json.loads(versions[key]['manifest']);allowed={v['key'] for v in manifest['variables']}
 body={'runtime_id':rt['id'],'name':key,'project_name':project,'catalog_app_version_id':versions[key]['id'],'environment':{'values':[{'key':k,'value':v} for k,v in values.items() if k in allowed]}}
 preview=api('POST','/api/v1/applications/preflight',{k:v for k,v in body.items() if k not in ['name','project_name']})
 if not preview['compose_compatible']:raise RuntimeError('incorrect Compose compatibility')
 install=api('POST','/api/v1/applications',body);operation=install['operation']['id']
 deadline=time.monotonic()+480
 while time.monotonic()<deadline:
  result=api('GET','/api/v1/operations/'+operation)
  if result['status']=='SUCCEEDED':break
  if result['status'] in ['FAILED','ROLLED_BACK','ROLLBACK_FAILED','CANCELLED']:raise RuntimeError(key+' install '+result['status']+' '+str(result.get('error','')))
  time.sleep(2)
 else:raise RuntimeError(key+' installation timed out')
 containers=command('docker','ps','-q','--filter','label=com.docker.compose.project='+project).split()
 observed=json.loads(command('docker','inspect',*containers))
 if not observed or any(v['State'].get('Health',{}).get('Status')!='healthy' for v in observed):raise RuntimeError(key+' lacks healthy containers')
 installed[key]={'application':install['application']['id'],'container':observed[0]['Id']}
 results.append({'app':key,'status':'SUCCEEDED','healthy_services':len(observed)})
 print(json.dumps(results[-1]),flush=True)
# Validate actual database authentication, data retention, and admin-tool connectivity.
def exec_in(key,*args):
 return command('docker','exec',installed[key]['container'],*args)
def database_query(key,sql):
 if key=='mysql':return exec_in(key,'env','MYSQL_PWD='+secret,'mysql','--protocol=TCP','-h127.0.0.1','-uapp','-Dapp','-N','-e',sql)
 return exec_in(key,'env','PGPASSWORD='+secret,'psql','-h127.0.0.1','-Uapp','-dapp','-At','-c',sql)
for key in ['redis','mysql','postgresql']:
 if key=='redis':exec_in(key,'env','REDISCLI_AUTH='+secret,'redis-cli','SET','nsc_market_marker',suffix)
 else:database_query(key,"CREATE TABLE nsc_market_marker (value varchar(64)); INSERT INTO nsc_market_marker VALUES ('"+suffix+"');")
 services=api('GET','/api/v1/services?application_id='+installed[key]['application']+'&limit=100')
 service=next(s for s in services if s['name']==key)
 result=api('POST','/api/v1/services/'+service['id']+'/restart',{})
 deadline=time.monotonic()+180
 while time.monotonic()<deadline:
  operation=api('GET','/api/v1/operations/'+result['id'])
  if operation['status']=='SUCCEEDED':break
  if operation['status'] in ['FAILED','ROLLBACK_FAILED','ROLLED_BACK']:raise RuntimeError(key+' restart failed')
  time.sleep(2)
 else:raise RuntimeError(key+' restart timeout')
 value=exec_in(key,'env','REDISCLI_AUTH='+secret,'redis-cli','GET','nsc_market_marker') if key=='redis' else database_query(key,'SELECT value FROM nsc_market_marker;')
 if value!=suffix:raise RuntimeError(key+' data did not survive restart')
 print(json.dumps({'app':key,'authenticated_data_after_restart':True}),flush=True)
exec_in('phpmyadmin','env','MYSQL_TEST_PASSWORD='+secret,'php','-r',"$db=new mysqli(getenv('PMA_HOST'),'app',getenv('MYSQL_TEST_PASSWORD'),'app'); if($db->query('SELECT 1')->fetch_row()[0] != 1) exit(1);")
exec_in('pgadmin','env','PGPASSWORD='+secret,'/venv/bin/python3','-c',"import importlib; db=importlib.import_module('psycopg'); c=db.connect(host='postgresql',user='app',dbname='app'); assert c.execute('SELECT 1').fetchone()[0]==1")
print(json.dumps({'admin_database_connectivity':'passed'}),flush=True)
print(json.dumps({'results':results,'fixtures':prefix,'production_resources_modified':False}),flush=True)
