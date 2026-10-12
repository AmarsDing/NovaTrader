/**
 * ESLint flat config（P6 工程化）。
 * - js/ts/vue 混编：TS 由 typescript-eslint 承接，.vue 脚本块经 vue-eslint-parser 转交 TS parser；
 * - 桌面内部工具：关闭 multi-word-component-names（面板组件多为单词命名）；
 * - 格式化归 prettier（eslint-config-prettier 关闭冲突规则），lint 只管质量。
 */
import js from '@eslint/js'
import globals from 'globals'
import vue from 'eslint-plugin-vue'
import prettier from 'eslint-config-prettier/flat'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  {
    ignores: ['dist/**', 'src-tauri/**', 'coverage/**', 'node_modules/**', 'bundle-stats.html'],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...vue.configs['flat/recommended'],
  {
    // .vue 的 <script lang="ts"> 交给 TS parser
    files: ['**/*.vue'],
    languageOptions: {
      parserOptions: { parser: tseslint.parser },
    },
  },
  {
    files: ['src/**/*.{js,ts,vue}'],
    languageOptions: {
      globals: { ...globals.browser },
    },
    rules: {
      // TS 已覆盖未定义名检查；vue 模板里的全局（sessionStorage 等）靠 browser globals
      'no-undef': 'off',
    },
  },
  {
    // 根目录配置脚本跑在 Node
    files: ['*.js', '*.config.js'],
    languageOptions: {
      globals: { ...globals.node },
    },
  },
  {
    rules: {
      'vue/multi-word-component-names': 'off',
      'vue/no-v-html': 'off', // 情报流等富文本来自本地后端，非外部输入
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
    },
  },
  prettier,
)
