import test from 'node:test'
import assert from 'node:assert/strict'
import { adaptBrand } from './brand.mjs'

test('adapts archive branding and demo storage without changing NSC contracts', () => {
  const original = "title: 'Nginx Service Composer'; document.title = ' · Nginx Service Composer'; const KEY = 'nginx-service-composer-vue-v1.3'; NSC; /api/v1; nsc.preferences"
  const adapted = adaptBrand(original)
  assert.equal(adapted, "title: 'Network Service Composer for Nginx'; document.title = ' · Network Service Composer'; const KEY = 'network-service-composer-vue-v1.3'; NSC; /api/v1; nsc.preferences")
  assert.equal(adaptBrand(adapted), adapted)
})
