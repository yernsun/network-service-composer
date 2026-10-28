import { describe, expect, it } from 'vitest'
import { editPlacement, parseCompose, validateCompose } from '../../src/compose/validator'
import fixtures from '../../../internal/composecheck/testdata/conformance.json'

const source = `# application comment
x-base: &base
  image: mysql:8.4
  deploy:
    replicas: 1 # preserve replicas
    placement:
      constraints:
        - node.role == worker # preserve role
services:
  mysql:
    <<: *base
    volumes:
      - data:/var/lib/mysql
  other:
    <<: *base
volumes:
  data: {}
`
describe('Compose validation and local edits', () => {
  it('stops after syntax or schema errors and maps fields to source', () => {
    const duplicate = validateCompose('services:\n  web:\n    image: a\n    image: b\n')
    expect(duplicate.syntax_valid).toBe(false)
    expect(duplicate.services).toEqual([])
    const wrong = parseCompose('services:\n  web:\n    image: [bad]\n')
    expect(wrong.schema_valid).toBe(false)
    expect(wrong.diagnostics[0]).toMatchObject({ service: 'web', line: 3, path: ['services', 'web', 'image'] })
    expect(parseCompose('services: {}\n---\nservices: {}').syntax_valid).toBe(false)
  })
  it('accepts full YAML forms and separates standalone storage policy', () => {
    expect(validateCompose(source).schema_valid).toBe(true)
    const raw = 'services:\n  db:\n    image: db:1\n    volumes:\n      - type: bind\n        source: /srv/data\n        target: /data\n      - type: tmpfs\n        target: /tmp\n'
    expect(validateCompose(raw).diagnostics).toEqual([])
    expect(validateCompose(raw, { swarm: true }).diagnostics[0].code).toBe('SWARM_BIND_MOUNT_WITHOUT_PLACEMENT')
  })
  it('adds a local override without changing other anchor consumers', () => {
    const changed = editPlacement(source, 'mysql', 'node.hostname == worker-01')
    expect(changed.error).toBeUndefined()
    const result = parseCompose(changed.source)
    expect(result.tree.services.mysql.deploy.placement.constraints).toEqual(['node.role == worker', 'node.hostname == worker-01'])
    expect(result.tree.services.other.deploy.placement.constraints).toEqual(['node.role == worker'])
    expect(changed.source).toContain('# application comment')
    expect(changed.source).toContain('# preserve replicas')
    expect(changed.source).toContain('# preserve role')
    expect(result.tree.services.mysql.volumes).toEqual(['data:/var/lib/mysql'])
    expect(editPlacement(changed.source, 'mysql', ' node.hostname==worker-01 ').source).toBe(changed.source)
  })
  it('requires explicit replacement of a conflict and preserves unrelated constraints', () => {
    const first = editPlacement(source, 'mysql', 'node.hostname == worker-01').source
    const conflict = editPlacement(first, 'mysql', 'node.hostname == worker-02')
    expect(conflict.source).toBe(first)
    expect(conflict.conflicts).toEqual(['node.hostname == worker-01'])
    const changed = editPlacement(first, 'mysql', 'node.hostname == worker-02', { replace: true })
    expect(parseCompose(changed.source).tree.services.mysql.deploy.placement.constraints).toEqual(['node.role == worker', 'node.hostname == worker-02'])
  })
})
it.each(fixtures)('shares Go conformance: $name', ({ yaml, code }) => {
  const result = validateCompose(yaml, { swarm: true })
  if (code) expect(result.diagnostics.map(d => d.code)).toContain(code)
  else expect(result.diagnostics).toEqual([])
})
it('keeps nested anchors and aliases local to the edited service', () => {
  const raw = 'x-base: &base\n  image: nginx:1\n  deploy: &deploy\n    replicas: 1\n    placement: &place\n      constraints: [node.role == worker]\nservices:\n  one: *base\n  two: *base\n  three:\n    image: nginx:1\n    deploy: *deploy\n'
  const result = parseCompose(editPlacement(raw, 'one', 'node.hostname == worker-01').source)
  expect(result.diagnostics).toEqual([])
  expect(result.tree.services.one.deploy.placement.constraints).toHaveLength(2)
  expect(result.tree.services.two.deploy.placement.constraints).toEqual(['node.role == worker'])
  expect(result.tree.services.three.deploy.placement.constraints).toEqual(['node.role == worker'])
})
it('repairs contradictory existing constraints without duplicating the retained choice', () => {
 const raw = 'services:\n  web:\n    image: nginx:1\n    deploy:\n      placement:\n        constraints: [node.hostname == one, node.hostname == two]\n'
 expect(editPlacement(raw, 'web', 'node.hostname == one').error).toBe('PLACEMENT_CONSTRAINT_CONFLICT')
 const changed = editPlacement(raw, 'web', 'node.hostname == one', { replace: true })
 expect(parseCompose(changed.source).tree.services.web.deploy.placement.constraints).toEqual(['node.hostname == one'])
})
it('isolates edits to the service that defines a shared anchor', () => {
 const raw = 'services:\n  one: &base\n    image: nginx:1\n    deploy:\n      placement:\n        constraints: &limits [node.role == worker]\n  two: *base\n  three:\n    image: nginx:1\n    deploy:\n      placement:\n        constraints: *limits\n'
 const changed = editPlacement(raw, 'one', 'node.hostname == worker-01')
 const result = parseCompose(changed.source)
 expect(result.diagnostics).toEqual([])
 expect(result.tree.services.one.deploy.placement.constraints).toHaveLength(2)
 expect(result.tree.services.two.deploy.placement.constraints).toEqual(['node.role == worker'])
 expect(result.tree.services.three.deploy.placement.constraints).toEqual(['node.role == worker'])
})
