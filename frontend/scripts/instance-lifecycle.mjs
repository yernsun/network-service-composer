import assert from 'node:assert/strict'
import { parse, parseExpression } from '@babel/parser'
import traverseModule from '@babel/traverse'
import generateModule from '@babel/generator'

const traverse = traverseModule.default
const generate = generateModule.default

// Add Docker lifecycle evidence to the immutable archive's instance tables.
// Task metadata and Container state remain separate in the data adapter.
export function adaptInstanceLifecycle(source, module) {
	const anchors = module === 'applications'
		? [{ template: 308, primary: 13 }]
		: module === 'infrastructure'
			? [{ template: 431, primary: 9 }, { template: 444, primary: 9 }]
			: []
	if (!anchors.length) return source
	const ast = parse(source, { sourceType: 'module' })
	const found = new Map(anchors.map(anchor => [anchor.template, 0]))
	traverse(ast, { CallExpression(path) {
		const template = path.node.arguments[0]?.property?.value
		const anchor = anchors.find(value => value.template === template)
		if (path.node.callee.name !== '__nscVueView' || !anchor) return
		const values = path.node.arguments[1]
		assert.equal(values.type, 'ArrayExpression')
		values.elements[anchor.primary] = parseExpression('_instanceTimeRows(t).primary')
		values.elements[anchor.primary + 1] = parseExpression('_instanceTimeRows(t).secondary')
		found.set(template, found.get(template) + 1)
	} })
	for (const anchor of anchors) assert.equal(found.get(anchor.template), 1, `${module}: lifecycle anchor ${anchor.template} changed`)
  let result = generate(ast, { retainLines: true }, source).code + '\n'
  const observationImport = "import { observationUnknown as _observationUnknown } from '../../data/service-instances';"
  if (result.includes(observationImport)) {
    result = result.replace(observationImport, "import { observationUnknown as _observationUnknown, instanceTimeRows as _instanceTimeRows } from '../../data/service-instances';")
  } else {
    result = "import { instanceTimeRows as _instanceTimeRows } from '../../data/service-instances';\n" + result
  }
	return result
}

function replaceLiteral(node, literal, key) {
	let found = 0
	if (Array.isArray(node)) {
		for (let index = 0; index < node.length; index++) {
			if (node[index] === literal) {
				node[index] = { i18n: key, variables: [] }
				found++
			} else found += replaceLiteral(node[index], literal, key)
		}
	} else if (node && typeof node === 'object') {
		if (node.text === literal) {
			delete node.text
			node.i18n = key
			node.variables = []
			return 1
		}
		for (const [name, value] of Object.entries(node)) {
			if (value === literal) {
				node[name] = { i18n: key, variables: [] }
				found++
			} else found += replaceLiteral(value, literal, key)
		}
	}
	return found
}

export function adaptInstanceLifecycleBlueprints(blueprints) {
	const result = structuredClone(blueprints)
	for (const [template, literal] of [[310, '启动 / 结束时间'], [433, '启动 / 结束'], [444, '开始 / 结束时间']]) {
		assert.equal(replaceLiteral(result[template], literal, 'cluster.lifecycle_timestamps'), 1, `template ${template}: lifecycle heading changed`)
	}
	return result
}
