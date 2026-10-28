from common import *
import ssl,re
pool=next(p for p in api('GET','/gateway-pools?limit=100') if p['name']==prefix)
installed=json.loads((work/'installed.json').read_text());pki=work/'pki';binary=state['binary'];host=prefix+'.test'
try:
 inner(manager,'inspect','fixture-gateway'); gateway_exists=True
except RuntimeError: gateway_exists=False
if not gateway_exists:
 gateway_id=cmd(binary,'id','gateway')
 cmd(binary,'pki','issue-agent','--ca-cert',str(pki/'ca.crt'),'--ca-key',str(pki/'ca.key'),'--cert',str(pki/'agent.crt'),'--key',str(pki/'agent.key'),'--pool-id',pool['id'],'--gateway-id',gateway_id,'--valid-for','2h')
 gateway_image=state['gateway_image']
 archive=work/'gateway.tar';cmd('docker','save','-o',str(archive),gateway_image)
 with archive.open('rb') as data:subprocess.run(['docker','exec','-i',manager,'docker','load'],stdin=data,stdout=subprocess.DEVNULL,check=True)
 cmd('docker','exec',manager,'mkdir','-p','/fixture/agent-pki')
 for f in ['ca.crt','agent.crt','agent.key']:
  with (pki/f).open('rb') as data:subprocess.run(['docker','exec','-i',manager,'sh','-c','cat > /fixture/agent-pki/'+f],stdin=data,check=True)
 inner(manager,'run','-d','--name','fixture-gateway','--user','0','--network',prefix+'-backend','-p','8444:443','-p','8088:80','-v','/fixture/agent-pki:/run/nsc:ro','-v',prefix+'-gateway-state:/var/lib/nsc',
  '-e','NSC_AGENT__GATEWAY_ID='+gateway_id,'-e','NSC_AGENT__POOL_ID='+pool['id'],'-e','NSC_AGENT__PUBLIC_ADDRESS=127.0.0.1','-e','NSC_AGENT__NAME=market-swarm-gateway','-e','NSC_AGENT__CONTROLLER_ADDRESS='+state['ips'][0]+':9443','-e','NSC_AGENT__SERVER_NAME='+manager,'-e','NSC_AGENT__CA_CERT_FILE=/run/nsc/ca.crt','-e','NSC_AGENT__TLS_CERT_FILE=/run/nsc/agent.crt','-e','NSC_AGENT__TLS_KEY_FILE=/run/nsc/agent.key','-e','NSC_AGENT__STATE_DIR=/var/lib/nsc','-e','NSC_NGINX__GENERATION_DIR=/var/lib/nsc/generations','-e','NSC_AGENT__HEARTBEAT_INTERVAL=1s','-e','NSC_STORAGE__DRIVER=postgres','-e','NSC_STORAGE__DSN=postgres://nsc:'+secret+'@'+state['ips'][0]+':5439/nsc?sslmode=disable',gateway_image,'--config=')
cmd('openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(work/'website.key'),'-out',str(work/'website.crt'),'-days','1','-subj','/CN='+host,'-addext','subjectAltName=DNS:'+host,'-addext','basicConstraints=critical,CA:FALSE','-addext','extendedKeyUsage=serverAuth')
certificate=api('POST','/certificates',{'pool_id':pool['id'],'certificate_pem':(work/'website.crt').read_text(),'private_key_pem':(work/'website.key').read_text()})
domain=next((d for d in api('GET','/domains?limit=100') if d['host']==host),None) or api('POST','/domains',{'pool_id':pool['id'],'host':host,'entrypoints':['http','https'],'redirect_http_to_https':True,'tls':{'mode':'custom','certificate_id':certificate['id']}})
services=api('GET','/services?application_id='+installed['wordpress']['application']+'&limit=100');service=next(s for s in services if s['service_key']==installed['wordpress']['project']+'_wordpress')
binding=next((b for b in api('GET','/services/'+service['id']+'/domain-bindings') if b['domain']['id']==domain['id']),None) or api('POST','/services/'+service['id']+'/domain-bindings',{'domain_id':domain['id'],'route':{'path':'/','backend':{'port':80,'protocol':'http'}}})
if binding.get('operation') or binding.get('type'): wait(binding)
# Traffic originates inside the same isolated manager namespace. Resolve the
# public host to that daemon's published Gateway port, with TLS SNI unchanged.
def https(path, method='GET', data=None, cookie=None):
 args=['run','--rm','--network','host','--entrypoint','curl',state['image'],'-k','-sS','-D','-','--resolve',host+':8444:127.0.0.1','-X',method]
 if cookie:args+=['-H','Cookie: '+cookie]
 if data:args+=['--data',data]
 args+=['https://'+host+':8444'+path]
 return inner(manager,*args)
for i in range(120):
 result=https('/wp-admin/install.php?step=1&language=en_US')
 if 'Welcome' in result and '502 Bad Gateway' not in result:break
 time.sleep(2)
else:raise RuntimeError('WordPress HTTPS installer unavailable')
from urllib.parse import urlencode
admin_password=secret+'-website-admin'
result=https('/wp-admin/install.php?step=2','POST',urlencode({'weblog_title':'NSC Swarm','user_name':'siteadmin','admin_password':admin_password,'admin_password2':admin_password,'language':'en_US','admin_email':'siteadmin@example.test','blog_public':'0','Submit':'Install WordPress'}))
if 'Success!' not in result:raise RuntimeError('WordPress HTTPS installation failed')
# Ensure generated canonical links preserve proxy HTTPS instead of downgrading.
if 'http://'+host in result:raise RuntimeError('WordPress installer downgraded HTTPS')
login=https('/wp-login.php','POST',urlencode({'log':'siteadmin','pwd':admin_password,'wp-submit':'Log In','redirect_to':'https://'+host+':8444/wp-admin/','testcookie':'1'}),'wordpress_test_cookie=WP%20Cookie%20check')
if 'wordpress_logged_in_' not in login or 'Location: http://' in login:raise RuntimeError('WordPress HTTPS login failed or downgraded')
print(json.dumps({'wordpress_https_installer':True,'wordpress_https_login':True,'independent_admin_credentials':True}),flush=True)
