import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'
import prettier from 'eslint-config-prettier'
import { createRequire } from 'module'
const require = createRequire(import.meta.url)
const requirePageStructure = require('./eslint-rules/require-page-structure.cjs')

export default defineConfig([
  globalIgnores(['dist', 'src/api/gen']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
      prettier,
    ],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    rules: {
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      '@typescript-eslint/consistent-type-imports': 'error',
      // Complexity guardrails mirroring the Go side's discipline. All `warn`
      // so pre-existing files do not break CI; they act as a ratchet on new
      // code. Existing violators carry inline disables with an explanation.
      complexity: ['warn', 15],
      'max-lines': ['warn', { max: 500, skipBlankLines: true, skipComments: true }],
      'max-lines-per-function': ['warn', { max: 80, skipBlankLines: true, skipComments: true }],
      'max-statements': ['warn', 25],
    },
  },
  // Test and E2E files: per-function size/statement/complexity limits add noise
  // here because a single `describe`/`it` callback legitimately wraps the whole
  // suite. Keep `max-lines` (it still encourages splitting oversized suites) but
  // relax the per-function rules.
  {
    files: ['**/*.test.{ts,tsx}', 'src/test/**/*.{ts,tsx}', 'e2e/**/*.{ts,tsx}'],
    rules: {
      'max-lines-per-function': 'off',
      'max-statements': 'off',
      complexity: 'off',
    },
  },
  // E2E test files are not React code - disable React-specific rules
  {
    files: ['e2e/**/*.{ts,tsx}'],
    rules: {
      'react-hooks/rules-of-hooks': 'off',
      'react-refresh/only-export-components': 'off',
    },
  },
  // Enforce PageShell and PageHeader usage in feature pages
  {
    files: ['src/features/**/pages/**/*.{ts,tsx}'],
    plugins: {
      meridian: {
        rules: {
          'require-page-structure': requirePageStructure,
        },
      },
    },
    rules: {
      'meridian/require-page-structure': 'warn',
    },
  },
])
