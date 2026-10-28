import { beforeEach, expect, it, vi } from 'vitest'
import { checkCompose, composeSession, deploymentReady, invalidateCompose, reviewCompose } from '../../src/compose/session'
import { request } from '../../src/shared/api/client'
vi.mock('../../src/shared/api/client', () => ({ request: vi.fn() }))
const wizard = () => ({ source: 'file', raw: 'services:\n  web:\n    image: nginx:1\n', env: 'one', runtime: 'swarm', step: 4, reviewAck: true })
beforeEach(() => vi.resetAllMocks())
it('invalidates immediately and discards out-of-order analysis', async () => {
 const w = wizard(); let first
 request.mockImplementationOnce(() => new Promise(resolve => { first = resolve }))
 const old = checkCompose(w, true)
 w.raw += '# changed\n'; invalidateCompose(w)
 expect(deploymentReady(w)).toBe(false); expect(w.reviewAck).toBe(false)
 request.mockResolvedValueOnce({ data: { deployable: false, diagnostics: [{ code: 'required' }] } })
 await checkCompose(w, true)
 first({ data: { deployable: true, diagnostics: [] } }); await old
 expect(composeSession(w).result.deployable).toBe(false)
 expect(deploymentReady(w)).toBe(false)
})
it('never requests semantic analysis for invalid YAML or schema', async () => {
 const w = wizard(); w.raw = 'services: ['
 await checkCompose(w, true); expect(request).not.toHaveBeenCalled()
 w.raw = 'services: []'; await checkCompose(w, true)
 expect(request).not.toHaveBeenCalled(); expect(deploymentReady(w)).toBe(false)
})
it('cannot publish a review from a previous environment or input', async () => {
 const w = wizard()
 request.mockResolvedValueOnce({ data: { deployable: true, diagnostics: [] } })
 await checkCompose(w, true)
 let finish
 request.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
 const review = reviewCompose(w)
 w.env = 'two'; invalidateCompose(w)
 finish({ data: {}, response: new Response() }); await review
 expect(request).toHaveBeenCalledTimes(2)
 expect(composeSession(w).review).toBeNull()
 expect(deploymentReady(w)).toBe(false)
})
it('requires fresh warning acknowledgement after source changes', async () => {
 const w = wizard(); w.deploymentPlan = { storage: [{ resource: 'retained' }], acknowledged: ['warning'] }
 invalidateCompose(w)
 expect(w.deploymentPlan.acknowledged).toEqual([])
 expect(w.deploymentPlan.storage).toEqual([{ resource: 'retained' }])
})
it('drops the inactive Swarm network selection after switching to standalone', async () => {
 const w = wizard(); w.networkName = 'previous-overlay'
 request.mockResolvedValue({ data: { deployable: true, diagnostics: [] } })
 await checkCompose(w, false)
 expect(request.mock.calls[0][2].body.network_name).toBe('')
})

it.each(['system.project_protected', 'auth.read_only', 'network'])('preserves API failure %s without claiming node failure and permits retry', async code => {
 const w = wizard()
 request.mockRejectedValueOnce({ code, params: {}, status: code === 'network' ? 0 : 403 })
 await checkCompose(w, true)
 expect(composeSession(w).error.code).toBe(code)
 expect(composeSession(w).error.key).not.toContain('SWARM_NODE_CHECK_UNAVAILABLE')
 expect(deploymentReady(w)).toBe(false)
 request.mockResolvedValueOnce({ data: { deployable: true, diagnostics: [] } })
 await checkCompose(w, true)
 expect(composeSession(w).error).toBe(false)
 expect(deploymentReady(w)).toBe(true)
})
it('preserves review failures instead of reporting unavailable nodes', async () => {
 const w = wizard()
 request.mockResolvedValueOnce({ data: { deployable: true, diagnostics: [] } })
 await checkCompose(w, true)
 request.mockResolvedValueOnce({ data: {}, response: new Response() })
 request.mockRejectedValueOnce({ code: 'system.project_protected' })
 await reviewCompose(w)
 expect(composeSession(w).error.code).toBe('system.project_protected')
 expect(composeSession(w).review).toBeNull()
})
it('reviews a catalog variant and invalidates when data node or parameters change', async () => {
 const w={...wizard(),source:'template',template:'redis-new',swarmTemplate:true,swarmRaw:'services:\n  redis:\n    image: redis:1\n',runtimeEnv:'REDIS_PASSWORD="private-value"',deploymentPlan:{data_node_id:'node-one'}}
 request.mockResolvedValue({data:{deployable:true,diagnostics:[]}})
 await checkCompose(w,true)
 const body=request.mock.calls[0][2].body
 expect(body.catalog_app_version_id).toBe('redis-new')
 expect(body.compose_yaml).toBeUndefined()
 expect(body.deployment_plan.data_node_id).toBe('node-one')
 expect(body.runtime_env).toBe(w.runtimeEnv)
 expect(deploymentReady(w)).toBe(true)
 w.deploymentPlan.data_node_id='node-two'
 expect(deploymentReady(w)).toBe(false)
 await checkCompose(w,true)
 w.runtimeEnv='REDIS_PASSWORD="changed-value"'
 expect(deploymentReady(w)).toBe(false)
})
