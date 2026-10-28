import assert from 'node:assert/strict'
import { parse, parseExpression } from '@babel/parser'
import traverseModule from '@babel/traverse'
import generateModule from '@babel/generator'
import * as t from '@babel/types'
const traverse = traverseModule.default, generate = generateModule.default

// Each approved edit names a text slot or a presentation/search function.
// ID-valued attributes and configuration/source expressions are never rewritten.
// An explicitly declared aria-label slot is presentation text for screen readers.
const viewName = id => `_serviceName(__viewContext.db.services, ${id})`
const localName = id => `_serviceName(db.services, ${id})`
const slot = (template, index, from, to, count = 1) => ({ template, index, from, to, count })
const expression = (fn, from, to, count = 1, property) => ({ fn, from, to, count, property })
export const serviceReferenceEdits = {
  domains: [
    slot(70, 22, 'sel?.service', viewName('sel?.service')),
    slot(74, 2, 'r.service', viewName('r.service')),
    slot(94, 2, 'result.route.service', viewName('result.route.service')),
    slot(122, 1, 's.id', `__viewContext.esc(${viewName('s.id')})`),
    expression('routesPage_15', "__nscVueAdd(__nscVueAdd(r.path, ' '), r.service)", "__nscVueAdd(__nscVueAdd(r.path, ' '), _serviceSearch(__viewContext.db.services, r.service))"),
  ],
  applications: [
    slot(267, 10, 's.name || s.id', viewName('s.id')),
    slot(278, 3, 's.name || s.id', viewName('s.id')),
    slot(291, 3, '__viewContext.ST(r.service)?.name || r.service', viewName('r.service')),
    slot(307, 0, 's.name || s.id', viewName('s.id'), 2),
    slot(307, 1, 's.name || s.id', viewName('s.id')),
    slot(326, 8, '__viewContext.ST(e.service)?.name || e.service', viewName('e.service')),
  ],
  infrastructure: [
    slot(428, 1, 's.name', viewName('s.id')),
    slot(431, 6, '__viewContext.W.ST(t.service)?.name', viewName('t.service')),
    slot(444, 8, 's?.name', viewName('t.service')),
    slot(448, 0, '__viewContext.W.ST(id)?.name || id', viewName('id')),
  ],
  shell: [
    slot(174, 2, 's.id', viewName('s.id')),
    expression('paletteResults_33', 's.id.includes(q)', '_serviceSearch(__viewContext.db.services, s.id).includes(q)'),
  ],
  workspace: [
    slot(137, 2, 's.id', `esc(${localName('s.id')})`),
    slot(163, 1, 's.id', `esc(${localName('s.id')})`),
    slot(226, 1, 's.name || s.id', localName('s.id')),
    slot(236, 1, 'S(id)?.name || id', localName('id')),
    { ...slot(236, 5, 'id', localName('id')), attribute: 'aria-label' },
    slot(235, 2, 'x.name || x.id', localName('x.id')),
    slot(242, 7, 'S(id)?.name || id', localName('id')),
    slot(249, 2, 's.name || s.id', localName('s.id')),
    slot(335, 10, 'e.service', localName('e.service')),
    slot(367, 0, 'ST(sid)?.name || sid', localName('sid')),
    slot(380, 0, 'ST(t.service)?.name || t.service', localName('t.service')),
    expression('serviceDetail', 'esc(s.id)', `esc(${localName('s.id')})`, 1, 'title'),
    expression('instanceDetail', 's?.name || t.service', localName('t.service'), 1, 'sub'),
    expression('opForm', 's.name', localName('s.id'), 1, 'sub'),
    expression('serviceFiltered', 's.name || s.id', localName('s.id')),
    expression('paletteResults', '(s.name || s.id).toLowerCase().includes(q)', '_serviceSearch(db.services, s.id).includes(q)'),
    expression('summarizeChanges', 'r.service || r.value', `${localName('r.service')} || r.value`, 1, 'detail'),
    // These are formatted summary values, after ID-based change comparison.
    expression('summarizeChanges', "k === 'snippet' ? '原扩展' : k === 'timeout' || k === 'connect' ? __nscVueAdd(old[k], 's') : typeof old[k] === 'boolean' ? old[k] ? '开启' : '关闭' : old[k]", `k === 'service' ? ${localName('old[k]')} : (k === 'snippet' ? '原扩展' : k === 'timeout' || k === 'connect' ? __nscVueAdd(old[k], 's') : typeof old[k] === 'boolean' ? old[k] ? '开启' : '关闭' : old[k])`, 1, 'old'),
    expression('summarizeChanges', "k === 'snippet' ? '新扩展' : k === 'timeout' || k === 'connect' ? __nscVueAdd(r[k], 's') : typeof r[k] === 'boolean' ? r[k] ? '开启' : '关闭' : r[k]", `k === 'service' ? ${localName('r[k]')} : (k === 'snippet' ? '新扩展' : k === 'timeout' || k === 'connect' ? __nscVueAdd(r[k], 's') : typeof r[k] === 'boolean' ? r[k] ? '开启' : '关闭' : r[k])`, 1, 'new'),
  ],
}

function functionName(path) {
  const fn = path.findParent(p => p.isFunctionDeclaration() || p.isFunctionExpression())
  if (fn?.isFunctionDeclaration()) return fn.node.id.name
  return fn?.parentPath.isAssignmentExpression() ? fn.parentPath.node.left.name : undefined
}

export function adaptServiceReferences(source, module) {
  const rules = serviceReferenceEdits[module]
  if (!rules) return source
  const ast = parse(source, { sourceType: 'module' })
  for (const rule of rules) {
    const from = parseExpression(rule.from), to = parseExpression(rule.to)
    let count = 0
    function replace(path) {
      if (t.isNodesEquivalent(path.node, from)) {
        path.replaceWith(t.cloneNode(to, true)); count++; path.skip()
      }
    }
    if (rule.template !== undefined) {
      traverse(ast, { CallExpression(path) {
        if (path.node.callee.name !== '__nscVueView' || path.node.arguments[0]?.property?.value !== rule.template) return
        const value = path.get(`arguments.1.elements.${rule.index}`)
        replace(value)
        if (!t.isNodesEquivalent(value.node, to)) value.traverse({ enter: replace })
      } })
    } else {
      traverse(ast, { enter(path) {
        if (!t.isNodesEquivalent(path.node, from) || functionName(path) !== rule.fn) return
        if (rule.property) {
          const field = path.findParent(p => p.isObjectProperty())
          if (field?.node.key.name !== rule.property) return
        }
        replace(path)
      } })
    }
    assert.equal(count, rule.count, `${module}: service reference edit drifted: ${JSON.stringify(rule)}`)
  }
  const prefix = module === 'workspace' ? '../' : '../../'
  const helpers = ['serviceName as _serviceName', ...(rules.some(r => r.to.includes('_serviceSearch')) ? ['serviceSearch as _serviceSearch'] : [])]
  return `import { ${helpers.join(', ')} } from '${prefix}data/service-references';\n` + generate(ast, { retainLines: true, comments: true }, source).code + '\n'
}
