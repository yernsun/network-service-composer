import js from '@eslint/js'
import pluginVue from 'eslint-plugin-vue'
import globals from 'globals'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  // The owner requires the supplied controller and generated renderers to retain
  // their original logic. Their generators and adapter code are linted; source
  // integrity/parity is verified separately. These files remain in coverage.
  { ignores: ['src/compose/schema.generated.js', 'dist/**', 'dist-demo/**', 'coverage/**', 'playwright-report/**', 'test-results/**', 'reference/**', 'src/mock/**', 'src/runtime/real.js', 'src/views/**/renderers.js', 'src/shared/api/schema.d.ts'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...pluginVue.configs['flat/recommended'],
  {
    files: ['**/*.{ts,vue,js}'],
    languageOptions: {
      globals: globals.browser,
      parserOptions: { parser: tseslint.parser },
    },
    rules: {
      'vue/multi-word-component-names': 'off',
      'vue/max-attributes-per-line': 'off',
      'vue/singleline-html-element-content-newline': 'off',
      'vue/html-self-closing': 'off',
      '@typescript-eslint/no-explicit-any': 'error',
    },
  },
  {
    files: ['scripts/**/*.mjs', '*.config.{js,ts}'],
    languageOptions: { globals: globals.node },
  },
)
