import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync, symlinkSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { checkContracts, checkLinks } from './contracts.mjs'

const repo = resolve(import.meta.dirname, '../..')
function fixture(t) {
  const root = mkdtempSync(resolve(tmpdir(), 'nsc-contracts-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const put = (name, text) => { mkdirSync(dirname(resolve(root, name)), { recursive: true }); writeFileSync(resolve(root, name), text) }
  const copy = name => put(name, readFileSync(resolve(repo, name)))
  const rules = ['workflow.md', 'harness.md', 'testing.md', 'coverage.json', 'i18n.md', 'code-style.md', 'documentation.md', 'documentation.json']
  for (const name of rules) name.endsWith('.json') ? copy(`.agents/rules/${name}`) : put(`.agents/rules/${name}`, '# English rule\n')
  put('AGENTS.md', rules.map(name => `[Rule](.agents/rules/${name})`).join('\n'))
  for (const name of ['nsc-verify', 'nsc-boundary-tests', 'nsc-i18n']) put(`.agents/skills/${name}/SKILL.md`, `---\nname: ${name}\ndescription: Perform a repository-specific workflow.\n---\n\nRead [testing](../../rules/testing.md).\n`)
  const policy = JSON.parse(readFileSync(resolve(root, '.agents/rules/documentation.json')))
  for (const name of ['README.md', 'ARCHITECTURE.md', 'CONTRIBUTING.md', 'SECURITY.md', 'frontend/README.md', ...policy.historical, ...policy.provenance, ...policy.legal]) put(name, '# Historical record\n\nEnglish documentation.\n')
  copy('internal/i18n/locales.json')
  const locales = JSON.parse(readFileSync(resolve(root, 'internal/i18n/locales.json')))
  for (const item of policy.immutable) {
    copy(item.path)
    for (const locale of locales) for (const file of ['README.md', 'operations.md']) put(`${dirname(item.path)}/docs/${locale.code}/${file}`, '# Manual\n')
  }
  return { root, put, policy, setPolicy: () => put('.agents/rules/documentation.json', JSON.stringify(policy)) }
}

test('valid contracts retain all supported locales and immutable assets', t => {
  const { root } = fixture(t)
  const report = checkContracts(root)
  assert.equal(report.skills, 3); assert.equal(report.immutable, 6); assert.equal(report.locales.length, 8)
})

for (const [name, mutate, error] of [
  ['missing rule', f => rmSync(resolve(f.root, '.agents/rules/testing.md')), /Missing/],
  ['unlinked rule', f => f.put('AGENTS.md', '# Instructions'), /must link/],
  ['duplicate authority', f => mkdirSync(resolve(f.root, 'rules')), /Legacy/],
  ['malformed policy', f => f.put('.agents/rules/coverage.json', '{'), /JSON/],
  ['incomplete coverage policy', f => f.put('.agents/rules/coverage.json', '{"frontend":{"branches":89}}'), /threshold/],
  ['invalid skill', f => f.put('.agents/skills/nsc-verify/SKILL.md', '---\nname: wrong\ndescription: Wrong name.\n---\n'), /metadata/],
  ['malformed skill YAML', f => f.put('.agents/skills/nsc-verify/SKILL.md', '---\nname: nsc-verify\ndescription: Verify changes.\nmetadata: [\n---\nBody\n'), /metadata/],
  ['empty skill instructions', f => f.put('.agents/skills/nsc-verify/SKILL.md', '---\nname: nsc-verify\ndescription: Verify changes.\n---\n'), /metadata/],
  ['personal dependency', f => f.put('.agents/skills/nsc-verify/SKILL.md', '---\nname: nsc-verify\ndescription: Verify changes.\n---\nRead /home/' + 'user/skill\n'), /Nonportable/],
  ['changed immutable asset', f => f.put(f.policy.immutable[0].path, '# Changed'), /artifact changed/],
  ['unapproved exception', f => { f.policy.immutable[0].path = 'README.md'; f.setPolicy() }, /Invalid immutable/],
  ['duplicate exception', f => { f.policy.immutable[1] = f.policy.immutable[0]; f.setPolicy() }, /exception list/],
  ['unexplained exception', f => { f.policy.immutable[0].reason = ''; f.setPolicy() }, /Invalid immutable/],
  ['incorrect English target', f => { f.policy.immutable[0].english = 'README.md'; f.setPolicy() }, /destination/],
  ['missing locale manual', f => rmSync(resolve(f.root, 'internal/core/builtin/redis/docs/ru/operations.md')), /Missing/],
  ['empty manual', f => f.put('internal/core/builtin/redis/docs/es/README.md', ''), /Empty/],
  ['historical relabeling', f => f.put(f.policy.historical[0], '# Current acceptance'), /historical label/],
  ['missing class', f => { delete f.policy.legal; f.setPolicy() }, /classification/],
  ['wrong default language', f => { f.policy.default_language = 'fr'; f.setPolicy() }, /classification/],
  ['invalid locale source', f => f.put('internal/i18n/locales.json', '[{"code":"en"},{"code":"en"}]'), /locale authority/],
]) test(`reject ${name}`, t => { const f = fixture(t); mutate(f); assert.throws(() => checkContracts(f.root), error) })

test('local links resolve against document directory; examples and Unicode are allowed', t => {
  const f = fixture(t)
  f.put('docs/example.md', '# Example café 日本語\n[Home](../README.md) [Web](https://example.com/) [mail](mailto:a@example.com) [heading](#example)\n```md\n[Example](missing.md)\n```\n')
  assert.doesNotThrow(() => checkLinks(f.root, resolve(f.root, 'docs/example.md')))
  for (const link of ['missing.md', '../../outside.md', '%ZZ']) {
    f.put('docs/example.md', `[Invalid](${link})`)
    assert.throws(() => checkLinks(f.root, resolve(f.root, 'docs/example.md')))
  }
  symlinkSync('/tmp', resolve(f.root, 'docs/outside'))
  f.put('docs/example.md', '[Outside](outside)')
  assert.throws(() => checkLinks(f.root, resolve(f.root, 'docs/example.md')), /Symlink/)
  assert.throws(() => checkContracts(f.root), /Symlink/)
})
