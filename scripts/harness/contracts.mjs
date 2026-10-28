import { createHash } from 'node:crypto'
import { existsSync, lstatSync, readFileSync, readdirSync, realpathSync } from 'node:fs'
import { dirname, isAbsolute, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { validateCoveragePolicy } from '../coverage/policy.mjs'

const requiredRules = ['workflow.md', 'harness.md', 'testing.md', 'coverage.json', 'i18n.md', 'code-style.md', 'documentation.md', 'documentation.json']
const requiredSkills = ['nsc-verify', 'nsc-boundary-tests', 'nsc-i18n']
export const sha256 = bytes => createHash('sha256').update(bytes).digest('hex')

function localPath(root, name) {
  if (typeof name !== 'string' || !name || isAbsolute(name) || name.includes('\\')) throw new Error(`Invalid repository path: ${name}`)
  const path = resolve(root, name)
  if (relative(root, path).startsWith('..') || !existsSync(path)) throw new Error(`Missing or escaping repository path: ${name}`)
  if (relative(realpathSync(root), realpathSync(path)).startsWith('..') || lstatSync(path).isSymbolicLink()) throw new Error(`Symlink document path: ${name}`)
  return path
}

function markdownFiles(path) {
  if (lstatSync(path).isSymbolicLink()) throw new Error(`Symlink documentation: ${path}`)
  if (!lstatSync(path).isDirectory()) return path.endsWith('.md') ? [path] : []
  return readdirSync(path).flatMap(name => markdownFiles(resolve(path, name)))
}

export function checkLinks(root, file) {
  const content = readFileSync(file, 'utf8').replace(/^```[^\n]*\n[\s\S]*?^```/gm, '')
  for (const match of content.matchAll(/!?\[[^\]\n]*\]\(([^)\n]+)\)/g)) {
    let target = match[1].replace(/^<([^>]+)>$/, '$1').split(/\s+"/)[0]
    if (/^(?:https?:|mailto:|#)/i.test(target)) continue
    target = decodeURIComponent(target.split('#')[0])
    localPath(root, relative(root, resolve(dirname(file), target)))
  }
}

export function checkContracts(root) {
  const read = name => readFileSync(localPath(root, name), 'utf8')
  for (const name of requiredRules) read(`.agents/rules/${name}`)
  if (existsSync(resolve(root, 'rules'))) throw new Error('Legacy rules directory remains')
  const entry = read('AGENTS.md')
  for (const name of requiredRules.filter(name => name.endsWith('.md'))) {
    if (!entry.includes(`.agents/rules/${name}`)) throw new Error(`AGENTS.md must link ${name}`)
  }
  const coverage = JSON.parse(read('.agents/rules/coverage.json'))
  validateCoveragePolicy(coverage)
  for (const name of requiredSkills) {
    const skill = read(`.agents/skills/${name}/SKILL.md`)
    const front = skill.match(/^---\n([\s\S]+?)\n---\n/)
    // Project skills deliberately use two plain scalar fields. Reject ambiguous
    // YAML instead of accepting a valid-looking name inside malformed metadata.
    if (!front || !new RegExp(`^name: ${name}\\ndescription: [A-Za-z][^\\n:\\[\\]{}]+$`).test(front[1]) || !skill.slice(front[0].length).trim()) throw new Error(`Invalid skill metadata: ${name}`)
    if (skill.includes('/home/') || skill.includes('$CODEX_HOME') || /TODO|\[INSERT/.test(skill)) throw new Error(`Nonportable or unfinished skill: ${name}`)
  }
  const policy = JSON.parse(read('.agents/rules/documentation.json'))
  if (policy.default_language !== 'en' || policy.localized_root !== 'internal/core/builtin') throw new Error('Invalid documentation classification')
  const documents = new Set()
  for (const kind of ['current_roots', 'historical', 'provenance', 'legal']) {
    if (!Array.isArray(policy[kind]) || !policy[kind].length) throw new Error(`Missing classification: ${kind}`)
    for (const name of policy[kind]) for (const file of markdownFiles(localPath(root, name))) documents.add(file)
  }
  const localeCodes = JSON.parse(read('internal/i18n/locales.json')).map(locale => locale.code)
  if (!localeCodes.includes('en') || new Set(localeCodes).size !== localeCodes.length) throw new Error('Invalid locale authority')
  const immutable = policy.immutable
  if (!Array.isArray(immutable) || immutable.length !== 6 || new Set(immutable.map(item => item.path)).size !== 6) throw new Error('Invalid immutable exception list')
  for (const item of immutable) {
    if (!/^internal\/core\/builtin\/(redis|mysql|postgresql|wordpress|pgadmin|phpmyadmin)\/README\.md$/.test(item.path) || !item.reason?.trim() || !/^[a-f0-9]{64}$/.test(item.sha256)) throw new Error('Invalid immutable exception')
    if (sha256(readFileSync(localPath(root, item.path))) !== item.sha256) throw new Error(`Immutable artifact changed: ${item.path}`)
    if (item.english !== `${dirname(item.path)}/docs/en/README.md`) throw new Error('Invalid English documentation destination')
    for (const locale of localeCodes) {
      for (const manual of ['README.md', 'operations.md']) {
        const file = `${dirname(item.path)}/docs/${locale}/${manual}`
        if (!read(file).trim()) throw new Error(`Empty localized document: ${file}`)
      }
    }
  }
  for (const name of policy.historical) if (!read(name).includes('Historical record')) throw new Error(`Missing historical label: ${name}`)
  for (const file of documents) checkLinks(root, file)
  return { documents: documents.size, skills: requiredSkills.length, immutable: immutable.length, locales: localeCodes }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { console.log(JSON.stringify(checkContracts(process.cwd()), null, 2)) }
  catch (error) { console.error(error.message); process.exitCode = 1 }
}
