"""Exercise public volume API, real HA workers and the authenticated node route."""
from common import *
for attempt in range(90):
 try: api('GET','/settings'); break
 except Exception: time.sleep(1)
else: raise RuntimeError('controller not ready')
network=prefix+'-volume-backend'
inner(manager,'network','create','--driver','overlay','--attachable',network)
pool=api('POST','/gateway-pools',{'name':prefix+'-volumes','backend_network':network,'challenge_upstream':'http://fixture-controller:8080'})
runtime=api('POST','/runtimes',{'name':prefix+'-volumes','pool_id':pool['id'],'runtime_type':'swarm','endpoint':'node://'+state['bindings'][0]['node_id'],'management_enabled':True})
root='/runtimes/'+runtime['id']+'/volumes'
manager_node=state['bindings'][0]['node_id']
def path(name='',target=node): return root+('/'+name if name else '')+'?node_id='+target
def detail(name,target=node):
 req=urllib.request.Request(base+'/api/v1'+path(name,target),headers={'Authorization':'Bearer '+secret})
 with opener.open(req,timeout=30) as response:
  result=json.load(response)
  if not result['complete']: raise RuntimeError('incomplete volume evidence')
  return result['data'],response.headers['ETag']
def expect_status(method,url,body,headers,status):
 req=urllib.request.Request(base+'/api/v1'+url,data=json.dumps(body).encode(),method=method,headers={'Authorization':'Bearer '+secret,'Content-Type':'application/json','Idempotency-Key':str(uuid.uuid4()),**headers})
 try:
  with opener.open(req,timeout=30) as response: raise RuntimeError('unexpected success '+str(response.status))
 except urllib.error.HTTPError as error:
  if error.code!=status: raise RuntimeError('unexpected rejection '+str(error.code))
name=prefix+'-data'
for target in [manager_node,node]: wait(api('POST',path(target=target),{'name':name}))
worker_volume,worker_etag=detail(name)
manager_volume,manager_etag=detail(name,manager_node)
if worker_volume['engine_id']==manager_volume['engine_id'] or not worker_volume['owned'] or not manager_volume['owned']: raise RuntimeError('node-local ownership is incorrect')
# Anonymous creation retry returns one operation and one generated volume.
key=str(uuid.uuid4());before={v['name'] for v in api('GET',path())}
operation=api('POST',path(),{'name':''},{'Idempotency-Key':key});wait(operation)
if api('POST',path(),{'name':''},{'Idempotency-Key':key})['id']!=operation['id']: raise RuntimeError('duplicate anonymous operation')
after=api('GET',path());created=[v for v in after if v['name'] not in before]
if len(created)!=1 or created[0]['kind']!='anonymous' or not created[0]['owned']: raise RuntimeError('anonymous create lost identity')
anonymous=created[0]['name']
# An external zero-replica Service must immediately block worker deletion,
# without waiting for another manager report to collect its specification.
inner(manager,'service','create','--name',prefix+'-volume-zero','--replicas','0','--no-resolve-image','--constraint','node.id == '+node,'--mount','type=volume,source='+name+',target=/data','alpine:3.24','sleep','600')
expect_status('DELETE',path(name),{'acknowledge_data_loss':True},{'If-Match':worker_etag},409)
wait(api('DELETE',path(name,manager_node),{'acknowledge_data_loss':True},{'If-Match':manager_etag}))
inner(worker,'volume','inspect',name)
inner(manager,'service','rm',prefix+'-volume-zero')
# Simulate external metadata replacement after the user obtained an ETag.
old,old_etag=detail(name);inner(worker,'volume','rm',name);inner(worker,'volume','create','--label','external-change=true',name)
expect_status('DELETE',path(name),{'acknowledge_data_loss':True},{'If-Match':old_etag},412)
for volume_name in [name,anonymous]:
 value,etag=detail(volume_name)
 wait(api('DELETE',path(volume_name),{'acknowledge_data_loss':True},{'If-Match':etag}))
 if any(v['name']==volume_name for v in api('GET',path())): raise RuntimeError('deleted volume still exists')
print(json.dumps({'volume_api':True,'two_node_identity':True,'anonymous_retry':True,'live_zero_replica_guard':True,'external_recreation_guard':True}),flush=True)
