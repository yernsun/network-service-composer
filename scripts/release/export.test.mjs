import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { exportCandidate } from './export.mjs'

function fixture(t) {
  const base = mkdtempSync(join(tmpdir(), 'nsc-release-export-')), root = join(base, 'source'), target = join(base, 'candidate')
  mkdirSync(root)
  t.after(() => rmSync(base, { recursive: true, force: true }))
  const write = (path, text) => { mkdirSync(dirname(join(root, path)), {recursive:true}); writeFileSync(join(root, path), text) }
  const git = (...args) => execFileSync('git', ['-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', ...args], {cwd:root, stdio:'pipe'}).toString().trim()
  git('init', '-q')
  for (const path of ['LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.md']) write(path, 'Fixture legal text\n')
  write('VERSION', '0.1.0\n'); write('third_party/dependencies.json', '{}\n')
  write('.gitignore', '.env\ncoverage/\n')
  write('README.md', 'Old content\n'); git('add', '.'); git('commit', '-qm', 'Old private history')
  const old = git('rev-parse', 'HEAD')
  write('README.md', 'Public candidate\n')
  write('.env', 'PRIVATE_FIXTURE_DATA=do-not-copy\n'); write('coverage/raw.log', 'private evidence\n')
  return {root,target,write,git,old}
}

test('export retains pending source changes and license text without old history or ignored evidence', t => {
  const f=fixture(t), report=exportCandidate(f.root,f.target)
  assert.equal(report.status,'UNVERIFIED_CANDIDATE')
  assert.equal(readFileSync(join(f.target,'README.md'),'utf8'),'Public candidate\n')
  assert.equal(existsSync(join(f.target,'.env')),false)
  assert.equal(existsSync(join(f.target,'coverage')),false)
  assert.equal(f.git('rev-parse','HEAD'),f.old)
  assert.notEqual(report.commit,f.old)
  assert.throws(()=>execFileSync('git',['cat-file','-e',f.old],{cwd:f.target,stdio:'pipe'}))
  assert.throws(()=>exportCandidate(f.root,f.target),/must not exist/)
})

test('export fails closed for tracked secrets, external links and files outside allowlist', t => {
  for (const kind of ['secret','symlink','unknown']) {
    const f=fixture(t)
    if(kind==='secret') { f.write('config/fixture.key','fixture'); f.git('add','-f','config/fixture.key') }
    if(kind==='symlink') symlinkSync(join(f.root,'README.md'),join(f.root,'third_party/linked-license'))
    if(kind==='unknown') f.write('operator-settings.txt','fixture')
    assert.throws(()=>exportCandidate(f.root,f.target))
    assert.equal(existsSync(f.target),false)
  }
})
