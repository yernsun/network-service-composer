import { config } from '@vue/test-utils'
import { afterEach, vi } from 'vitest'

config.global.stubs = { transition: false }

Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
  configurable: true,
  value: vi.fn(),
})

afterEach(() => {
  // Vitest 4 restores spies separately from vi.fn/module-mock state.
  vi.resetAllMocks()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})
