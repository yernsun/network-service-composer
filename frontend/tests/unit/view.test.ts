import { afterEach, describe, expect, it } from 'vitest'
import { h } from 'vue'
import { mount } from '@vue/test-utils'
import { View, add, nodes, view, clearRegions, setRegion, resolveRegions } from '../../src/components/view'
import { labels, observedVersion } from '../../src/i18n'
import { i18n, languages, setLocale } from '../../src/shared/i18n'
afterEach(() => { setLocale('en'); clearRegions() })
describe('original template rendering with explicit translation keys', () => {
  it('presents missing domain load evidence as unknown without inventing a version', () => {
    for (const locale of languages) {
      setLocale(locale.code)
      expect(observedVersion(undefined)).toBe(i18n.global.t('community.evidence_state.UNKNOWN'))
      expect(observedVersion(null)).toBe(i18n.global.t('community.evidence_state.UNKNOWN'))
      expect(observedVersion(3)).toBe('v3')
    }
  })
  it('retains the original JavaScript expression semantics', () => {
    expect(add(2, 3)).toBe(5)
    expect(add('2', 3)).toBe('23')
    expect(add(null, true)).toBe(1)
    expect(add([1], 2)).toBe('12')
    expect(add({}, 2)).toBe('[object Object]2')
    expect(add(undefined, 2)).toBeNaN()
    expect(add(new View(['before']), h('strong', 'after'))).toBeInstanceOf(View)
  })
  it('localizes static labels without translating or executing resource names and code', () => {
    const literal = '<img src=x onerror=alert(1)> 删除 {name} $host'
    for (const locale of languages) {
      setLocale(locale.code)
      const result = view([{ tag: 'article', attrs: [], children: [
        { i18n: 'nav.sign_out', variables: [] },
        { tag: 'code', attrs: [], children: ['NSCEXPR0END'] },
      ] }], [literal])
      const wrapper = mount({ render: () => h('div', nodes(result)) })
      expect(wrapper.find('code').text()).toBe(literal)
      expect(wrapper.find('img').exists()).toBe(false)
      expect(wrapper.text()).toContain(i18n.global.t('nav.sign_out'))
      wrapper.unmount()
    }
  })
  it('updates static label dictionaries without rebuilding or modifying data', () => {
    const dictionary = labels({ logout: () => i18n.global.t('nav.sign_out') })
    const initial = dictionary.logout
    setLocale('ja')
    expect(dictionary.logout).toBe(i18n.global.t('nav.sign_out'))
    expect(dictionary.logout).not.toBe(initial)
    Object.assign(dictionary, { later: () => i18n.global.t('nav.sign_out') })
    setLocale('de')
    expect(dictionary.later).toBe(i18n.global.t('nav.sign_out'))
  })
  it('replaces original modal regions through inert Vue nodes', () => {
    const original = view([{ tag: 'div', attrs: [['id', 'p-form-error']], children: [] }])
    setRegion('p-form-error', new View([h('p', { role: 'alert' }, 'Save failed; draft kept')]))
    const wrapper = mount({ render: () => h('main', resolveRegions(nodes(original))) })
    expect(wrapper.get('[role=alert]').text()).toBe('Save failed; draft kept')
    wrapper.unmount()
    clearRegions()
    const fresh = mount({ render: () => h('main', resolveRegions(nodes(original))) })
    expect(fresh.find('[role=alert]').exists()).toBe(false)
  })
})
