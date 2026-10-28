import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import ServiceLogs from '../../src/components/ServiceLogs.vue'
import { session } from '../../src/shared/api/client'
import { i18n, setLocale } from '../../src/shared/i18n'

vi.mock('../../src/shared/api/client', () => ({ session: ref({ role: 'administrator' }) }))
class LogSource extends EventTarget {
  static connections: LogSource[] = []
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  close = vi.fn()
  constructor(readonly url: string) { super(); LogSource.connections.push(this) }
  log(stream: string, text: string) { this.dispatchEvent(new MessageEvent('log', { data: JSON.stringify({ stream, text }) })) }
}
let wrapper: VueWrapper
beforeEach(() => { setLocale('en'); session.value.role = 'administrator'; LogSource.connections = []; vi.stubGlobal('EventSource', LogSource) })
afterEach(() => wrapper?.unmount())
function screen(runtimeType = 'swarm') { wrapper = mount(ServiceLogs, { props: { serviceId: 'svc/encoded', runtimeType }, global: { plugins: [i18n] } }); return wrapper }

it('streams escaped output, ignores unsupported streams and closes on pause and unmount', async () => {
  const w = screen(), source = LogSource.connections[0]!
  expect(source.url).toBe('/api/v1/services/svc%2Fencoded/logs?tail=200')
  source.onopen!(); source.log('stdout', '<img src=x onerror=alert(1)>'); source.log('stderr', 'database warning'); source.log('other', 'ignored')
  await flushPromises()
  expect(w.find('img').exists()).toBe(false); expect(w.get('pre').text()).toContain('<img src=x onerror=alert(1)>')
  expect(w.get('pre .text-amber-300').text()).toContain('database warning'); expect(w.text()).not.toContain('ignored')
  await w.get('button').trigger('click'); expect(source.close).toHaveBeenCalledOnce()
  await w.get('button').trigger('click'); expect(LogSource.connections).toHaveLength(2)
  w.unmount(); expect(LogSource.connections[1]!.close).toHaveBeenCalledOnce()
})

it('retains only the most recent 2000 lines', async () => {
  const w = screen(), source = LogSource.connections[0]!
  for (let i = 0; i < 2001; i++) source.log('stdout', `line-${i}`)
  await flushPromises(); expect(w.findAll('pre span')).toHaveLength(2000)
  expect(w.findAll('pre span')[0]!.text()).toBe('line-1'); expect(w.findAll('pre span').at(-1)!.text()).toBe('line-2000')
})

it.each(['invalid-json', 'error-event', 'connection-error', 'unavailable'])('retains collected logs and closes the stream on %s', async failure => {
  const w = screen(), source = LogSource.connections[0]!
  source.log('stdout', 'last known output')
  if (failure === 'invalid-json') source.dispatchEvent(new MessageEvent('log', { data: '{broken' }))
  else if (failure === 'connection-error') source.onerror!()
  else source.dispatchEvent(new Event(failure === 'error-event' ? 'error' : 'unavailable'))
  await flushPromises(); expect(source.close).toHaveBeenCalledOnce(); expect(w.get('pre').text()).toContain('last known output')
  expect(w.get('button').text()).toBe(i18n.global.t('serviceLogs.resume'))
  await w.get('button').trigger('click'); expect(LogSource.connections).toHaveLength(2)
})

it.each(['docker', 'viewer'])('does not open log streams for %s access', async boundary => {
  if (boundary === 'viewer') session.value.role = 'viewer'
  const w = screen(boundary === 'docker' ? 'docker' : 'swarm'); await flushPromises()
  expect(LogSource.connections).toHaveLength(0); expect(w.find('button').exists()).toBe(false)
  expect(w.text()).toContain(i18n.global.t(`serviceLogs.${boundary === 'docker' ? 'swarmOnly' : 'adminOnly'}`))
})
