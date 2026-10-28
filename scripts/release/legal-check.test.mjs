import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { legalCheck } from './legal-check.mjs'

test('redistribution gate rejects missing, stale, tampered and unconfirmed evidence', t => {
  const root=mkdtempSync(join(tmpdir(),'nsc-legal-'))
  t.after(()=>rmSync(root,{recursive:true,force:true}))
  const put=(path,text)=>{mkdirSync(dirname(join(root,path)),{recursive:true});writeFileSync(join(root,path),text)}
  const sha=text=>createHash('sha256').update(text).digest('hex')
  const inputs=Object.fromEntries(['go.mod','go.sum','frontend/package.json','frontend/package-lock.json','VERSION'].map(path=>{put(path,'fixture');return[path,sha('fixture')]}))
  for(const path of ['LICENSE','NOTICE','THIRD_PARTY_NOTICES.md','third_party/license.txt','third_party/approval.txt'])put(path,'fixture')
  const inventory={inputs,entries:[{ecosystem:'go',name:'example.invalid/lib',version:'v1',linked:true,notices:[{path:'third_party/license.txt',sha256:sha('fixture')}]}],unresolved:[]}
  const save=()=>put('third_party/dependencies.json',JSON.stringify(inventory))
  save();put('third_party/provenance.json',JSON.stringify({assets:[{id:'asset',status:'PENDING',evidence:'third_party/approval.txt'}]}))
  assert.equal(legalCheck(root).status,'BLOCKED')
  put('third_party/provenance.json',JSON.stringify({assets:[{id:'asset',status:'CONFIRMED',evidence:'third_party/approval.txt'}]}))
  assert.equal(legalCheck(root).status,'PASS')
  put('third_party/license.txt','changed')
  assert.equal(legalCheck(root).problems[0].category,'modified-third-party-notice')
  inventory.entries[0].notices=[];save()
  assert.equal(legalCheck(root).problems[0].category,'missing-license-copy')
  put('go.mod','new dependency')
  assert(legalCheck(root).problems.some(p=>p.category==='stale-license-inventory'))
})
