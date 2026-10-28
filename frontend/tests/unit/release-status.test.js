import { mount } from '@vue/test-utils'
import { Fragment, h, nextTick, ref } from 'vue'
import { afterEach, expect, it } from 'vitest'
import { releaseStatusView } from '../../src/components/release-status'
import { languages, setLocale } from '../../src/shared/i18n'

afterEach(() => setLocale('en'))

function screen(release, note = '', noteError = false) {
  return mount({ render: () => h(Fragment, null, releaseStatusView(release.value, note, noteError).nodes) })
}

it('lists gateways on separate rows and changes overall and target colors as evidence arrives', async () => {
  setLocale('zh-CN')
  const release = ref({ status: 'PUBLISHING', targets: [
    { gateway_id: 'main', name: 'gateway-main', status: 'RUNNING' },
    { gateway_id: 'pve', name: 'gateway-pve', status: 'PENDING' },
  ] })
  const wrapper = screen(release)
  try {
    expect(wrapper.find('.pool-release-overall .badge.blue').text()).toBe('发布中')
    expect(wrapper.findAll('.pool-release-targets li').map(row => row.find('.pool-release-gateway').text())).toEqual(['gateway-main', 'gateway-pve'])
    expect(wrapper.findAll('.pool-release-targets li .badge.blue')).toHaveLength(2)

    release.value = { status: 'PARTIAL', targets: [
      { gateway_id: 'main', name: 'gateway-main', status: 'SUCCEEDED' },
      { gateway_id: 'pve', name: 'gateway-pve', status: 'FAILED', error: 'nginx -t failed' },
    ] }
    await nextTick()
    expect(wrapper.find('.pool-release-overall .badge.amber').exists()).toBe(true)
    expect(wrapper.findAll('.pool-release-targets li')[0].find('.badge.green').exists()).toBe(true)
    expect(wrapper.findAll('.pool-release-targets li')[1].find('.badge.red').exists()).toBe(true)
    expect(wrapper.find('.pool-release-error').text()).toBe('nginx -t failed')

    release.value = { status: 'UNKNOWN', targets: [{ gateway_id: 'main', name: 'gateway-main', status: 'UNKNOWN' }] }
    await nextTick()
    expect(wrapper.find('.pool-release-overall .badge.amber').exists()).toBe(true)
    expect(wrapper.find('.pool-release-targets .badge.amber').exists()).toBe(true)
    expect(wrapper.find('.pool-release-result[data-tone="green"]').exists()).toBe(false)
  } finally { wrapper.unmount() }
})

it('renders gateway names and errors as text, with status labels in all supported languages', () => {
  const name = '<img src=x onerror=alert(1)>'
  const error = '<script>alert(1)</script>'
  const release = ref({ status: 'FAILED', targets: [{ gateway_id: 'gateway-1', name, status: 'FAILED', error }] })
  for (const { code } of languages) {
    setLocale(code)
    const wrapper = screen(release)
    try {
      expect(wrapper.find('.pool-release-overall .badge.red').text()).toBeTruthy()
      expect(wrapper.find('.pool-release-overall .badge.red').text()).not.toContain('community.state')
      expect(wrapper.find('.pool-release-gateway').text()).toBe(name)
      expect(wrapper.find('.pool-release-error').text()).toBe(error)
      expect(wrapper.find('img').exists()).toBe(false)
      expect(wrapper.find('script').exists()).toBe(false)
    } finally { wrapper.unmount() }
  }
})

it('keeps stopped and unfamiliar evidence cautionary and handles incomplete target identity', async () => {
  setLocale('en')
  const release = ref({ status: 'STOPPED', targets: [
    { name: 'gateway-without-id', status: 'STOPPED' },
    { gateway_id: 'gateway-id-only', name: '', status: 'FUTURE_STATUS' },
    { gateway_id: '', name: '', status: '' },
  ] })
  const wrapper = screen(release, 'Status read failed', true)
  try {
    expect(wrapper.find('.pool-release-overall .badge.amber').exists()).toBe(true)
    expect(wrapper.findAll('.pool-release-targets li').map(row => row.find('.pool-release-gateway').text())).toEqual(['gateway-without-id', 'gateway-id-only', '—'])
    expect(wrapper.findAll('.pool-release-targets li .badge.amber')).toHaveLength(3)
    expect(wrapper.findAll('.pool-release-targets li .badge').map(badge => badge.text())).toContain('FUTURE_STATUS')
    expect(wrapper.find('.pool-release-note.is-error').text()).toBe('Status read failed')

    release.value = { status: 'FUTURE_STATUS' }
    await nextTick()
    expect(wrapper.find('.pool-release-overall .badge.amber').text()).toBe('FUTURE_STATUS')
    expect(wrapper.findAll('.pool-release-targets li')).toHaveLength(0)

    release.value = { status: '' }
    await nextTick()
    expect(wrapper.find('.pool-release-overall .badge.amber').text()).toBe('Unknown')
  } finally { wrapper.unmount() }
})
