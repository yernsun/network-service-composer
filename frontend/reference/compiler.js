
/* NSC prototype compiler. Deliberately bounded YAML subset, never executes code.
 * Production must use a pinned official Compose/Stack validator, not this parser.
 * This file is standalone so its rules can be tested in Node as well as browsers.
 */

'use strict';
const own=(o,k)=>Object.prototype.hasOwnProperty.call(o,k);
const blocked=new Set(['__proto__','constructor','prototype']);
function stripComment(s) {
 let q='',esc=false;
 for(let i=0;i<s.length;i++){const c=s[i];if(esc){esc=false;continue;}if(q==='"'&&c==='\\'){esc=true;continue;}if(q){if(c===q){if(q==="'"&&s[i+1]==="'"){i++;continue;}q='';}}else if(c==='"'||c==="'"){q=c;}else if(c==='#'&&(i===0||/\s/.test(s[i-1]))){return s.slice(0,i).trimEnd();}}
 return s.trimEnd();
}
function colon(s){let q='',depth=0;for(let i=0;i<s.length;i++){let c=s[i];if(q){if(c==='\\'&&q==='"'){i++;continue;}if(c===q)q='';continue;}if(c==='"'||c==="'"){q=c;continue;}if(c==='['||c==='{')depth++;if(c===']'||c==='}')depth--;if(c===':'&&depth===0&&(i===s.length-1||/\s/.test(s[i+1])))return i;}return -1;}
function scalar(s,line){s=s.trim();if(!s)return null;if(/^[&*!>|]/.test(s)||s==='---'||s==='...')throw Error(`第 ${line} 行：演示解析器不支持锚点、别名、标签或多行标量；原文件未被修改。`);if(s.startsWith('"')){try{return JSON.parse(s);}catch(_){throw Error(`第 ${line} 行：双引号字符串无效。`);}}if(s.startsWith("'")){if(!s.endsWith("'"))throw Error(`第 ${line} 行：缺少结束引号。`);return s.slice(1,-1).replace(/''/g,"'");}if(s.startsWith('[')||s.startsWith('{')){try{return JSON.parse(s);}catch(_){throw Error(`第 ${line} 行：行内集合仅支持 JSON 格式；请改为分行 YAML。`);}}if(s==='null'||s==='~')return null;if(s==='true')return true;if(s==='false')return false;if(/^-?(?:0|[1-9]\d*)(?:\.\d+)?$/.test(s))return Number(s);return s;}
function parseYAML(text){
 if(typeof text!=='string'||text.length>65536)throw Error('演示编排文件上限为 64 KiB。');
 const ls=text.split(/\r?\n/).map((s,i)=>{if(/^\s*\t/.test(s))throw Error(`第 ${i+1} 行：缩进不能使用 Tab。`);s=stripComment(s);return {text:s.trim(),indent:s.length-s.trimStart().length,line:i+1};}).filter(l=>l.text);
 if(!ls.length)throw Error('编排文件为空。');if(ls[0].indent)throw Error('顶层必须从第 1 列开始。');let at=0;
 function block(indent,depth){if(depth>24)throw Error('嵌套过深，已停止解析。');const arr=ls[at].text.startsWith('- '),out=arr?[]:Object.create(null);
  while(at<ls.length&&ls[at].indent===indent){const row=ls[at];if(row.text.startsWith('- ')!==arr)throw Error(`第 ${row.line} 行：列表与映射混用。`);if(arr){const v=row.text.slice(2);if(colon(v)>=0)throw Error(`第 ${row.line} 行：演示暂不支持列表中的对象，请使用端口/挂载的简写字符串。`);out.push(scalar(v,row.line));at++;if(at<ls.length&&ls[at].indent>indent)throw Error(`第 ${ls[at].line} 行：标量列表不能继续缩进。`);}else{const ci=colon(row.text);if(ci<0)throw Error(`第 ${row.line} 行：需要 key: value。`);const key=String(scalar(row.text.slice(0,ci),row.line));if(blocked.has(key)||key==='<<'||key.includes('$'))throw Error(`第 ${row.line} 行：不允许的键或动态键。`);if(own(out,key))throw Error(`第 ${row.line} 行：重复键 ${key}，不能静默覆盖。`);const tail=row.text.slice(ci+1).trim();at++;if(tail){out[key]=scalar(tail,row.line);if(at<ls.length&&ls[at].indent>indent)throw Error(`第 ${ls[at].line} 行：上一项已经是标量。`);}else if(at<ls.length&&ls[at].indent>indent)out[key]=block(ls[at].indent,depth+1);else out[key]=null;}}
  if(at<ls.length&&ls[at].indent>indent)throw Error(`第 ${ls[at].line} 行：缩进不一致。`);return out;
 }
 const obj=block(0,0);if(at<ls.length)throw Error(`第 ${ls[at].line} 行：缩进不一致。`);return obj;
}
function parseEnv(text){const rows=Object.create(null);const errors=[];for(const [i,line] of text.split(/\r?\n/).entries()){const s=stripComment(line).trim();if(!s)continue;const m=s.match(/^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/);if(!m){errors.push(`第 ${i+1} 行：只支持单行 KEY=VALUE。`);continue;}const k=m[1];if(blocked.has(k)||own(rows,k)){errors.push(`第 ${i+1} 行：键重复或不允许。`);continue;}try{let v=m[2];if(v.startsWith('"')||v.startsWith("'"))v=scalar(v,i+1);if(String(v).includes('${')||String(v).includes('$('))throw Error('值按字面量保存；导入不执行变量展开或 Shell。');rows[k]=String(v);}catch(e){errors.push(`第 ${i+1} 行：${e.message}`);}}return {rows,errors};}
function interpolate(text,vars,path='',refs=[],depth=0){if(depth>16)throw Error('变量默认表达式嵌套过深。');let out='';
 for(let i=0;i<text.length;){if(text[i]!=='$'){out+=text[i++];continue;}if(text[i+1]==='$'){out+='$';i+=2;continue;}let expr='',end=i+1;
  if(text[i+1]==='{'){let balance=1,j=i+2;for(;j<text.length&&balance;j++){if(text[j]==='{')balance++;else if(text[j]==='}')balance--;}if(balance)throw Error(`${path}：变量表达式缺少 }。`);expr=text.slice(i+2,j-1);end=j;}else{const m=text.slice(i+1).match(/^[_A-Za-z][_A-Za-z0-9]*/);if(!m){out+='$';i++;continue;}expr=m[0];end=i+1+expr.length;}
  const m=expr.match(/^([_A-Za-z][_A-Za-z0-9]*)(?:(:-|-|:\?|\?|:\+|\+)([\s\S]*))?$/);if(!m)throw Error(`${path}：不支持的变量表达式。`);
  const [,key,op,arg='']=m;const set=own(vars,key),v=set?vars[key]:null,secret=v?.type==='credential';const value=set?String(v?.value??v??''):'';const nonempty=set&&value!=='';let chosen=false,result='';
  if(!op){if(!set)throw Error(`${path}：缺少变量 ${key}。NSC 不静默替换为空值。`);chosen=true;result=value;}
  else if(op===':-'||op==='-'){chosen=op===':-'?nonempty:set;result=chosen?value:interpolate(arg,vars,path,refs,depth+1);}
  else if(op===':?'||op==='?'){if(!(op===':?'?nonempty:set))throw Error(`${path}：${key} 必须${op===':?'?'设置且非空':'设置'}。`);chosen=true;result=value;}
  else{const use=op===':+'?nonempty:set;result=use?interpolate(arg,vars,path,refs,depth+1):'';}
  if(secret)throw Error(`${path}：${key} 是凭据引用，不可展开到 YAML。请使用 secrets 文件挂载。`);
  refs.push({key,path,source:chosen?(v?.source||'变量'):'表达式默认/替代值',value:result,expression:'$'+'{'+expr+'}'});out+=result;i=end;
 }return out;}
function expandTree(v,vars,refs,path=''){if(typeof v==='string')return interpolate(v,vars,path,refs);if(Array.isArray(v))return v.map((s,i)=>expandTree(s,vars,refs,`${path}[${i}]`));if(v&&typeof v==='object'){const out=Object.create(null);for(const [k,s] of Object.entries(v))out[k]=expandTree(s,vars,refs,path?`${path}.${k}`:k);return out;}return v;}
function dump(v,indent=0){const p=' '.repeat(indent);if(Array.isArray(v))return v.map(x=>p+'- '+JSON.stringify(x)).join('\n');if(v&&typeof v==='object')return Object.entries(v).map(([k,val])=>p+k+':'+(val&&typeof val==='object'?'\n'+dump(val,indent+2):' '+JSON.stringify(val))).join('\n');return p+JSON.stringify(v);}
const ALLOWED_TOP=new Set(['version','name','services','networks','volumes','secrets','configs']);
const ALLOWED_SVC=new Set(['image','environment','env_file','ports','expose','volumes','networks','command','entrypoint','working_dir','user','read_only','restart','mem_limit','cpus','deploy','healthcheck','labels','secrets','configs']);
function compile(text,vars,{target='stack',envFiles={}}={}){const errors=[],warnings=[],refs=[];let original,tree;try{original=parseYAML(text);tree=expandTree(original,vars,refs);}catch(e){return {errors:[e.message],warnings,refs,tree:null,text:'',runtime:[]};}
 for(const k of Object.keys(tree))if(!ALLOWED_TOP.has(k))errors.push(`顶层 ${k} 不在演示支持范围；不执行也不静默忽略。`);
 if(!tree.services||Array.isArray(tree.services)||typeof tree.services!=='object')errors.push('必须提供 services 映射。');const runtime=[];
 for(const [name,s] of Object.entries(tree.services||{})){if(!s||typeof s!=='object'||Array.isArray(s)){errors.push(`${name}：服务定义应为映射。`);continue;}const at='services.'+name;
  const invalid=[];for(const key of ['deploy','healthcheck'])if(s[key]!==undefined&&(!s[key]||typeof s[key]!=='object'||Array.isArray(s[key])))invalid.push(key+' 需要映射');
  for(const key of ['ports','expose','volumes','secrets','configs'])if(s[key]!==undefined&&!Array.isArray(s[key]))invalid.push(key+' 需要列表');
  if(s.env_file!==undefined&&typeof s.env_file!=='string'&&!Array.isArray(s.env_file))invalid.push('env_file 需要字符串或列表');
  if(s.environment!==undefined&&(!s.environment||typeof s.environment!=='object'))invalid.push('environment 需要映射或列表');
  if(invalid.length){errors.push(...invalid.map(m=>at+'：'+m));continue;}
  if(s.deploy?.mode&&!['replicated','replicated-job'].includes(s.deploy.mode))errors.push(at+'：演示只支持 replicated / replicated-job；不把 global 模式改成副本模式。');

  for(const k of Object.keys(s))if(!ALLOWED_SVC.has(k))errors.push(`${at}.${k} 不在演示支持范围。`);
  if(!s.image||!/^[a-zA-Z0-9][a-zA-Z0-9._/@:-]*$/.test(String(s.image)))errors.push(`${at}.image：需要明确的镜像引用。`);
  if(s.deploy){for(const k of Object.keys(s.deploy))if(!['mode','replicas','resources','restart_policy','placement','update_config','rollback_config','labels'].includes(k))errors.push(`${at}.deploy.${k} 不受演示支持。`);if(s.deploy.replicas!==undefined&&(!/^\d+$/.test(String(s.deploy.replicas))||Number(s.deploy.replicas)>50))errors.push(`${at}.deploy.replicas：应为 0–50 的整数。`);if(s.deploy.replicas!==undefined)s.deploy.replicas=Number(s.deploy.replicas);}
  const cpu=s.deploy?.resources?.limits?.cpus??s.cpus;if(cpu!==undefined&&(!(Number(cpu)>0)||Number(cpu)>128))errors.push(`${at}：CPU 上限应为大于 0、不超过 128 的数值。`);
  const mem=s.deploy?.resources?.limits?.memory??s.mem_limit;if(mem!==undefined&&!/^\d+(?:\.\d+)?[bkmg]?(?:b)?$/i.test(String(mem)))errors.push(`${at}：内存格式无效，例如 512M、1G。`);
  for(const p of s.expose||[])if(!/^\d+(?:\/tcp|\/udp)?$/.test(String(p))||parseInt(p)<1||parseInt(p)>65535)errors.push(`${at}.expose：端口应为 1–65535。`);
  if(s.ports?.length){for(const p of s.ports){if(typeof p!=='string'||p.split(':').slice(-2).some(n=>!/^\d+(?:\/tcp|\/udp)?$/.test(n)||parseInt(n)<1||parseInt(n)>65535))errors.push(`${at}.ports：需要合法的端口简写。`);}warnings.push(`${name} 发布主机/集群端口，须核对暴露范围和占用；不是 Nginx Route。`);}
  if(target==='stack'&&(s.restart!==undefined||s.mem_limit!==undefined||s.cpus!==undefined))errors.push(`${name}：Stack 请使用 deploy.restart_policy / deploy.resources，不能静默忽略单机字段。`);
  if(target==='compose'&&s.deploy?.mode==='replicated-job')errors.push(`${name}：此演示不把 Swarm Job 自动转换为单机任务。`);
  if(target==='compose'&&s.deploy?.placement)errors.push(`${name}：节点调度约束只适用于集群目标。`);
  if(s.volumes?.some(v=>String(v).includes('/var/run/docker.sock')||String(v).startsWith('/')))warnings.push(`${name} 引用宿主机路径；需验证权限、可用节点与数据保留。`);
  if(s.secrets||s.configs)warnings.push(`${name} 引用外部配置/凭据文件；本地原型不验证目标资产或读取机密。`);
  let env=Object.create(null);const files=Array.isArray(s.env_file)?s.env_file:s.env_file?[s.env_file]:[];
  for(const f of files){if(!own(envFiles,f)){errors.push(`${at}.env_file：缺少文件 ${String(f)}。`);continue;}const parsed=parseEnv(envFiles[f]);errors.push(...parsed.errors.map(e=>`${f}：${e}`));for(const [k,v] of Object.entries(parsed.rows)){if(/(PASSWORD|TOKEN|SECRET|PRIVATE_KEY)$/i.test(k))errors.push(`${f}.${k}：离线原型不保存明文凭据，使用凭据文件引用。`);env[k]={value:v,source:'env_file · '+f};}}
  const explicit=Array.isArray(s.environment)?Object.fromEntries(s.environment.map(row=>{const i=String(row).indexOf('=');if(i<0){errors.push(`${at}.environment：拒绝隐式读取执行机变量，请填写 KEY=value。`);return [String(row),''];}return [row.slice(0,i),row.slice(i+1)];})):s.environment||{};
  for(const [k,v] of Object.entries(explicit)){if(v===null){errors.push(`${at}.environment.${k}：请明确设置值或空字符串。`);continue;}if(/(PASSWORD|TOKEN|SECRET|PRIVATE_KEY)$/i.test(k))errors.push(`${at}.environment.${k}：此演示不允许明文凭据注入，改为凭据文件引用。`);env[k]={value:String(v),source:env[k]?'environment（覆盖 env_file）':'environment'};}
  runtime.push({service:name,env,unknownImageDefaults:true});
 }
 if(!Object.keys(tree.services||{}).length)errors.push('至少需要一个服务。');
 if(target==='stack')warnings.push('Stack：NSC 先冻结变量并渲染，再交给 Stack 兼容性校验；不会要求 docker stack deploy 自动读取 .env。');
 warnings.push('这里只检查演示规则；未执行 Docker 官方校验、网络探测、端口占用或镜像拉取。');
 return {errors:[...new Set(errors)],warnings:[...new Set(warnings)],refs,tree,text:dump(tree)+'\n',runtime};
}
export default {parseYAML,parseEnv,interpolate,expandTree,dump,compile};
