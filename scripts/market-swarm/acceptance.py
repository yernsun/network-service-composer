from common import *
for i in range(90):
 try:api('GET','/settings');break
 except Exception:time.sleep(1)
else:raise RuntimeError('Controller not ready')
network=prefix+'-backend'
try: inner(manager,'network','inspect',network)
except RuntimeError: inner(manager,'network','create','--driver','overlay','--attachable',network)
pool=next((p for p in api('GET','/gateway-pools?limit=100') if p['name']==prefix),None) or api('POST','/gateway-pools',{'name':prefix,'backend_network':network,'challenge_upstream':'http://fixture-controller:8080'})
rt=next((r for r in api('GET','/runtimes?limit=100') if r['name']==prefix),None) or api('POST','/runtimes',{'name':prefix,'pool_id':pool['id'],'runtime_type':'swarm','endpoint':'node://'+state['bindings'][0]['node_id'],'management_enabled':True})
versions={}
for v in api('GET','/catalog/apps?limit=100'):
 if v.get('swarm_yaml') or v['app_key']=='nginx':versions[v['app_key']]=v
installed=json.loads((work/"installed.json").read_text()) if (work/"installed.json").exists() else {}
for key in ['nginx','redis','mysql','postgresql','wordpress','pgadmin','phpmyadmin']:
 if key in installed: continue
 project=prefix+'-'+key
 values={'REDIS_PASSWORD':secret,'MYSQL_ROOT_PASSWORD':secret+'root','MYSQL_PASSWORD':secret,'POSTGRES_PASSWORD':secret,'PGADMIN_DEFAULT_EMAIL':'admin@example.com','PGADMIN_DEFAULT_PASSWORD':secret,'PMA_HOST':prefix+'-mysql_mysql','DATABASE_NETWORK':prefix+('-postgresql' if key=='pgadmin' else '-mysql')}
 manifest={} if key=='nginx' else json.loads(versions[key]['manifest']);allowed={v['key'] for v in manifest.get('variables',[])}
 body={'runtime_id':rt['id'],'name':key,'project_name':project,'catalog_app_version_id':versions[key]['id'],'environment':{'values':[{'key':k,'value':v} for k,v in values.items() if k in allowed]}}
 if key!='nginx':body['deployment_plan']={'data_node_id':node}
 preview=api('POST','/applications/preflight',{k:v for k,v in body.items() if k not in ['name','project_name']})
 if not preview['swarm_compatible']:raise RuntimeError('variant incompatible '+key)
 prior=next((a for a in api('GET','/applications?limit=100') if a['project_name']==project),None)
 install={'application':prior,'operation':api('POST','/applications/'+prior['id']+'/operations',{'action':'start'})} if prior else api('POST','/applications',body)
 wait(install)
 services=json.loads(inner(manager,'service','inspect',*inner(manager,'service','ls','-q','--filter','label=com.docker.stack.namespace='+project).split()))
 for s in services:
  if s['Spec']['Mode']['Replicated']['Replicas']!=1:raise RuntimeError('not single replica')
  if key!='nginx' and 'node.id == '+node not in s['Spec']['TaskTemplate']['Placement']['Constraints']:raise RuntimeError('data node not pinned')
 installed[key]={'application':install['application']['id'],'project':project,'services':[s['Spec']['Name'] for s in services]}
 (work/'installed.json').write_text(json.dumps(installed));print(json.dumps({'app':key,'service_mode':True,'data_node_verified':key!='nginx'}),flush=True)
def exec_service(key,service,*args):
 target=installed[key]['project']+'_'+service
 cid=inner(worker,'ps','-q','--filter','label=com.docker.swarm.service.name='+target)
 if not cid:raise RuntimeError('no worker container '+target)
 return inner(worker,'exec',cid,*args)
def verify(key):
 if key=='redis':return exec_service(key,key,'env','REDISCLI_AUTH='+secret,'redis-cli','GET','nsc_marker')
 if key=='mysql':return exec_service(key,key,'env','MYSQL_PWD='+secret,'mysql','-h127.0.0.1','-uapp','-Dapp','-N','-e','SELECT value FROM nsc_marker')
 if key=='postgresql':return exec_service(key,key,'env','PGPASSWORD='+secret,'psql','-h127.0.0.1','-Uapp','-dapp','-At','-c','SELECT value FROM nsc_marker')
 return exec_service(key,key,'cat','/var/www/html/nsc-marker' if key=='wordpress' else '/var/lib/pgadmin/nsc-marker' if key=='pgadmin' else '/sessions/nsc-marker')
for key in ['redis','mysql','postgresql','wordpress','pgadmin','phpmyadmin']:
 if key=='redis':exec_service(key,key,'env','REDISCLI_AUTH='+secret,'redis-cli','SET','nsc_marker',prefix)
 elif key=='mysql':exec_service(key,key,'env','MYSQL_PWD='+secret,'mysql','-h127.0.0.1','-uapp','-Dapp','-e',"CREATE TABLE IF NOT EXISTS nsc_marker(value varchar(64)); DELETE FROM nsc_marker; INSERT INTO nsc_marker VALUES ('"+prefix+"')")
 elif key=='postgresql':exec_service(key,key,'env','PGPASSWORD='+secret,'psql','-h127.0.0.1','-Uapp','-dapp','-c',"CREATE TABLE IF NOT EXISTS nsc_marker(value varchar(64)); DELETE FROM nsc_marker; INSERT INTO nsc_marker VALUES ('"+prefix+"')")
 else:exec_service(key,key,'sh','-c','printf %s '+prefix+' > '+('/var/www/html/nsc-marker' if key=='wordpress' else '/var/lib/pgadmin/nsc-marker' if key=='pgadmin' else '/sessions/nsc-marker'))
 appid=installed[key]['application'];services=api('GET','/services?application_id='+appid+'&limit=100');selected=next(s for s in services if s['service_key']==installed[key]['project']+'_'+key)
 wait(api('POST','/services/'+selected['id']+'/restart',{}))
 if verify(key)!=prefix:raise RuntimeError('restart lost data '+key)
 wait(api('POST','/applications/'+appid+'/operations',{'action':'stop'}))
 if inner(manager,'service','ls','-q','--filter','label=com.docker.stack.namespace='+installed[key]['project']):raise RuntimeError('stop left services')
 wait(api('POST','/applications/'+appid+'/operations',{'action':'start'}))
 if verify(key)!=prefix:raise RuntimeError('start lost data '+key)
 print(json.dumps({'app':key,'restart_stop_start_data_retained':True}),flush=True)
exec_service('phpmyadmin','phpmyadmin','env','TEST_PASSWORD='+secret,'php','-r',"$db=new mysqli(getenv('PMA_HOST'),'app',getenv('TEST_PASSWORD'),'app');exit($db->query('SELECT 1')->fetch_row()[0]==1?0:1);")
exec_service('pgadmin','pgadmin','env','PGPASSWORD='+secret,'/venv/bin/python3','-c',"import psycopg; c=psycopg.connect(host='"+prefix+"-postgresql_postgresql',user='app',dbname='app'); assert c.execute('SELECT 1').fetchone()[0]==1")
print(json.dumps({'database_tools_connected':True}),flush=True)
