import { test, beforeEach } from 'vitest'
import { setLocale } from '../../src/shared/i18n'
beforeEach(() => setLocale('zh-CN'))
import assert from 'node:assert/strict'
import compiler from '../../src/mock/compiler.js'

test('变量默认值、空值与转义规则保持一致', () => {
  assert.equal(compiler.interpolate('${PORT:-8080}', {}), '8080')
  assert.equal(compiler.interpolate('${PORT:-8080}', { PORT: { value: '3000' } }), '3000')
  assert.equal(compiler.interpolate('$$HOME', {}), '$HOME')
  assert.throws(() => compiler.interpolate('${MISSING:?必须设置}', {}), /必须设置/)
})
test('凭据引用不会展开到 YAML', () => {
  assert.throws(() => compiler.interpolate('${DB_SECRET}', { DB_SECRET: { value: 'secret://db/password@v1', type: 'credential' } }), /凭据引用/)
})
test('.env 导入保留字面量，拒绝重复键及 Shell 展开', () => {
  const result = compiler.parseEnv('PORT=8080\nNAME="portal web"\n# comment')
  assert.deepEqual({ ...result.rows }, { PORT: '8080', NAME: 'portal web' })
  assert.deepEqual(result.errors, [])
  assert.ok(compiler.parseEnv('PORT=1\nPORT=2').errors.length)
  assert.ok(compiler.parseEnv('A=$(command)').errors.length)
  assert.ok(compiler.parseEnv('A=${B}').errors.length)
})
test('YAML 拒绝重复键和原型字段', () => {
  assert.throws(() => compiler.parseYAML('services:\n  web:\n    image: nginx:1\n    image: nginx:2'))
  assert.throws(() => compiler.parseYAML('services:\n  __proto__:\n    image: nginx:1'))
})
test('编排渲染冻结变量，并正确合并容器环境变量', () => {
  const result = compiler.compile('services:\n  web:\n    image: nginx:${TAG}\n    env_file: runtime.env\n    environment:\n      MODE: production\n    deploy:\n      replicas: ${REPLICAS}', { TAG: { value: '1.27' }, REPLICAS: { value: '2' } }, { target: 'stack', envFiles: { 'runtime.env': 'MODE=testing\nPORT=8080' } })
  assert.deepEqual(result.errors, [])
  assert.equal(result.tree.services.web.image, 'nginx:1.27')
  assert.equal(result.tree.services.web.deploy.replicas, 2)
  assert.equal(result.runtime[0].env.MODE.value, 'production')
  assert.equal(result.runtime[0].env.PORT.value, '8080')
})
test('Swarm 与单机 Compose 的不兼容设置会阻止部署', () => {
  assert.ok(compiler.compile('services:\n  web:\n    image: nginx:1\n    mem_limit: 512M', {}, { target: 'stack' }).errors.some(e => e.includes('Stack')))
  assert.ok(compiler.compile('services:\n  job:\n    image: job:1\n    deploy:\n      mode: replicated-job', {}, { target: 'compose' }).errors.some(e => e.includes('Swarm Job')))
})
test('缺失 env 文件、无效副本数和隐式主机变量不会被静默忽略', () => {
  const result = compiler.compile('services:\n  web:\n    image: nginx:1\n    env_file: missing.env\n    environment:\n      - HOME\n    deploy:\n      replicas: 51', {})
  assert.ok(result.errors.some(e => e.includes('缺少文件')))
  assert.ok(result.errors.some(e => e.includes('隐式读取')))
  assert.ok(result.errors.some(e => e.includes('0–50')))
})
