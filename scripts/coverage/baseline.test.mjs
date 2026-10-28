import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { genuineRoot, initialBaseline, resolveBaseline } from './baseline.mjs'

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'nsc-baseline-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const git = (...args) => execFileSync('git', ['-c', 'user.name=NSC Fixture', '-c', 'user.email=fixture@example.invalid', ...args], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
  git('init', '-q')
  writeFileSync(join(root, 'source'), 'initial')
  git('add', '.'); git('commit', '-qm', 'Initial fixture')
  return { root, git, first: git('rev-parse', 'HEAD') }
}
const zero = '0'.repeat(40)

test('root push and root tag establish a baseline only for committed clean sources', t => {
  const f = fixture(t)
  assert.equal(genuineRoot(f.root), true)
  for (const ref of ['branch', 'tag']) {
    assert.equal(resolveBaseline(f.root, zero, ref), zero)
    assert.equal(resolveBaseline(f.root, undefined, ref), zero)
  }
  assert.equal(initialBaseline(f.root, zero), true)
  assert.equal(initialBaseline(f.root, 'HEAD'), false)
  writeFileSync(join(f.root, 'new-source'), 'uncommitted')
  assert.throws(() => initialBaseline(f.root, zero), /unchanged/)
})

test('push and PR compare to available history; tag uses its real parent', t => {
  const f = fixture(t)
  writeFileSync(join(f.root, 'source'), 'changed')
  f.git('commit', '-qam', 'Second fixture')
  assert.equal(genuineRoot(f.root), false)
  assert.equal(resolveBaseline(f.root, f.first, 'branch'), f.first)
  assert.equal(resolveBaseline(f.root, f.first, 'pull_request'), f.first)
  assert.equal(resolveBaseline(f.root, undefined, 'tag'), f.first)
  assert.equal(resolveBaseline(f.root, zero, 'tag'), f.first)
  for (const base of [undefined, zero]) assert.throws(() => resolveBaseline(f.root, base, 'branch'), /require/)
  assert.throws(() => resolveBaseline(f.root, 'missing', 'tag'), /missing/)
  assert.throws(() => initialBaseline(f.root, zero), /genuine initial/)
  writeFileSync(join(f.root, '.git', 'shallow'), f.git('rev-parse', 'HEAD') + '\n')
  assert.equal(genuineRoot(f.root), false)
  assert.throws(() => initialBaseline(f.root, zero), /genuine initial/)
  assert.throws(() => resolveBaseline(f.root, undefined, 'tag'), /parent commit/)
})
