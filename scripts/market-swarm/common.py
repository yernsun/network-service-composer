import json,pathlib,sys,time,subprocess,uuid,urllib.request,urllib.error
state=json.loads(pathlib.Path(sys.argv[1]).read_text());manager,worker=state['nodes'];secret=state['secret'];base=state['base'];prefix=state['prefix'];node=state['bindings'][1]['node_id'];work=pathlib.Path(state['work']);opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def cmd(*args):
 p=subprocess.run(args,capture_output=True,text=True)
 if p.returncode:raise RuntimeError(' '.join(args[:4])+': '+p.stderr[-800:].replace(secret,'[redacted]'))
 return p.stdout.strip()
def inner(n,*args):return cmd('docker','exec',n,'docker',*args)
def api(method,path,body=None,headers=None):
 req=urllib.request.Request(base+'/api/v1'+path,data=None if body is None else json.dumps(body).encode(),method=method,headers={'Authorization':'Bearer '+secret,'Content-Type':'application/json','Idempotency-Key':str(uuid.uuid4()),**(headers or {})})
 try:
  with opener.open(req,timeout=90) as r:return json.load(r)['data']
 except urllib.error.HTTPError as e:raise RuntimeError(path+': '+e.read().decode()[:1200].replace(secret,'[redacted]'))
def wait(value):
 operation=value.get('operation',value);deadline=time.monotonic()+480
 while time.monotonic()<deadline:
  result=api('GET','/operations/'+operation['id'])
  if result['status']=='SUCCEEDED':return result
  if result['status'] in ['FAILED','ROLLED_BACK','ROLLBACK_FAILED']:raise RuntimeError(result['status']+': '+result.get('error_message','')+' '+result.get('log','')[-1000:])
  time.sleep(2)
 raise RuntimeError('operation timeout')
