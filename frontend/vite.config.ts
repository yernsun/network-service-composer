import { fileURLToPath, URL } from 'node:url'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import vue from '@vitejs/plugin-vue'
import VueI18nPlugin from '@intlify/unplugin-vue-i18n/vite'
import { defineConfig, loadEnv, type ProxyOptions, type Plugin } from 'vite'
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const coveragePolicy = JSON.parse(
    readFileSync(new URL('../.agents/rules/coverage.json', import.meta.url), 'utf8'),
  )
  const branches = coveragePolicy.frontend?.branches
  if (typeof branches !== 'number' || !Number.isFinite(branches) || branches <= 0 || branches > 100)
    throw new Error('Invalid frontend branch coverage threshold')
  const proxy: Record<string, ProxyOptions> = {}
  proxy['/api'] = { target: env.VITE_API_PROXY_TARGET || 'http://127.0.0.1:8080', changeOrigin: false }
  proxy['/healthz'] = { target: env.VITE_API_PROXY_TARGET || 'http://127.0.0.1:8080' }
  const modeBoundary: Plugin = {
    name: 'nsc-workspace-mode-boundary',
    transformIndexHtml(html) {
      return html
    },
    generateBundle(_options, bundle) {
      const modules = [...this.getModuleIds()]
      if (modules.some((id) => id.endsWith('/mock/service.js') || id.endsWith('/data/project-lifecycle-demo.ts') || id.endsWith('/data/demo-transport.ts')))
        this.error('The Controller frontend must not include the demo backend')
      const dependencies = new Map<string, { name: string; version: string }>()
      for (const output of Object.values(bundle)) {
        if (output.type !== 'chunk') continue
        for (const [id, info] of Object.entries(output.modules)) {
          if (!info.renderedLength || !id.includes('/node_modules/')) continue
          const modulePath = id.replace(/^\0/, '')
          const offset = modulePath.lastIndexOf('/node_modules/') + '/node_modules/'.length
          const name = modulePath.slice(offset).match(/^(@[^/]+\/[^/]+|[^/]+)/)?.[0]
          if (!name) continue
          const pkg = JSON.parse(readFileSync(join(modulePath.slice(0, offset), name, 'package.json'), 'utf8'))
          dependencies.set(`${pkg.name}@${pkg.version}`, { name: pkg.name, version: pkg.version })
        }
      }
      this.emitFile({ type: 'asset', fileName: 'nsc-bundled-dependencies.json', source: JSON.stringify({ schema: 1,
        packages: [...dependencies.values()].sort((a,b) => a.name.localeCompare(b.name)),
        generatedCode: ['Ajv standalone validator', 'Compose Specification schema'],
      }, null, 2) + '\n' })
    },
  }
  return {
    plugins: [
      vue(),
      modeBoundary,
      VueI18nPlugin({
        include: fileURLToPath(new URL('./src/shared/i18n/generated/**', import.meta.url)),
        strictMessage: false,
        escapeHtml: false,
      }),
    ],
    resolve: { alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      '@workspace-runtime': fileURLToPath(new URL('./src/runtime/real.js', import.meta.url)),
      '@workspace-data': fileURLToPath(new URL('./src/data/api.js', import.meta.url)),
      '@workspace-transport': fileURLToPath(new URL('./src/data/transport.ts', import.meta.url)),
      '@project-lifecycle': fileURLToPath(new URL('./src/data/project-lifecycle-real.ts', import.meta.url)),
    } },
    build: {
      outDir: '../internal/api/frontend-dist',
      emptyOutDir: true,
      rollupOptions: {
        output: {
          manualChunks(id) {
            if (id.includes('/i18n/generated/')) return 'messages'
            if (id.includes('node_modules')) return 'vendor'
          },
        },
      },
    },
    server: {
      host: '127.0.0.1',
      port: 5173,
      proxy,
    },
    test: {
      environment: 'jsdom',
      maxWorkers: 1,
      setupFiles: ['./src/test/setup.ts'],
      coverage: {
        thresholds: { branches },
        provider: 'istanbul',
        reporter: ['text', 'json-summary', 'json', 'html'],
        include: ['src/**/*.{ts,js,vue}'],
        // Official-schema Ajv output is generated; analyzer and AST editor remain measured.
        exclude: ['src/compose/schema.generated.js', 'src/shared/api/schema.d.ts', 'src/shared/i18n/generated/**', 'src/test/**'],
      },
    },
  }
})
