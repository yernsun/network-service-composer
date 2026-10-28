import { createApp, h } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import DomainHistory from '../../src/components/DomainHistory.vue'
import { i18n } from '../../src/shared/i18n'
import { session } from '../../src/shared/api/client'
import '../../src/styles/original.css'

session.value.locale = 'zh-CN'
createApp({ render: () => h(DomainHistory, { poolId: 'pool-history', domainId: 'domain-a', routeIds: ['route-a'] }) })
  .use(i18n)
  .use(VueQueryPlugin, { queryClient: new QueryClient({ defaultOptions: { queries: { retry: false } } }) })
  .mount('#app')
