import { afterEach, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import MarketplaceParameters from '../../src/components/MarketplaceParameters.vue'
import { useWorkspaceStore } from '../../src/stores/workspace'
import { composeSession, deploymentReady } from '../../src/compose/session'
import { i18n } from '../../src/shared/i18n'
let panel
const locale = i18n.global.locale.value
afterEach(() => { panel?.unmount(); panel = undefined; i18n.global.locale.value = locale })
function fixture() {
 setActivePinia(createPinia())
 const store=useWorkspaceStore()
 const wizard={source:'template',template:'redis',step:2,raw:'services: {}',catalogVariables:[{key:'PASSWORD',secret:true,required:true,min_length:16,description:{en:'Credential', 'zh-CN':'访问密码'}},{key:'DATABASE',default:'app'}]}
 store.service={workload:{wizard}}
 panel=mount(MarketplaceParameters,{global:{plugins:[i18n]}})
 return {store,wizard}
}
it('keeps secrets in the draft, escapes dotenv dollar expressions and invalidates review',async()=>{
 const {wizard}=fixture()
 const session=composeSession(wizard);session.review={id:'old-review'};wizard.reviewAck=true
 const input=panel.get('#market-parameter-PASSWORD')
 expect(input.attributes('type')).toBe('password')
 expect(input.attributes('autocomplete')).toBe('off')
 expect(input.attributes('aria-describedby')).toBe('market-parameter-PASSWORD-help market-parameter-PASSWORD-error')
 expect(panel.get('label[for="market-parameter-PASSWORD"]').text()).toBe('PASSWORD')
 await input.setValue('secret$HOME${UNSET}"next')
 expect(wizard.catalogValues.PASSWORD).toBe('secret$HOME${UNSET}"next')
 expect(wizard.runtimeEnv).toContain('PASSWORD="secret$$HOME$${UNSET}\\"next"')
 expect(wizard.runtimeEnv).toContain('DATABASE="app"')
 expect(wizard.reviewAck).toBe(false)
 expect(deploymentReady(wizard)).toBe(false)
 expect(wizard.touched).toBe(true)
 expect(input.attributes('aria-describedby')).toBe('market-parameter-PASSWORD-help')
})
it('localizes descriptions, switches parameter sets and hides frozen review inputs',async()=>{
 const {store,wizard}=fixture()
 i18n.global.locale.value='zh-CN';await nextTick()
 expect(panel.text()).toContain('访问密码')
 wizard.template='mysql';wizard.catalogVariables=[{key:'USER',description:'Database user',default:'initial'}];wizard.catalogValues={USER:'saved'};store.revision++
 await nextTick()
 expect(panel.find('#market-parameter-PASSWORD').exists()).toBe(false)
 expect(panel.get('#market-parameter-USER').element.value).toBe('saved')
 wizard.step=4;store.revision++;await nextTick()
 expect(panel.find('[data-testid=market-parameters]').exists()).toBe(false)
})
it('has no parameter controls for raw compose or absent wizard',async()=>{
 const {store,wizard}=fixture()
 wizard.source='file';store.revision++;await nextTick()
 expect(panel.find('input').exists()).toBe(false)
 store.service.workload.wizard=null;store.revision++;await nextTick()
 expect(panel.find('section').exists()).toBe(false)
})
