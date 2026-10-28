import { adaptBrand } from './brand.mjs'
import { adaptSystemApplications } from './system-applications.mjs'
import { adaptClusterHealth, adaptClusterBlueprints } from './cluster-health.mjs'
import { readFileSync, writeFileSync, readdirSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { parse } from '@babel/parser'
import traverseModule from '@babel/traverse'
import generateModule from '@babel/generator'
import * as t from '@babel/types'
import { adaptServiceReferences } from './service-references.mjs'
import { adaptProjectLifecycle, adaptProjectRuntime } from './project-lifecycle.mjs'
import { adaptHistoryViews } from './service-history.mjs'
import { adaptInstanceLifecycle, adaptInstanceLifecycleBlueprints } from './instance-lifecycle.mjs'
import { adaptEmptyProjectScope, adaptScopeHeading } from './project-scope.mjs'
import { adaptServiceLifecycle, adaptServiceLifecycleBlueprints } from './service-lifecycle.mjs'
import { adaptActionableCards, adaptActionableCardBlueprints, adaptActionableNavigation } from './actionable-cards.mjs'
const generate = generateModule.default
const traverse = traverseModule.default
const base = new URL('../', import.meta.url)
const clusterOnly = process.argv.includes('--cluster-health')
const read = path => {
  const source = readFileSync(new URL(path, base), 'utf8')
  return path.startsWith('reference/') ? adaptBrand(source) : source
}
const write = (path, text) => writeFileSync(new URL(path, base), text)
const locales = JSON.parse(read('../internal/i18n/locales.json')).map(l => l.code)
const catalogs = Object.fromEntries(locales.map(l => [l, JSON.parse(read(`src/shared/i18n/locales/${l}.json`))]))
const known = new Map(Object.entries(catalogs['zh-CN']).filter(([k]) => !k.startsWith('projectLifecycle.')).map(([k, v]) => [v, k]))
const english = {}
for (const file of ['src/i18n/en.ts', 'src/i18n/en-details.ts']) {
  const ast = parse(read(file), { sourceType: 'module', plugins: ['typescript'] })
  traverse(ast, { ObjectProperty(p) { if (p.node.key.type === 'StringLiteral' && p.node.value.type === 'StringLiteral') english[p.node.key.value] = p.node.value.value } })
}
const messages = {}
function key(text) {
  if (known.has(text)) {
    const found = known.get(text)
    if (found.startsWith('archive.')) messages[found] = text
    return found
  }
  const id = `archive.message_${createHash('sha256').update(text).digest('hex').slice(0, 12)}`
  messages[id] = text
  return id
}
const cjk = text => /[\u3400-\u9fff]/.test(text)
const preserveFunctions = new Set(['seed', 'seedV10', 'seedWorkloads', 'ensureDoc', 'renderConfig', 'previewWizard', 'specText', 'newInstance', 'appendEvent', 'runAttempt', 'simulateDeployment', 'initializeImportedApp'])
function transform(source, prefix) {
  const labelMaps = new Set(['MATCH', 'KINDS', 'PAGE_NAMES', 'KIND_NAMES', 'MODE', 'statusCN', 'typelabel'])
  let usesLabels = false, usesAttribute = false, usesModal = false, usesVersion = false
  const ast = parse(source, { sourceType: 'module' })
  const protectedPath = p => p.findParent(a => a.isFunctionDeclaration() && preserveFunctions.has(a.node.id?.name)) || p.findParent(a => a.isVariableDeclarator() && ['TEMPLATES', 'SAMPLE_COMPOSE'].includes(a.node.id?.name))
  traverse(ast, {
    StringLiteral(p) {
      const value = p.node.value
      if (!cjk(value) || value.includes('\n') || value.includes('NSCEXPR') || protectedPath(p)) return
      if (p.parentPath.isObjectProperty() && p.key === 'key') return
      // Attribute snippets are syntax, not translatable sentences. Localize only
      // the quoted presentation value and escape it before the archive parses it.
      if (/\b[\w-]+=/.test(value)) {
        const attribute = /\b(?:title|aria-label|placeholder)=(['"])(.*?)\1/g
        const found = [...value.matchAll(attribute)].find(m => cjk(m[2]))
        if (found) {
          const start = found.index + found[0].indexOf(found[1]) + 1
          const prefix = value.slice(0, start), suffix = value.slice(start + found[2].length)
          usesAttribute = true
          p.replaceWith(t.callExpression(t.identifier('_a'), [t.stringLiteral(prefix), t.stringLiteral(key(found[2])), t.stringLiteral(suffix)]))
          p.skip(); return
        }
        // Code and action data embedded in attributes must remain literal.
        if (/^(?:disabled|checked|selected|data-|class=)/.test(value)) return
      }
      if (p.parentPath.isImportDeclaration() || p.parentPath.isExportDeclaration()) return
      const object = p.parentPath.isObjectProperty() ? p.parentPath.parentPath : null
      const constantLabel = object?.parentPath.isVariableDeclarator() && labelMaps.has(object.parentPath.node.id.name)
      const assignedLabel = object?.parentPath.isCallExpression() && object.parentPath.node.callee.object?.name === 'Object' && object.parentPath.node.callee.property?.name === 'assign' && object.parentPath.node.arguments[0]?.name === 'PAGE_NAMES'
      const call = t.callExpression(t.identifier('_t'), [t.stringLiteral(key(value))])
      p.replaceWith(constantLabel || assignedLabel ? t.arrowFunctionExpression([], call) : call)
      p.skip()
    },
    TemplateLiteral: { exit(p) {
      if (protectedPath(p) || p.parentPath.isTaggedTemplateExpression()) return
      // Multiline source files, example configuration and logs remain literal.
      if (p.node.quasis.some(q => q.value.cooked.includes('\n')) || !p.node.quasis.some(q => cjk(q.value.cooked))) return
      const text = p.node.quasis.map((q, i) => q.value.cooked + (i < p.node.expressions.length ? `{v${i}}` : '')).join('')
      const params = t.objectExpression(p.node.expressions.map((expression, i) => t.objectProperty(t.identifier(`v${i}`), expression)))
      p.replaceWith(t.callExpression(t.identifier('_t'), [t.stringLiteral(key(text)), params]))
      p.skip()
    } },
    VariableDeclarator: { exit(p) {
      if (!labelMaps.has(p.node.id.name) || p.node.init?.type !== 'ObjectExpression') return
      usesLabels = true
      p.node.init = t.callExpression(t.identifier('_labels'), [p.node.init])
    } },
    CallExpression: { exit(p) {
      const callee = p.node.callee
      const [prefix, value] = p.node.arguments
      if (callee.name === '__nscVueAdd' && prefix?.value === 'v' &&
          value?.type === 'MemberExpression' && value.object?.property?.name === 'loaded') {
        usesVersion = true
        p.replaceWith(t.callExpression(t.identifier('_observedVersion'), [value]))
        return
      }
      if (callee.name !== 'showModal' && callee.property?.name !== 'showModal') return
      const argument = p.get('arguments.0')
      if (!argument?.isObjectExpression()) return
      // Capture the previous modal, as the original expression does. Never
      // generate new IDs during a language change.
      let unsafe = false, previousModal = false
      argument.traverse({ ReferencedIdentifier(ref) {
        if (ref.node.name === 'uid') unsafe = true
        if (ref.node.name === 'modal') previousModal = true
      } })
      if (unsafe) return
      usesModal = true
      p.node.arguments[0] = t.callExpression(t.identifier('_modal'), [
        t.arrowFunctionExpression(previousModal ? [t.identifier('modal')] : [], p.node.arguments[0]),
        ...(previousModal ? [t.identifier('modal')] : []),
      ])
    } },
  })
  return `import { translate as _t${usesLabels ? ', labels as _labels' : ''}${usesAttribute ? ', translatedAttribute as _a' : ''}${usesModal ? ', localizeModal as _modal' : ''}${usesVersion ? ', observedVersion as _observedVersion' : ''} } from '${prefix}i18n';\n` + generate(ast, { retainLines: true, comments: true }, source).code + '\n'
}
for (const module of readdirSync(new URL('reference/views/', base), { withFileTypes: true }).filter(e => e.isDirectory() && (!clusterOnly || e.name === 'applications'))) {
  const adapted = adaptActionableCards(adaptServiceLifecycle(adaptHistoryViews(adaptClusterHealth(adaptProjectLifecycle(adaptServiceReferences(adaptSystemApplications(adaptScopeHeading(read(`reference/views/${module.name}/renderers.js`), module.name), module.name), module.name), module.name), module.name), module.name), module.name), module.name)
  write(`src/views/${module.name}/renderers.js`, transform(adaptInstanceLifecycle(adapted, module.name), '../../'))
}
if (!clusterOnly) {
write('src/mock/compiler.js', transform(read('reference/compiler.js'), '../'))
write('src/mock/service.js', transform(adaptProjectRuntime(adaptServiceReferences(adaptEmptyProjectScope(read('reference/demo.js')), 'workspace'), true), '../'))
const real = read('src/runtime/real.js')
if (!real.includes('translate as _t')) write('src/runtime/real.js', transform(adaptActionableNavigation(real.includes('serviceName as _serviceName') ? real : adaptServiceReferences(real, 'workspace')), '../'))
}
const templates = adaptActionableCardBlueprints(adaptServiceLifecycleBlueprints(adaptInstanceLifecycleBlueprints(adaptClusterBlueprints(JSON.parse(read('reference/views/blueprints.json'))))))
function blueprint(node, protectedContent = false) {
  if (typeof node === 'string') {
    if (protectedContent || !cjk(node)) return node
    const variables = [...node.matchAll(/NSCEXPR(\d+)END/g)].map(m => Number(m[1]))
    return { i18n: key(node.replace(/NSCEXPR(\d+)END/g, (_, n) => `{v${n}}`)), variables }
  }
  if ('i18n' in node) return node
  if ('text' in node) return blueprint(node.text, protectedContent)
  const literal = protectedContent || ['pre', 'code', 'textarea'].includes(node.tag)
  node.attrs = node.attrs.map(([name, value]) => ['title', 'aria-label', 'placeholder'].includes(name) && cjk(value) ? [name, `@i18n:${key(value)}`] : [name, value])
  node.children = node.children.map(child => blueprint(child, literal))
  return node
}
write('src/views/blueprints.json', JSON.stringify(templates.map(t => t.map ? t.map(n => blueprint(n)) : blueprint(t))))
const pending = []
for (const [id, source] of Object.entries(messages)) {
  catalogs['zh-CN'][id] = source
  for (const locale of locales.filter(l => l !== 'zh-CN')) {
    if (!catalogs[locale][id]) {
      catalogs[locale][id] = locale === 'en' ? english[source] || source : source

    }
    if (catalogs[locale][id] === source) pending.push({ locale, key: id, source, english: english[source] || '' })
  }
}
for (const locale of locales) write(`src/shared/i18n/locales/${locale}.json`, JSON.stringify(catalogs[locale], null, 2) + '\n')
if (!clusterOnly) {
write('reference/localization-source.json', JSON.stringify(messages, null, 2) + '\n')
write('reference/localization-pending.json', JSON.stringify(pending, null, 2) + '\n')
}
console.log(`Localized ${Object.keys(messages).length} original messages; ${pending.length} translation entries require review`)
