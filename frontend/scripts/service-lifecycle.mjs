import assert from 'node:assert/strict'
import { parse, parseExpression } from '@babel/parser'
import traverseModule from '@babel/traverse'
import generateModule from '@babel/generator'

const traverse = traverseModule.default
const generate = generateModule.default

export function adaptServiceLifecycle(source, module) {
  if (module !== 'applications') return source
  const ast = parse(source, { sourceType: 'module' })
  let table = 0, empty = 0, workspace = 0, detail = 0
  traverse(ast, { CallExpression(path) {
    if (path.node.callee.name !== '__nscVueView') return
    const template = path.node.arguments[0]?.property?.value
    const values = path.node.arguments[1]
    if (template === 278) {
      assert.equal(values.type, 'ArrayExpression')
      assert.equal(values.elements.length, 15)
      values.elements[9] = parseExpression('_serviceVersionSummary(s, __viewContext.RT(s.runtimeId)?.connection === \'online\')')
      values.elements.push(parseExpression('_serviceLifecycle(s).primary'), parseExpression('_serviceLifecycle(s).secondary'))
      table++
    }
    if (template === 279) {
      assert.equal(values.type, 'ArrayExpression')
      assert.equal(values.elements.length, 1)
      values.elements[0] = parseExpression('showScope ? 8 : 7')
      empty++
    }
    if (template === 287) {
      assert.equal(values.type, 'ArrayExpression')
      assert.equal(values.elements.length, 19)
      values.elements.push(parseExpression("__viewContext.x.svcHealth === 'running' ? 'selected' : ''"))
      workspace++
    }
    if (template === 307) {
      assert.equal(values.type, 'ArrayExpression')
      assert.equal(values.elements.length, 13)
      values.elements[8] = parseExpression("_serviceRevisionLabel(s.specVersion)")
      values.elements[9] = parseExpression("__viewContext.RT(s.runtimeId)?.connection === 'online' ? _serviceRevisionLabel(s.observedVersion, 'service.version_not_confirmed') : _t('community.evidence_state.UNKNOWN')")
      values.elements.push(parseExpression('_serviceLifecycle(s).detail'))
      detail++
    }
  } })
  assert.equal(table, 1, 'applications: service table lifecycle anchor changed')
  assert.equal(empty, 1, 'applications: service empty row lifecycle anchor changed')
  assert.equal(workspace, 1, 'applications: running service filter anchor changed')
  assert.equal(detail, 1, 'applications: service detail lifecycle anchor changed')
  return "import { serviceLifecycle as _serviceLifecycle } from '../../data/service-lifecycle';\n" +
    "import { serviceRevisionLabel as _serviceRevisionLabel, serviceVersionSummary as _serviceVersionSummary } from '../../data/service-version-view';\n" +
    generate(ast, { retainLines: true }, source).code + '\n'
}

export function adaptServiceLifecycleBlueprints(blueprints) {
  const result = structuredClone(blueprints)
  const row = result[278][0]
  assert.equal(row.tag, 'tr')
  assert.equal(row.children.length, 7)
  row.children.splice(row.children.length - 1, 0, {
    tag: 'td', attrs: [['class', 'service-lifecycle']], children: [
      { text: 'NSCEXPR15END' },
      { tag: 'small', attrs: [], children: [{ text: 'NSCEXPR16END' }] },
    ],
  })
  const header = result[280][0].children[0].children[0].children[0]
  assert.equal(header.tag, 'tr')
  assert.equal(header.children.length, 7)
  header.children.splice(header.children.length - 1, 0, { tag: 'th', attrs: [], children: [{ i18n: 'service.lifecycle', variables: [] }] })
  const health = result[287][1].children[3]
  assert.equal(health.attrs[0][1], 'x-service-health')
  assert.equal(health.children.length, 3)
  health.children.splice(1, 0, {
    tag: 'option', attrs: [['value', 'running'], ['NSCEXPR19END', '']], children: [
      { i18n: 'services.health_running', variables: [] },
    ],
  })
  const meta = result[307][1].children[1]
  assert.equal(meta.attrs[0][1], 'x-detail-meta')
  assert.equal(meta.children.length, 4)
  meta.children.push({ tag: 'span', attrs: [['class', 'service-lifecycle']], children: [
    { i18n: 'service.lifecycle', variables: [] },
    { tag: 'b', attrs: [], children: [{ text: 'NSCEXPR13END' }] },
  ] })
  return result
}
