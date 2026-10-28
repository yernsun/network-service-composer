import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'

const read = file => readFileSync(file, 'utf8')
const policy = JSON.parse(read('.agents/rules/documentation.json'))
const exceptions = new Set([
  ...policy.historical, ...policy.provenance, ...policy.immutable.map(item => item.path),
  'docs/repository-rename.md', // Documents the breaking changes and original inputs.
  'frontend/README.md', 'frontend/scripts/check-archive.mjs', // Original archive filename.
  'frontend/scripts/brand.mjs', 'frontend/scripts/brand.check.mjs', // Explicit archive adaptation.
  'internal/deployment/application_network_test.go', // Old-label rejection fixtures.
])
const oldSlug = ['nginx', 'service', 'composer'].join('-')
const oldName = new RegExp(['nginx', 'service', 'composer'].join('[- ]'), 'i')
const files = execFileSync('git', ['ls-files', '-z', '--cached', '--others', '--exclude-standard'], { encoding: 'utf8' }).split('\0').filter(Boolean)
const stale = []
for (const file of new Set(files)) {
  if (!existsSync(file)) continue // Pending deletions are absent from the candidate.
  if (exceptions.has(file) || file.startsWith('frontend/reference/') || file === `${oldSlug}.tar.gz`) continue
  const bytes = readFileSync(file)
  if (!bytes.includes(0) && oldName.test(bytes.toString('utf8'))) stale.push(file)
}
assert.deepEqual(stale, [], `Stale product names: ${stale.join(', ')}`)
const modulePath = 'github.com/yernsun/network-service-composer'
assert.ok(read('go.mod').startsWith(`module ${modulePath}\n`))
assert.ok(read('frontend/go.mod').includes(`module ${modulePath}/frontend\n`))
assert.ok(read('api/proto/nsc/gateway/v2/gateway.proto').includes(`"${modulePath}/api/gen/nsc/gateway/v2;gatewayv2"`))
assert.equal(JSON.parse(read('frontend/package.json')).name, 'network-service-composer-frontend')
assert.equal(JSON.parse(read('frontend/package-lock.json')).name, 'network-service-composer-frontend')
assert.ok(read('compose.yaml').startsWith('name: network-service-composer\n'))
for (const { code } of JSON.parse(read('internal/i18n/locales.json'))) {
  const catalog = JSON.parse(read(`internal/i18n/catalogs/${code}.json`))
  assert.equal(catalog['app.name'], 'Network Service Composer')
  assert.equal(catalog['login.heading'], 'Network Service Composer')
}
assert.ok(read('README.md').startsWith('# Network Service Composer for Nginx\n'))
console.log('Product names, module paths, runtime identifiers and locale brands are consistent.')
