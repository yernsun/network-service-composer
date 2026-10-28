<script lang="ts">
import { cloneVNode, defineComponent, h, isVNode, type PropType, type VNodeArrayChildren, type VNodeChild } from 'vue'
import { nodes, resolveRegions } from './view'
import { usePreferencesStore } from '../stores/preferences'
import { useWorkspaceStore } from '../stores/workspace'

const classes = (node: VNodeChild) => isVNode(node) ? String(node.props?.class || '').split(/\s+/) : []
function textContent(node: VNodeChild): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (!isVNode(node)) return ''
  return Array.isArray(node.children) ? node.children.map(textContent).join('') : typeof node.children === 'string' ? node.children : ''
}

export default defineComponent({
  name: 'DeploymentWizardBody',
  props: { content: { type: null as unknown as PropType<unknown>, required: true } },
  setup(props, { slots }) {
    const store = useWorkspaceStore()
    const preferences = usePreferencesStore()
    return () => {
      void store.revision; void preferences.language
      const content = resolveRegions(nodes(props.content))
      const errors = new Set<string>()
      function collect(items: VNodeChild[]) {
        for (const item of items) {
          const cls = classes(item)
          if (cls.includes('callout') && cls.includes('red')) errors.add(textContent(item).trim())
          if (isVNode(item) && Array.isArray(item.children)) collect(item.children as VNodeChild[])
        }
      }
      collect(content)
      function layout(items: VNodeChild[], main = false): VNodeChild[] {
        return items.flatMap<VNodeChild>((item, index): VNodeChild[] => {
          if (!isVNode(item)) return [item]
          const cls = classes(item), text = textContent(item).trim()
          // Empty template notes and duplicate gate messages add no information.
          if (cls.includes('x-info-note') && (!text || errors.has(text))) return []
          const copy = cloneVNode(item)
          if (Array.isArray(copy.children)) copy.children = layout(copy.children as VNodeChild[], cls.includes('x-wizard-grid')) as VNodeArrayChildren
          if (main && index === 0) copy.children = [...(Array.isArray(copy.children) ? copy.children : []), h('div', { class: 'deployment-wizard-controls', key: 'controls' }, slots.default?.()), h('div', { class: 'deployment-wizard-analysis', key: 'analysis' })] as VNodeArrayChildren
          return [copy]
        })
      }
      return h('div', { class: 'deployment-wizard-body' }, layout(content))
    }
  },
})
</script>

<style scoped>
.deployment-wizard-body :deep(.x-wizard-grid > div), .deployment-wizard-controls { min-width: 0; }
.deployment-wizard-body :deep(.x-fields-two) { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.deployment-wizard-body :deep(.x-step) { min-width: 0; overflow-wrap: anywhere; }
.deployment-wizard-body :deep(.x-step span) { flex-shrink: 0; }
.deployment-wizard-body :deep(.x-summary) { overflow-wrap: anywhere; }
@media (max-width:800px) { .deployment-wizard-body :deep(.x-fields-two) { grid-template-columns: minmax(0, 1fr); } }
</style>
