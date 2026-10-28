import assert from 'node:assert/strict'
import { parse } from '@babel/parser'
import traverseModule from '@babel/traverse'
import generateModule from '@babel/generator'
import * as t from '@babel/types'

const traverse = traverseModule.default
const generate = generateModule.default

const metricRules = {
  domains: {
    cloudOverview_6: ['data-action="tab" data-tab="routes"', 'data-action="tab" data-tab="routes"', 'data-action="nav" data-page="gateways"', 'data-action="impact"'],
    opsOverview_10: ['data-action="diagnose"', 'data-action="tab" data-tab="tls"', 'data-action="nav" data-page="gateways"', 'data-action="tab" data-tab="config"', 'data-action="nav" data-page="services"'],
    workspaceOverview_23: ['data-action="nav" data-page="domains"', 'data-action="nav" data-page="routes"', 'data-action="nav" data-page="services"', 'data-action="nav" data-page="gateways"'],
  },
  projects: {
    projectPage_41: ['data-action="nav" data-page="environments"', 'data-action="nav" data-page="domains"', 'data-action="nav" data-page="services"', 'data-action="nav" data-page="domains"'],
    environmentOverview_44: ['data-action="nav" data-page="domains"', 'data-action="nav" data-page="services"', 'data-action="nav" data-page="routes"', 'data-action="nav" data-page="domains"'],
  },
  applications: {
    applicationsPage_50: ['data-action="x-nav" data-page="applications"', 'data-action="x-nav" data-page="services"', 'data-action="x-nav" data-page="services" data-health="running"', 'data-action="x-nav" data-page="services" data-health="issue"'],
    applicationPage_55: ['data-action="x-app-tab" data-id="overview"', 'data-action="x-app-tab" data-id="overview"', 'data-action="x-app-tab" data-id="overview"', 'data-action="x-app-tab" data-id="events"'],
    runtimePage_59: ['data-action="x-runtime-tab" data-id="nodes"', 'data-action="x-runtime-tab" data-id="services"', 'data-action="x-runtime-tab" data-id="overview"', 'data-action="x-runtime-tab" data-id="services"'],
  },
}

const primaryActions = new Map([
  [122, 'service-detail'],
  [124, 'gateway-detail'],
  [193, 'p-env'],
  [196, 'p-project'],
  [267, 'x-app'],
  [271, 'x-template-info'],
  [316, 'x-runtime'],
])

export function adaptActionableNavigation(source) {
  const replacements = [
    [
      "        if (x.svcHealth === 'issue' && !['issue', 'unknown'].includes(st.key)) return false;\n        if (x.svcHealth === 'external' && s.managed) return false;",
      "        if (x.svcHealth === 'issue' && !['issue', 'unknown'].includes(st.key)) return false;\n        if (x.svcHealth === 'running' && !['healthy', 'running', 'complete'].includes(st.key)) return false;\n        if (x.svcHealth === 'external' && s.managed) return false;",
    ],
    [
      "        case 'x-nav':\n          jump(el.dataset.page);",
      "        case 'x-nav':\n          if (el.dataset.page === 'services') {\n            x.svcHealth = el.dataset.health || 'all';\n            x.svcPage = 1;\n          }\n          jump(el.dataset.page);",
    ],
    [
      "W.x.svcHealth = tab('health', ['all', 'issue', 'external'], 'all');",
      "W.x.svcHealth = tab('health', ['all', 'running', 'issue', 'external'], 'all');",
    ],
  ]
  let result = source
  for (const [before, after] of replacements) {
    assert.equal(result.split(before).length, 2, `actionable navigation anchor changed: ${before.slice(0, 48)}`)
    result = result.replace(before, after)
  }
  return result
}

function functionName(path) {
  return path.findParent(parent => parent.isFunctionDeclaration())?.node.id?.name || ''
}

function appendAttribute(call, index) {
  while (call.arguments.length <= index) call.arguments.push(t.stringLiteral(''))
  const current = call.arguments[index]
  if (t.isStringLiteral(current)) current.value = `${current.value} data-card-primary`.trim()
  else call.arguments[index] = t.callExpression(t.identifier('__nscVueAdd'), [current, t.stringLiteral(' data-card-primary')])
}

export function adaptActionableCards(source, module) {
  const rules = metricRules[module] || {}
  const ast = parse(source, { sourceType: 'module' })
  const counts = new Map()
  const primary = new Map([...primaryActions].map(([template]) => [template, 0]))
  let helper = 0
  traverse(ast, {
    FunctionDeclaration(path) {
      const name = path.node.id?.name
      const template = name === 'metric_5' ? 67 : name === 'health_11' ? 87 : name === 'xMetric_45' ? 255 : null
      if (template === null) return
      path.node.params.push(t.assignmentPattern(t.identifier('action'), t.stringLiteral('')))
      let found = 0
      path.traverse({ CallExpression(child) {
        if (child.node.callee.name !== '__nscVueView' || child.node.arguments[0]?.property?.value !== template) return
        const values = child.node.arguments[1]
        assert.equal(values.type, 'ArrayExpression')
        values.elements.push(t.identifier('action'))
        found++
      } })
      assert.equal(found, 1, `${module}: actionable metric helper ${name} changed`)
      helper++
    },
    CallExpression(path) {
      const callee = path.node.callee?.property?.name
      const fn = functionName(path)
      if (['metric', 'health', 'xMetric'].includes(callee) && rules[fn]) {
        const index = counts.get(fn) || 0
        const actions = rules[fn]
        assert.ok(index < actions.length, `${module}: too many metric calls in ${fn}`)
        const actionIndex = callee === 'health' ? 5 : 4
        while (path.node.arguments.length < actionIndex) path.node.arguments.push(t.stringLiteral(''))
        path.node.arguments.push(t.stringLiteral(actions[index]))
        counts.set(fn, index + 1)
      }
      if (path.node.callee.name !== '__nscVueView') return
      const template = path.node.arguments[0]?.property?.value
      const action = primaryActions.get(template)
      if (!action) return
      let found = 0
      path.get('arguments.1').traverse({ CallExpression(child) {
        const helperName = child.node.callee?.property?.name
        if (!['B', 'TB'].includes(helperName) || child.node.arguments[1]?.value !== action) return
        appendAttribute(child.node, helperName === 'B' ? 4 : 3)
        found++
      } })
      assert.equal(found, 1, `${module}: primary action ${action} for card ${template} changed`)
      primary.set(template, (primary.get(template) || 0) + 1)
    },
  })
  for (const [fn, actions] of Object.entries(rules)) assert.equal(counts.get(fn), actions.length, `${module}: actionable metrics in ${fn} changed`)
  if (module === 'domains') assert.equal(helper, 2)
  if (module === 'applications') assert.equal(helper, 1)
  if (module === 'projects') assert.equal(helper, 0)
  for (const [template, count] of primary) {
    const belongs = (module === 'domains' && [122, 124].includes(template)) || (module === 'projects' && [193, 196].includes(template)) || (module === 'applications' && [267, 271, 316].includes(template))
    if (belongs) assert.equal(count, 1, `${module}: actionable card ${template} changed`)
  }
  return generate(ast, { retainLines: true }, source).code + '\n'
}

function addClass(node, className) {
  const entry = node.attrs.find(([name]) => name === 'class')
  assert.ok(entry)
  entry[1] = `${entry[1]} ${className}`
}

function spread(node, index) {
  node.attrs.push([`NSCEXPR${index}END`, ''])
}

export function adaptActionableCardBlueprints(blueprints) {
  const result = structuredClone(blueprints)
  for (const [template, index] of [[67, 4], [87, 6], [255, 4]]) {
    const root = result[template][0]
    assert.equal(root.tag, 'div')
    root.tag = 'button'
    addClass(root, 'actionable-card')
    spread(root, index)
  }
  for (const template of primaryActions.keys()) {
    if (!result[template]) continue
    addClass(result[template][0], 'actionable-card')
  }
  return result
}
