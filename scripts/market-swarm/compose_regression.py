from common import *

network=prefix+'-compose-backend'
inner(manager,'network','create',network)
pool=api('POST','/gateway-pools',{'name':prefix+'-compose','backend_network':network})
rt=api('POST','/runtimes',{'name':prefix+'-compose','pool_id':pool['id'],'runtime_type':'docker','endpoint':'node://'+state['bindings'][0]['node_id'],'management_enabled':True})
versions={}
for version in api('GET','/catalog/apps?limit=100'):
 if version.get('swarm_yaml') or version['app_key']=='nginx':versions[version['app_key']]=version
for key in ['nginx','redis','mysql','postgresql','wordpress','pgadmin','phpmyadmin']:
 project=prefix+'-compose-'+key
 values={'REDIS_PASSWORD':secret,'MYSQL_ROOT_PASSWORD':secret+'root','MYSQL_PASSWORD':secret,'POSTGRES_PASSWORD':secret,'PGADMIN_DEFAULT_EMAIL':'admin@example.com','PGADMIN_DEFAULT_PASSWORD':secret,'PMA_HOST':prefix+'-compose-mysql-mysql-1','DATABASE_NETWORK':prefix+('-compose-postgresql_default' if key=='pgadmin' else '-compose-mysql_default')}
 manifest={} if key=='nginx' else json.loads(versions[key]['manifest']);allowed={v['key'] for v in manifest.get('variables',[])}
 result=api('POST','/applications',{'runtime_id':rt['id'],'name':'Compose '+key,'project_name':project,'catalog_app_version_id':versions[key]['id'],'environment':{'values':[{'key':k,'value':v} for k,v in values.items() if k in allowed]}})
 wait(result)
 ids=inner(manager,'ps','-q','--filter','label=com.docker.compose.project='+project).split()
 if not ids:raise RuntimeError('Compose installation did not create containers: '+key)
 if inner(manager,'service','ls','-q','--filter','label=com.docker.stack.namespace='+project):raise RuntimeError('Compose installation created Swarm services')
 print(json.dumps({'app':key,'compose_regression':True}),flush=True)
