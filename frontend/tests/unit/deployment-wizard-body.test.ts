import { afterEach, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { h } from 'vue'
import DeploymentWizardBody from '../../src/components/DeploymentWizardBody.vue'
import { View } from '../../src/components/view'

let wrapper: VueWrapper
afterEach(() => wrapper?.unmount())
it('places parameter controls in the main column and preserves the deployment summary', () => {
  setActivePinia(createPinia())
  const content = new View([h('div', { class: 'x-steps' }, 'Steps'), h('div', { class: 'x-wizard-grid' }, [
    h('div', null, [h('input', { id: 'name', value: 'Unsaved name' }), h('div', { class: 'x-info-note' }, '')]),
    h('aside', { class: 'x-summary' }, 'Summary'),
  ])])
  wrapper = mount(DeploymentWizardBody, { props: { content }, slots: { default: () => h('section', { id: 'parameters' }, 'Long parameter description') } })
  expect(wrapper.get('.x-wizard-grid > div #parameters').text()).toBe('Long parameter description')
  expect(wrapper.get('#name').element).toHaveProperty('value', 'Unsaved name')
  expect(wrapper.get('.x-summary').text()).toBe('Summary')
  expect(wrapper.find('.x-info-note').exists()).toBe(false)
})
it('preserves mixed text, numbers, empty children and nested arrays alongside wizard controls', () => {
  setActivePinia(createPinia())
  const content = new View([
    'Leading text ', 0, false, null, undefined,
    [h('strong', { id: 'nested' }, 'Nested child'), ' trailing text'],
    h('div', { class: 'x-wizard-grid' }, [
      h('div', null, [
        h('p', { id: 'mixed-content' }, ['Main text ', 42, false, null, undefined, [h('em', null, 'Inline child'), ' trailing']]),
        h('input', { id: 'mixed-draft', value: 'Unsaved value' }),
      ]),
      h('aside', { class: 'x-summary' }, 'Summary'),
    ]),
  ])
  wrapper = mount(DeploymentWizardBody, { props: { content }, slots: { default: () => h('section', { id: 'parameters' }, 'Parameter controls') } })
  expect(wrapper.text()).toContain('Leading text 0')
  expect(wrapper.get('#nested').text()).toBe('Nested child')
  expect(wrapper.text()).toContain(' trailing text')
  expect(wrapper.get('#mixed-content').text()).toBe('Main text 42Inline child trailing')
  expect(wrapper.get('#mixed-draft').element).toHaveProperty('value', 'Unsaved value')
  expect(wrapper.get('.x-wizard-grid > div #parameters').text()).toBe('Parameter controls')
  expect(wrapper.findAll('.deployment-wizard-controls')).toHaveLength(1)
  expect(wrapper.get('.x-summary').text()).toBe('Summary')
})
it('shows a repeated manager gate only once while preserving other notes', () => {
  setActivePinia(createPinia())
  const message = 'A Manager connection is required.'
  const content = new View([h('div', { class: 'x-wizard-grid' }, [h('div', null, [
    h('div', { class: 'x-info-note' }, message),
    h('div', { class: 'x-info-note' }, 'Deployment target guidance'),
    h('div', { class: 'callout red' }, [h('span', null, message)]),
  ]), h('aside', { class: 'x-summary' }, 'Summary')])])
  wrapper = mount(DeploymentWizardBody, { props: { content } })
  expect(wrapper.text().split(message)).toHaveLength(2)
  expect(wrapper.get('.x-info-note').text()).toBe('Deployment target guidance')
  expect(wrapper.get('.callout.red').text()).toBe(message)
})
