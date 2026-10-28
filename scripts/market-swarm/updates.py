from common import *
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

installed=json.loads((work/"installed.json").read_text())

article=json.loads(exec_service('wordpress','wordpress','php','-r',"require '/var/www/html/wp-load.php'; $p=wp_insert_post(['post_title'=>'NSC retained article','post_content'=>'"+prefix+"','post_status'=>'publish']); $u=wp_upload_bits('nsc-fixture.txt',null,'"+prefix+"'); if(is_wp_error($p)||$u['error'])exit(1); echo json_encode(['id'=>$p,'url'=>$u['url']]);"))
for key in ['redis','mysql','postgresql','wordpress','pgadmin','phpmyadmin']:
 appid=installed[key]['application'];app=api('GET','/applications/'+appid)
 wait(api('POST','/applications/'+appid+'/revisions',{}, {'If-Match':'"rev-'+str(app['revision'])+'"'}))
 if verify(key)!=prefix:raise RuntimeError('update lost data '+key)
 print(json.dumps({'app':key,'update_data_retained':True}),flush=True)
print(exec_service('wordpress','wordpress','php','-r',"require '/var/www/html/wp-load.php'; $p=get_posts(['title'=>'NSC retained article']); if(!$p||$p[0]->post_content!='"+prefix+"')exit(1); $u=wp_upload_dir(); if(file_get_contents($u['path'].'/nsc-fixture.txt')!='"+prefix+"')exit(1); echo 'wordpress_article_upload_retained';"),flush=True)
# Verify the persisted article and upload through the real HTTPS Gateway.
from urllib.parse import urlparse
host=prefix+'.test'
for path in ['/?p='+str(article['id']),urlparse(article['url']).path]:
 response=inner(manager,'run','--rm','--network','host','--entrypoint','curl',state['image'],'-k','-sS','--resolve',host+':8444:127.0.0.1','https://'+host+':8444'+path)
 if prefix not in response:raise RuntimeError('WordPress HTTPS article or upload lost after update')
print(json.dumps({'wordpress_https_article_upload_retained':True}),flush=True)
appid=installed['redis']['application'];wait(api('POST','/applications/'+appid+'/operations',{'action':'stop'}))
volumes=inner(worker,'volume','ls','-q','--filter','label=com.docker.stack.namespace='+installed['redis']['project']).split()
if len(volumes)!=1:raise RuntimeError('Redis volume identity ambiguous')
# Docker retains historical stopped task containers. Remove only the Redis
# fixture's exited containers before deliberately deleting its retained volume.
for cid in inner(worker,'ps','-aq','--filter','label=com.docker.swarm.service.name='+installed['redis']['project']+'_redis').split():
 if inner(worker,'inspect','--format','{{.State.Running}}',cid)!='false':raise RuntimeError('Redis task still running')
 inner(worker,'rm',cid)
inner(worker,'volume','rm',volumes[0])
operation=api('POST','/applications/'+appid+'/operations',{'action':'start'})
try:wait(operation);raise RuntimeError('missing volume start accepted')
except RuntimeError as e:
 if 'FAILED' not in str(e):raise
if volumes[0] in inner(worker,'volume','ls','-q').split():raise RuntimeError('empty replacement volume created')
print(json.dumps({'missing_volume_start_blocked_without_replacement':True}),flush=True)
