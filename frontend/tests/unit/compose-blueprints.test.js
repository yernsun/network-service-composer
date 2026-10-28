import { expect, it } from 'vitest'
import { deploymentBlueprints } from '../../src/compose/blueprints'
it('describes real deployment without changing demo or unrelated copy', () => {
 const source = [{ i18n: 'archive.message_9ef91f682b7a', variables: [] }, { i18n: 'archive.message_ccb178a59049' }, { tag: 'p', children: ['keep'] }]
 expect(deploymentBlueprints(source)).toEqual([{ i18n: 'compose.confirmDeployment', variables: [] }, { i18n: 'compose.credentialsHint' }, { tag: 'p', children: ['keep'] }])
 expect(source[0].i18n).toBe('archive.message_9ef91f682b7a')
})
