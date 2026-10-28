<script lang="ts">
import { defineComponent, Fragment, h, type PropType } from 'vue'
import { nodes, resolveRegions } from './view'
import { usePreferencesStore } from '../stores/preferences'
import { useWorkspaceStore } from '../stores/workspace'
export default defineComponent({
  name: 'ViewContent',
  props: { content: { type: null as unknown as PropType<unknown>, required: true } },
  setup(props) {
    const store = useWorkspaceStore()
    const preferences = usePreferencesStore()
    return () => { void store.revision; void preferences.language; return h(Fragment, null, resolveRegions(nodes(props.content))) }
  },
})
</script>
