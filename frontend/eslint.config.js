import js from '@eslint/js'
import pluginVue from 'eslint-plugin-vue'
import vueParser from 'vue-eslint-parser'
import tsParser from '@typescript-eslint/parser'
import tsPlugin from '@typescript-eslint/eslint-plugin'
import prettierConfig from 'eslint-config-prettier'

export default [
  // 基础 JavaScript 推荐规则
  js.configs.recommended,

  // Vue 3 推荐规则
  ...pluginVue.configs['flat/recommended'],

  // Prettier 配置（禁用与 Prettier 冲突的规则）
  prettierConfig,

  // 全局配置
  {
    files: ['**/*.{js,ts,vue}'],
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: {
        // 浏览器环境
        window: 'readonly',
        document: 'readonly',
        navigator: 'readonly',
        console: 'readonly',
        localStorage: 'readonly',
        sessionStorage: 'readonly',
        setTimeout: 'readonly',
        setInterval: 'readonly',
        clearTimeout: 'readonly',
        clearInterval: 'readonly',
        confirm: 'readonly',
        alert: 'readonly',
        prompt: 'readonly',
        fetch: 'readonly',
        URL: 'readonly',
        URLSearchParams: 'readonly',
        AbortController: 'readonly',
        AbortSignal: 'readonly',
        RequestInit: 'readonly',
        Response: 'readonly',
        Request: 'readonly',
        Headers: 'readonly',
        FormData: 'readonly',
        Blob: 'readonly',
        File: 'readonly',
        Event: 'readonly',
        KeyboardEvent: 'readonly',
        MouseEvent: 'readonly',
        HTMLElement: 'readonly',
        Element: 'readonly',
        ResizeObserver: 'readonly',
        CustomEvent: 'readonly',
        MessageEvent: 'readonly',
        // Node.js 环境
        process: 'readonly',
        __dirname: 'readonly',
        __filename: 'readonly',
        module: 'readonly',
        require: 'readonly'
      }
    }
  },

  // TypeScript 文件配置
  {
    files: ['**/*.ts'],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: 'latest',
        sourceType: 'module'
      }
    },
    plugins: {
      '@typescript-eslint': tsPlugin
    },
    rules: {
      ...tsPlugin.configs.recommended.rules,
      // TypeScript 特定规则
      '@typescript-eslint/no-explicit-any': 'warn',
      '@typescript-eslint/no-unused-vars': [
        'warn',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_'
        }
      ]
    }
  },

  // Vue 文件配置
  {
    files: ['**/*.vue'],
    languageOptions: {
      parser: vueParser,
      parserOptions: {
        ecmaVersion: 'latest',
        sourceType: 'module',
        parser: tsParser,
        extraFileExtensions: ['.vue']
      }
    },
    rules: {
      // Vue 3 特定规则
      'vue/multi-word-component-names': 'off', // 允许单词组件名
      'vue/no-v-html': 'warn', // 警告使用 v-html
      'vue/require-default-prop': 'off', // 不强制要求 prop 默认值
      'vue/require-explicit-emits': 'warn', // 建议显式声明 emits
      'vue/no-undef-properties': 'error', // 检测模板中引用了 setup/props 中未定义的属性
      // 代码风格（与 Prettier 不冲突的规则）
      'vue/html-self-closing': [
        'error',
        {
          html: {
            void: 'always',
            normal: 'never',
            component: 'always'
          },
          svg: 'always',
          math: 'always'
        }
      ],
      'vue/max-attributes-per-line': 'off', // 由 Prettier 处理
      'vue/singleline-html-element-content-newline': 'off', // 由 Prettier 处理
      'vue/html-indent': 'off' // 由 Prettier 处理
    }
  },

  // 通用规则
  {
    rules: {
      // 代码质量
      'no-console': process.env.NODE_ENV === 'production' ? 'warn' : 'off',
      'no-debugger': process.env.NODE_ENV === 'production' ? 'error' : 'off',
      'no-unused-vars': [
        'warn',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_'
        }
      ],
      'no-undef': 'error',
      'prefer-const': 'warn',
      'no-var': 'error',

      // 代码风格（不与 Prettier 冲突）
      'no-multiple-empty-lines': [
        'error',
        {
          max: 1,
          maxEOF: 0
        }
      ],
      'eol-last': ['error', 'always']
    }
  },

  // TypeScript 已由编译器和 @typescript-eslint 处理标识符与未使用变量。
  {
    files: ['**/*.ts'],
    rules: {
      'no-undef': 'off',
      'no-unused-vars': 'off'
    }
  },

  // 测试桩会在同一文件内声明多个轻量组件，并仅列出测试所需的 prop 名称。
  // 这些生产组件约束不适用于测试替身，限定在测试文件中关闭以避免误报。
  {
    files: ['**/*.test.ts'],
    rules: {
      'vue/one-component-per-file': 'off',
      'vue/require-prop-types': 'off'
    }
  },

  // 忽略文件
  {
    ignores: [
      'dist/**',
      'node_modules/**',
      '*.config.js',
      '*.config.ts',
      'coverage/**',
      '.vite/**',
      '.nuxt/**',
      '.output/**'
    ]
  }
]
