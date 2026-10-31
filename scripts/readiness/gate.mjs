import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const digest = value => createHash('sha256').update(value).digest('hex')
const hex = /^[a-f0-9]{64}$/
const validAttempt = value => typeof value === 'string' && /^[1-9][0-9]*$/.test(value) && Number.isSafeInteger(Number(value))
export function identity(root) {
  const git = args => execFileSync('git', args, { cwd: root, encoding: 'utf8' }).trim()
  const commit = git(['rev-parse', 'HEAD'])
  const files = [...new Set(git(['ls-files', '-z', '--cached', '--others', '--exclude-standard']).split('\0').filter(Boolean))].sort()
  const fingerprint = digest(files.map(file => `${file}\0${existsSync(resolve(root, file)) ? digest(readFileSync(resolve(root, file))) : 'DELETED'}\0`).join(''))
  const protocol = readFileSync(resolve(root, 'internal/gateway/server.go'), 'utf8').match(/const ProtocolVersion int32 = (\d+)/)?.[1]
  const renderer = readFileSync(resolve(root, 'internal/nginx/render.go'), 'utf8').match(/const RendererVersion = "([^"]+)"/)?.[1]
  if (!protocol || !renderer) throw new Error('Missing protocol/renderer authority')
  const migrations = {}
  for (const driver of ['postgres', 'mysql', 'sqlite']) {
    const dir = resolve(root, 'internal/storage/migrations', driver)
    migrations[driver] = digest(readdirSync(dir).filter(file => file.endsWith('.sql')).sort().map(file => `${file}\0${digest(readFileSync(resolve(dir, file)))}\0`).join(''))
  }
  return { ...(process.env.GITHUB_RUN_ID ? { run: { id: process.env.GITHUB_RUN_ID, attempt: process.env.GITHUB_RUN_ATTEMPT } } : {}), source: { commit, fingerprint }, contracts: { protocol: Number(protocol), renderer, migrations } }
}

export function record(root, job, outcomes, imageRefs = {}) {
  if (!/^[a-z0-9-]+$/.test(job)) throw new Error('Invalid job identity')
  const current = identity(root)
  const policy = JSON.parse(readFileSync(resolve(root, '.github/readiness-policy.json')))
  const entry = policy.jobs.find(value => value.id === job)
  const required = entry?.checks
  if (!required?.length) throw new Error('Job has no required acceptance policy')
  const checks = required.map(id => { const outcome = outcomes[id]; return { id, status: outcome === 'success' ? 'PASS' : ({ failure: 'FAIL', skipped: 'SKIPPED', cancelled: 'CANCELLED' }[outcome] || 'BLOCKED') } })
  const images = {}, errors = []
  if (process.env.GITHUB_SHA && current.source.commit !== process.env.GITHUB_SHA) errors.push({ code: 'source_revision_mismatch' })
  // A failed image build still has useful step outcomes. Retain them without
  // publishing Docker stderr, image references or unverified identity fields.
  for (const name of entry.images || []) {
    const ref = imageRefs[name]
    let inspected
    try {
      if (typeof ref !== 'string' || !ref) throw new Error('Missing image')
      inspected = JSON.parse(execFileSync('docker', ['image', 'inspect', ref], {
        encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], timeout: 10000, maxBuffer: 1024 * 1024,
      }))[0]
      if (!inspected || typeof inspected !== 'object') throw new Error('Missing inspection')
    } catch {
      errors.push({ code: 'image_identity_unavailable', image: name })
      continue
    }
    const labels = inspected.Config?.Labels || {}
    if (labels['org.opencontainers.image.revision'] !== current.source.commit || labels['org.nsc.source.fingerprint'] !== current.source.fingerprint) {
      errors.push({ code: 'image_identity_mismatch', image: name })
      continue
    }
    if (!/^sha256:[a-f0-9]{64}$/.test(inspected.Id)) {
      errors.push({ code: 'image_identity_invalid', image: name })
      continue
    }
    images[name] = { digest: inspected.Id, digest_type: 'oci-image-config', ...current.source }
  }
  return { schema: 1, job, ...current, status: !errors.length && checks.every(check => check.status === 'PASS') ? 'PASS' : 'FAIL', checks, images, errors, exclusions: { ovh: 'DISABLED' } }
}

export function evaluate(expected, policy, reports, jobResults) {
  const errors = [], byJob = new Map(), seen = new Set()
  if (policy.schema !== 1 || !Array.isArray(policy.jobs) || !policy.jobs.length || new Set(policy.jobs.map(job => job.id)).size !== policy.jobs.length || policy.jobs.some(job => !job.id || !job.workflowJob || !Array.isArray(job.checks) || !job.checks.length)) throw new Error('Invalid or empty readiness policy')
  if (!hex.test(expected.source.fingerprint)) errors.push('invalid expected fingerprint')
  if (expected.run && (!expected.run.id || !validAttempt(expected.run.attempt))) errors.push('invalid expected workflow run')
  for (const report of reports) {
    if (!policy.jobs.some(job => job.id === report.job)) { errors.push('unexpected evidence identity'); continue }
    if (expected.run && (report.run?.id !== expected.run.id || !validAttempt(report.run?.attempt) || Number(report.run.attempt) > Number(expected.run.attempt))) {
      errors.push(`workflow run mismatch: ${report.job}`)
      continue
    }
    const key = expected.run ? `${report.job}/${report.run.attempt}` : report.job
    if (seen.has(key)) errors.push(`duplicate evidence: ${report.job}`)
    seen.add(key)
    const previous = byJob.get(report.job)
    // GitHub's partial reruns retain successful jobs from previous attempts.
    // Select by attempt before checking status: a newer failure must never fall
    // back to an older PASS, and input/download order must not affect admission.
    if (!previous || (expected.run && Number(report.run.attempt) > Number(previous.run.attempt))) byJob.set(report.job, report)
  }
  for (const job of policy.jobs) {
    const report = byJob.get(job.id)
    if (!report) { errors.push(`missing evidence: ${job.id}`); continue }
    if (jobResults[job.workflowJob] !== 'success') errors.push(`required job did not succeed: ${job.workflowJob}`)
    if (report.schema !== 1 || report.status !== 'PASS') errors.push(`failed evidence: ${job.id}`)
    if (report.errors?.length) errors.push(`evidence retains failures: ${job.id}`)
    if (JSON.stringify(report.source) !== JSON.stringify(expected.source) || JSON.stringify(report.contracts) !== JSON.stringify(expected.contracts)) errors.push(`source/contract mismatch: ${job.id}`)
    for (const check of job.checks) {
      const matches = (report.checks || []).filter(value => value.id === check)
      if (matches.length !== 1 || matches[0].status !== 'PASS') errors.push(`required check not passed: ${job.id}/${check}`)
    }
    for (const name of job.images || []) {
      const image = report.images?.[name]
      if (!image || !/^sha256:[a-f0-9]{64}$/.test(image.digest) || image.commit !== expected.source.commit || image.fingerprint !== expected.source.fingerprint) errors.push(`image identity missing/mismatched: ${job.id}/${name}`)
    }
    if (report.exclusions?.ovh !== 'DISABLED') errors.push(`OVH policy changed: ${job.id}`)
  }
  const evidence_runs = expected.run ? Object.fromEntries(policy.jobs.filter(job => byJob.has(job.id)).map(job => [job.id, byJob.get(job.id).run])) : undefined
  return { schema: 1, ...expected, status: errors.length ? 'FAIL' : 'PASS', errors, ...(evidence_runs ? { evidence_runs } : {}), required_jobs: policy.jobs.map(job => job.id), exclusions: { ovh: 'DISABLED' } }
}

function save(path, value) {
  mkdirSync(dirname(resolve(path)), { recursive: true, mode: 0o700 })
  writeFileSync(path, JSON.stringify(value, null, 2) + '\n', { mode: 0o600 })
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const [command, ...args] = process.argv.slice(2)
    const root = process.cwd()
    if (command === 'identity') console.log(JSON.stringify(identity(root)))
    else if (command === 'fingerprint') console.log(identity(root).source.fingerprint)
    else if (command === 'github-output') { const value = identity(root); console.log(`fingerprint=${value.source.fingerprint}\ncommit=${value.source.commit}`) }
    else if (command === 'record' && args.length === 2) {
      const report = record(root, args[0], JSON.parse(process.env.NSC_CHECK_RESULTS || '{}'), JSON.parse(process.env.NSC_EVIDENCE_IMAGES || '{}'))
      save(args[1], report)
      if (report.status !== 'PASS') process.exitCode = 1
    } else if (command === 'gate' && args.length === 2) {
      const policy = JSON.parse(readFileSync(resolve(root, '.github/readiness-policy.json')))
      const reports = readdirSync(args[0], { recursive: true }).filter(path => path.endsWith('.json')).map(path => JSON.parse(readFileSync(resolve(args[0], path))))
      const result = evaluate(identity(root), policy, reports, JSON.parse(process.env.NSC_REQUIRED_JOBS || '{}'))
      save(args[1], result)
      console.log(result.status)
      if (result.status !== 'PASS') {
        console.error(result.errors.map(error => `- ${error}`).join('\n'))
        process.exitCode = 1
      }
    } else throw new Error('Usage: gate.mjs identity|fingerprint|github-output|record <job> <output>|gate <evidence-directory> <output>')
  } catch (error) { console.error(error.message); process.exitCode = 1 }
}
