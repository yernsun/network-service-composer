import { createApp, h } from 'vue'
import ComposeWorkbench from '../../src/components/ComposeWorkbench.vue'

const source = 'services:\n  web:\n    image: nginx:${TAG}\n'
const props = {
  editor: { composeWork: source, runtimeWork: '', view: 'source' },
  application: { managed: true, name: 'isolated fixture' },
  document: { source, rawSource: source, runtime: '', sourceRevision: 'fixture-1' },
  save: async () => {}, review: async () => {}, merge: async () => '', deploy: async () => {},
  analyze: async current => {
    await new Promise(resolve => setTimeout(resolve, 50))
    return { validation: { diagnostics: [], deployable: true }, rendered_yaml: current.replace('${TAG}', '1.29') }
  },
}
createApp({ render: () => h(ComposeWorkbench, props) }).mount('#app')
