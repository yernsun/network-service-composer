import { adaptActivityNavigation } from './activity-navigation.mjs'
import { adaptInstallationNavigation } from './installation-navigation.mjs'
import { adaptHANavigation } from './ha-navigation.mjs'
import { adaptBrand } from './brand.mjs'
import { adaptHistoryNavigation, adaptHistoryViews } from './service-history.mjs'
import { adaptSystemApplications } from './system-applications.mjs'
import { adaptClusterHealth, adaptClusterBlueprints } from './cluster-health.mjs'
import { readFileSync, existsSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { parse } from '@babel/parser'
import traverseModule from '@babel/traverse'
import generateModule from '@babel/generator'
import assert from 'node:assert/strict'
import * as t from '@babel/types'
import { adaptServiceReferences } from './service-references.mjs'
import { adaptProjectLifecycle, adaptProjectRuntime } from './project-lifecycle.mjs'
import { adaptComposeWizard } from './compose-wizard.mjs'
import { adaptInstanceLifecycle, adaptInstanceLifecycleBlueprints } from './instance-lifecycle.mjs'
import { adaptEmptyProjectScope, adaptScopeHeading } from './project-scope.mjs'
import { adaptServiceLifecycle, adaptServiceLifecycleBlueprints } from './service-lifecycle.mjs'
import { adaptActionableCards, adaptActionableCardBlueprints, adaptActionableNavigation } from './actionable-cards.mjs'
const traverse = traverseModule.default, generate = generateModule.default
const base = new URL('../', import.meta.url)
const read = path => {
  const source = readFileSync(new URL(path, base), 'utf8')
  return path.startsWith('reference/') ? adaptBrand(source) : source
}
const chinese = { ...JSON.parse(read('../internal/i18n/catalogs/zh-CN.json')), ...JSON.parse(read('src/shared/i18n/locales/zh-CN.json')) }
// The project form is a Vue component; retain the archive check for the
// navigation, recovery and remaining original interaction implementations.
const frozen = new Set(['selectScope', 'switchScope', 'navigate', 'jump', 'routePath', 'parse', 'setScope', 'environmentHref', 'environmentAction', 'projectHref', 'scopeListPage', 'concreteScopeHref', 'linkFor', 'guard', 'editorKey', 'collectWizard', 'nextWizard', 'renderWizard', 'isUnsaved', 'modalFields', 'restoreFields'])
function astFor(source, typescript = false) {
  const ast = parse(source, { sourceType: 'module', plugins: typescript ? ['typescript'] : [] })
  traverse(ast, { StringLiteral(path) { delete path.node.extra }, DirectiveLiteral(path) { delete path.node.extra }, CallExpression: { exit(path) {
    if (path.node.callee.name === '_observedVersion') {
      const value = path.node.arguments[0]
      assert.equal(value.type, 'MemberExpression')
      assert.equal(value.object.property.name, 'loaded')
      path.replaceWith(t.callExpression(t.identifier('__nscVueAdd'), [t.stringLiteral('v'), value]))
      return
    }
    if (path.node.callee.name === '_modal') {
      assert.equal(path.node.arguments[0].type, 'ArrowFunctionExpression')
      assert.equal(path.node.arguments[0].body.type, 'ObjectExpression')
      path.replaceWith(path.node.arguments[0].body)
      return
    }
    if (path.node.callee.name === '_a') {
      const [prefix, key, suffix] = path.node.arguments
      assert.ok(key.value in chinese)
      path.replaceWith(t.stringLiteral(prefix.value + chinese[key.value] + suffix.value))
      return
    }
    if (path.node.callee.name === '_t' && path.node.arguments[0]?.type === 'StringLiteral') {
      const key = path.node.arguments[0].value
      assert.ok(key in chinese, `Unknown source translation: ${key}`)
      if (path.node.arguments.length === 1) path.replaceWith(t.stringLiteral(chinese[key]))
      else {
        const pieces = chinese[key].split(/\{v\d+\}/g)
        const expressions = path.node.arguments[1].properties.map(p => p.value)
        assert.equal(pieces.length, expressions.length + 1)
        const quasis = pieces.map((cooked, i) => t.templateElement({ cooked, raw: cooked.replace(/\\/g, '\\\\').replace(/`/g, '\\`').replace(/\$\{/g, '\\${') }, i === pieces.length - 1))
        path.replaceWith(t.templateLiteral(quasis, expressions))
      }
    }
  } } })
  return ast
}
function functions(source) {
  const result = []
  traverse(astFor(source), {
    FunctionDeclaration(path) { if (frozen.has(path.node.id.name)) result.push([path.node.id.name, generate(path.node, { comments: false, compact: true }).code]) },
    AssignmentExpression(path) { if (frozen.has(path.node.left.name) && path.node.right.type === 'FunctionExpression') result.push([path.node.left.name, generate(path.node.right, { comments: false, compact: true }).code]) },
  })
  return result
}
const originalSource = adaptHistoryNavigation(adaptProjectRuntime(adaptEmptyProjectScope(read('reference/workspace.js'))))
const original = functions(originalSource)
const hash = text => createHash('sha256').update(text).digest('hex')
function adaptServiceLogTabs(source) {
  const tabs = "['instances', 'events', 'config', 'routes']"
  assert.equal(source.split(tabs).length, 5, 'Service tab reference drifted')
  return source.replaceAll(tabs, "['instances', 'events', 'logs', 'config', 'routes']")
}
function adaptDockerResourceTabs(source) {
  const tabs = "['overview', 'nodes', 'services', 'events']"
  assert.equal(source.split(tabs).length, 3, 'Runtime tab reference drifted')
  return source.replaceAll(tabs, "['overview', 'nodes', 'services', 'events', 'resources']")
}
function adaptServiceLogView(source, module) {
  if (module !== 'applications') return source
  const events = "  if (__viewContext.x.serviceTab === 'events') body = __viewContext.eventsTable(__viewContext.db.runtimeEvents.filter(e => e.service === s.id));"
  const tab = "['events', '退出与恢复记录', 'history']"
  assert.equal(source.split(events).length, 2, 'Service event renderer reference drifted')
  assert.equal(source.split(tab).length, 2, 'Service event tab reference drifted')
  return source.replace(events, `${events}\n  if (__viewContext.x.serviceTab === 'logs') body = __viewContext.serviceLogsPage(s.id, __viewContext.RT(s.runtimeId)?.type);`)
    .replace(tab, `${tab}, ['logs', '日志', 'terminal']`)
}
function adaptDockerResourceView(source, module) {
  if (module !== 'applications') return source
  const tab = "['events', '事件与采集', 'activity']"
  assert.equal(source.split(tab).length, 2, 'Runtime tab renderer reference drifted')
  return source.replace(tab, `${tab}, ['resources', '${chinese['dockerResources.title']}', 'box']`)
}
for (const file of ['src/mock/service.js', 'src/runtime/real.js']) {
  const actual = functions(read(file))
  assert.equal(actual.length, original.length)
  const expected = file === 'src/runtime/real.js' ? functions(adaptInstallationNavigation(adaptDockerResourceTabs(adaptServiceLogTabs(adaptActionableNavigation(adaptComposeWizard(adaptActivityNavigation(adaptHANavigation(originalSource)))))))) : original
  for (const [index, [name, code]] of expected.entries()) assert.equal(hash(actual[index][1]), hash(code), `${file}: ${name} interaction changed`)
}
function connect(source) {
  let result
  traverse(astFor(source, true), { FunctionDeclaration(path) { if (path.node.id.name === 'connectRouter') result = generate(path.node, { comments: false, compact: true }).code } })
  return result
}
assert.equal(connect(read('src/router/index.ts')), connect(read('reference/router.ts')))
for (const module of ['applications', 'domains', 'infrastructure', 'projects', 'shell', 'variables']) {
  const ast = astFor(read(`src/views/${module}/renderers.js`))
  ast.program.body = ast.program.body.filter(node => !(node.type === 'ImportDeclaration' && node.source.value.endsWith('/i18n')))
  const adapted = adaptActionableCards(adaptServiceLifecycle(adaptHistoryViews(adaptClusterHealth(adaptProjectLifecycle(adaptServiceReferences(adaptSystemApplications(adaptScopeHeading(adaptDockerResourceView(adaptServiceLogView(read(`reference/views/${module}/renderers.js`), module), module), module), module), module), module), module), module), module), module)
  assert.equal(generate(ast, { comments: false, compact: true }).code, generate(astFor(adaptInstanceLifecycle(adapted, module)), { comments: false, compact: true }).code, `${module}: renderer changed outside approved service reference bindings, lifecycle evidence and project settings entry`)
}
const compiler = astFor(read('src/mock/compiler.js'))
compiler.program.body = compiler.program.body.filter(node => !(node.type === 'ImportDeclaration' && node.source.value.endsWith('/i18n')))
assert.equal(generate(compiler, { comments: false, compact: true }).code, generate(astFor(read('reference/compiler.js')), { comments: false, compact: true }).code, 'Original parser behavior changed')
const actions = JSON.parse(read('reference/action-inventory.json')).actions
const capabilities = JSON.parse(read('reference/action-capabilities.json'))
assert.equal(capabilities.length, 174)
assert.deepEqual(capabilities.map(row => row.action).sort(), actions.map(row => typeof row === 'string' ? row : row.action).sort())
function normalize(value) {
  if (Array.isArray(value)) return value.map(normalize)
  if (value && typeof value === 'object') {
    if ('i18n' in value) return chinese[value.i18n].replace(/\{v(\d+)\}/g, (_, n) => `NSCEXPR${n}END`)
    if ('text' in value) return value.text
    return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, normalize(v)]))
  }
  if (typeof value === 'string' && value.startsWith('@i18n:')) return chinese[value.slice(6)]
  return value
}
assert.deepEqual(normalize(JSON.parse(read('src/views/blueprints.json'))), normalize(adaptActionableCardBlueprints(adaptServiceLifecycleBlueprints(adaptInstanceLifecycleBlueprints(adaptClusterBlueprints(JSON.parse(read('reference/views/blueprints.json'))))))))
const archive = new URL('../nginx-service-composer.tar.gz', base)
if (existsSync(archive)) assert.equal(createHash('sha256').update(readFileSync(archive)).digest('hex'), '4e848bbe5ee4e643481906ca05bddcbf54251a853ba5ee913a2290fb414b0d55')
console.log(`Archive preserved with the explicit empty-project scope adaptation: ${original.length} interaction implementations, router, 6 renderer modules and 457 templates`)
