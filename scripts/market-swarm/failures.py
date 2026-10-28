from common import *
installed=json.loads((work/"installed.json").read_text())

appid=installed['redis']['application'];app=api('GET','/applications/'+appid);source=api('GET','/applications/'+appid+'/source')
raw=source['compose_yaml']
# The official Redis health command is replaced without changing its storage.
# Parse the reviewed source: serialization may reorder healthcheck keys.
parser="""import { parse, stringify } from './frontend/node_modules/yaml/dist/index.js'; import { readFileSync } from 'node:fs'; const document=parse(readFileSync(0,'utf8')); if(!document.services.redis.healthcheck)throw new Error('Missing Redis healthcheck'); document.services.redis.healthcheck={test:['CMD','false'],interval:'1s',timeout:'1s',retries:1,start_period:'1s'}; process.stdout.write(stringify(document));"""
raw=subprocess.run(['node','--input-type=module','-e',parser],input=raw,capture_output=True,text=True,check=True).stdout
value=api('POST','/applications/'+appid+'/revisions',{'compose_yaml':raw,'environment':{'raw':source['runtime_env']},'deployment_plan':{'data_node_id':node}}, {'If-Match':'"rev-'+str(app['revision'])+'"'})
try:wait(value);raise RuntimeError('unhealthy update accepted')
except RuntimeError as e:
 if not any(status in str(e) for status in ['FAILED','ROLLED_BACK','ROLLBACK_FAILED']):raise
print(json.dumps({'failed_update_recorded':True}),flush=True)
# Restore explicitly if automatic rollback has not completed successfully.
app=api('GET','/applications/'+appid)
wait(api('POST','/applications/'+appid+'/revisions',{'compose_yaml':source['compose_yaml'],'environment':{'raw':source['runtime_env']},'deployment_plan':{'data_node_id':node}}, {'If-Match':'"rev-'+str(app['revision'])+'"'}))
if inner(worker,'exec',inner(worker,'ps','-q','--filter','label=com.docker.swarm.service.name='+installed['redis']['project']+'_redis'),'env','REDISCLI_AUTH='+secret,'redis-cli','GET','nsc_marker')!=prefix:raise RuntimeError('failed update lost original Redis data')
print(json.dumps({'failed_update_original_data_retained':True}),flush=True)
cmd('docker','pause',worker)
try:
 time.sleep(35)
 app=api('GET','/applications/'+appid)
 try: wait(api('POST','/applications/'+appid+'/operations',{'action':'start'}));raise RuntimeError('offline node accepted')
 except RuntimeError as e:
  if not any(text in str(e) for text in ['FAILED','ready','Ready','active','Active','retention','data','node','validation.application_preflight_failed']):raise
 services=json.loads(inner(manager,'service','inspect',installed['redis']['project']+'_redis'))
 if 'node.id == '+node not in services[0]['Spec']['TaskTemplate']['Placement']['Constraints']:raise RuntimeError('offline data rescheduled')
 print(json.dumps({'offline_node_blocked_without_rescheduling':True}),flush=True)
finally:cmd('docker','unpause',worker)
time.sleep(15)
