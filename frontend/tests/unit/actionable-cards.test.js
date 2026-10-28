import { describe, expect, it, vi } from 'vitest'
import blueprints from '../../src/views/blueprints.json'
import { view } from '../../src/components/view'

describe('actionable card contract', () => {
  it('uses native buttons for metric and health cards', () => {
    for (const [template, spread] of [[67, 4], [87, 6], [255, 4]]) {
      const root = blueprints[template][0]
      expect(root.tag).toBe('button')
      expect(root.attrs).toContainEqual([`NSCEXPR${spread}END`, ''])
      expect(root.attrs.find(([name]) => name === 'class')[1]).toContain('actionable-card')
    }
  })

  it('marks every resource card family with one stretched primary destination', () => {
    for (const template of [122, 124, 193, 196, 267, 271, 316]) {
      expect(blueprints[template][0].attrs.find(([name]) => name === 'class')[1]).toContain('actionable-card')
    }
  })

  it('routes the card surface and keyboard activation to its primary destination', () => {
    const root = view([{ tag: 'article', attrs: [['class', 'card actionable-card']], children: [
      { tag: 'button', attrs: [['data-action', 'x-app'], ['data-id', 'app-1'], ['data-card-primary', '']], children: ['Open'] },
    ] }]).nodes[0]
    expect(root.props).toMatchObject({ 'data-action': 'x-app', 'data-id': 'app-1', role: 'link', tabindex: 0 })
    const click = vi.fn()
    const preventDefault = vi.fn()
    root.props.onKeydown({ key: 'Enter', currentTarget: { click }, preventDefault })
    expect(click).toHaveBeenCalledOnce()
    expect(preventDefault).toHaveBeenCalledOnce()
  })
})
