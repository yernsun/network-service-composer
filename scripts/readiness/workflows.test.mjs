import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import YAML from '../../frontend/node_modules/yaml/dist/index.js'
const ci = YAML.parse(readFileSync(new URL('../../.github/workflows/ci.yml', import.meta.url), 'utf8'))
const release = YAML.parse(readFileSync(new URL('../../.github/workflows/release.yml', import.meta.url), 'utf8'))
const policy = JSON.parse(readFileSync(new URL('../../.github/readiness-policy.json', import.meta.url), 'utf8'))
test('all required workflows are prerequisites of fail-closed admission', () => {
  assert.equal(ci.jobs.readiness.if, 'always()')
  const enforce = ci.jobs.readiness.steps.find(s=>s.run?.includes('gate.mjs gate'))
  assert.equal(enforce.if, 'always()')
  assert.ok(enforce.run.includes('mkdir -p coverage/readiness-input'))
  assert.equal(ci.on.workflow_call, null)
  for (const entry of policy.jobs) {
    assert.ok(ci.jobs[entry.workflowJob], entry.workflowJob)
    assert.ok(ci.jobs.readiness.needs.includes(entry.workflowJob), entry.id)
  }
  for (const [jobName, job] of Object.entries(ci.jobs)) {
    const ids = (job.steps || []).filter(s => s.id).map(s => s.id)
    assert.equal(new Set(ids).size, ids.length, jobName)
    for (const step of job.steps || []) if (step.uses) assert.match(step.uses, /@[a-f0-9]{40}$/)
  }
})
test('step outcomes and source identity are always retained without private fixture trees', () => {
  for (const name of new Set(policy.jobs.map(v => v.workflowJob))) {
    const steps = ci.jobs[name].steps
    const capture = steps.find(s => s.run?.includes('gate.mjs record'))
    assert.equal(capture.if, 'always()', name)
    const outcomes = JSON.parse(capture.env.NSC_CHECK_RESULTS)
    for (const entry of policy.jobs.filter(j=>j.workflowJob===name)) for (const check of entry.checks) assert.ok(outcomes[check]?.includes('.outcome'), name+'/'+check)
    if (capture.env.NSC_EVIDENCE_IMAGES?.includes('NSC_TEST_IMAGE_TAG')) assert.ok(steps.some(s=>s.run?.includes('NSC_TEST_IMAGE_TAG=v$(cat VERSION)')),name)
    assert.ok(capture.env.NSC_CHECK_RESULTS.includes('.outcome'), name)
    const upload = steps.find(s => s.with?.name?.startsWith('readiness-'))
    assert.equal(upload.if, 'always()', name)
    assert.ok(upload.with.name.includes('github.run_attempt'),name)
    assert.match(upload.with.path, /^coverage\/readiness\/[^/]+\.json$/)
  }
  const p0uploads=ci.jobs['p0-acceptance'].steps.filter(s=>s.uses?.startsWith('actions/upload-artifact'))
  assert.equal(p0uploads.length,1)
  assert.equal(p0uploads[0].with['if-no-files-found'],'error')
})
test('partial reruns preserve envelopes from all attempts and exclude admission outputs', () => {
  const download = ci.jobs.readiness.steps.find(s => s.uses?.startsWith('actions/download-artifact'))
  assert.equal(download.with.pattern, 'readiness-*-${{ github.run_id }}-*')
  assert.equal(download.with['merge-multiple'], false)
  const decision = ci.jobs.readiness.steps.find(s => s.uses?.startsWith('actions/upload-artifact'))
  assert.equal(decision.with.name, 'admission-decision-${{ github.run_id }}-${{ github.run_attempt }}')
  assert.equal(decision.if, 'always()')
  assert.equal(decision.with['if-no-files-found'], 'error')
})
test('release reuses complete admission and creates release after both artifact preparations', () => {
  assert.equal(release.jobs.verify.uses,'./.github/workflows/ci.yml')
  assert.deepEqual(release.jobs['create-release'].needs,['verify','binaries','images'])
  assert.equal(release.jobs.binaries.needs,'verify')
  assert.equal(release.jobs.images.needs,'verify')
  assert.ok(release.jobs['validate-tag'].steps.some(s=>s.run?.includes('v$(cat VERSION)')))
  const publish=release.jobs.images.steps.find(s=>s.id==='candidate')
  assert.ok(publish.with.outputs.includes('push-by-digest=true'))
  assert.equal(release.jobs.images.steps.filter(s=>s.uses?.startsWith('docker/build-push-action')).length,1)
  assert.equal(release.jobs.images.steps.find(s=>s.id==='meta').with.flavor,'latest=false')
  for (const arch of ['amd64','arm64']) assert.ok(release.jobs.images.steps.find(s=>s.id==='scan_'+arch).with['image-ref'].includes('architectures.outputs.'+arch))
  assert.ok(release.jobs['create-release'].steps.find(s=>s.with?.pattern).with.pattern.includes('github.run_attempt'))
  assert.equal(publish.with.provenance,'mode=max')
  assert.equal(publish.with.sbom,true)
  assert.ok(publish.with['build-args'].includes('SOURCE_FINGERPRINT='))
  assert.ok(publish.with['build-args'].includes('SOURCE_REVISION='))
})
