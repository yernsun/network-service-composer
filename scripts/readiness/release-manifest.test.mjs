import { test } from 'node:test'
import assert from 'node:assert/strict'
import { selectArchitectures, promote } from './release-manifest.mjs'
const digest = char => 'sha256:' + char.repeat(64)
const repository = 'ghcr.io/example/nsc-controller'
const index = () => ({ manifests: ['amd64', 'arm64'].map((architecture, i) => ({ platform: { os: 'linux', architecture }, digest: digest(String(i)) })) })
const expected = { source: { commit: 'a'.repeat(40), fingerprint: 'b'.repeat(64) }, contracts: { protocol: 4, renderer: 'fixture', migrations: {} } }
const report = () => ({ ...structuredClone(expected), ...selectArchitectures(index(), digest('c'), repository) })
test('resolve exactly both final architecture digests and ignore unattached provenance descriptors', () => {
  const value = index(); value.manifests.push({ platform: { os: 'unknown', architecture: 'unknown' }, digest: digest('d') })
  assert.deepEqual(selectArchitectures(value, digest('c'), repository).architectures, { amd64: digest('0'), arm64: digest('1') })
  for (const change of [v => v.manifests.pop(), v => v.manifests.push(v.manifests[0]), v => v.manifests[0].digest = 'latest', v => v.manifests[1].digest = v.manifests[0].digest]) {
    const bad = index(); change(bad); assert.throws(() => selectArchitectures(bad, digest('c'), repository))
  }
})
test('no registry promotion before both actual architecture scans and matching source contracts', () => {
  for (const results of [{}, { amd64: 'success' }, { amd64: 'success', arm64: 'failure' }, { amd64: 'success', arm64: 'skipped' }]) {
    const calls = []; assert.throws(() => promote(report(), expected, 'v0.2.0', results, args => calls.push(args))); assert.equal(calls.length, 0)
  }
  const stale = report(); stale.source.fingerprint = '0'.repeat(64)
  assert.throws(() => promote(stale, expected, 'v0.2.0', { amd64: 'success', arm64: 'success' }, () => assert.fail('stale source was promoted')))
})
test('promote the same tested index without rebuilding or publishing a stable alias', () => {
  const calls = [], candidate = report()
  const result = promote(candidate, expected, 'v0.2.0', { amd64: 'success', arm64: 'success' }, args => { calls.push(args); return candidate.digest })
  assert.equal(result.status, 'PASS'); assert.equal(calls.length, 2)
  assert.deepEqual(calls[0], ['buildx', 'imagetools', 'create', '--tag', repository + ':v0.2.0', repository + '@' + candidate.digest])
  assert.throws(() => promote(candidate, expected, 'v0.2.0', { amd64: 'success', arm64: 'success' }, () => digest('d')))
})
