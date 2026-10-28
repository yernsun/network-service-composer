import assert from 'node:assert/strict'
import { parse, parseExpression } from '@babel/parser'
import traverseModule from '@babel/traverse'
import generateModule from '@babel/generator'
const traverse = traverseModule.default, generate = generateModule.default

// Approved, display-only additions to the original node table. Navigation,
// selection, actions and original technical-ID slots remain unchanged.
export function adaptClusterHealth(source, module) {
  if (module !== 'applications') return source
  const ast = parse(source, { sourceType: 'module' })
  let found = 0
  traverse(ast, { CallExpression(path) {
    if (path.node.callee.name !== '__nscVueView' || path.node.arguments[0]?.property?.value !== 320) return
    const values = path.node.arguments[1].elements
    assert.equal(values.length, 13)
    values[9] = parseExpression("_nodeMetric(n, 'cpu')")
    values[10] = parseExpression("_nodeMetric(n, 'memory')")
    for (const kind of ['controller', 'collector', 'gateway', 'disk']) values.push(parseExpression(`__viewContext.esc(_nodeHealth(n, '${kind}'))`))
    found++
  } })
  assert.equal(found, 1)
  return "import { nodeHealth as _nodeHealth, nodeMetric as _nodeMetric } from '../../data/node-health';\n" + generate(ast, { retainLines: true }, source).code + '\n'
}

export function adaptClusterBlueprints(templates) {
  const result = structuredClone(templates)
  const row = result[320][0]
  assert.equal(row.tag, 'tr')
  assert.equal(row.children.length, 7)
  for (let i = 13; i < 17; i++) row.children.push({ tag: 'td', attrs: [], children: [{ text: `NSCEXPR${i}END` }] })
  const header = result[321][0].children[1].children[0].children[0].children[0]
  assert.equal(header.tag, 'tr')
  assert.equal(header.children.length, 7)
  for (const key of ['controller', 'collector', 'gateway', 'disk']) header.children.push({tag:'th',attrs:[],children:[{i18n:`nodeHealth.${key}`,variables:[]}]})
  return result
}
