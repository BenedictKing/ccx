import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import vuetify from 'vite-plugin-vuetify'
import { resolve } from 'path'

export default defineConfig(({ mode }) => {
  // 加载环境变量
  const env = loadEnv(mode, process.cwd(), '')

  const frontendPort = parseInt(env.VITE_FRONTEND_PORT || '5173')
  const backendUrl = env.VITE_PROXY_TARGET || 'http://localhost:3000'
  const uiLanguage = env.APP_UI_LANGUAGE || 'en'

  return {
    // 使用绝对路径，适配 Go 嵌入式部署
    base: '/',

    plugins: [
      vue(),
      vuetify({
        autoImport: false, // 禁用自动导入，使用手动配置的图标
        styles: {
          configFile: 'src/styles/settings.scss'
        }
      }),
    ],
    resolve: {
      alias: {
        '@': resolve(import.meta.dirname, 'src')
      }
    },
    optimizeDeps: {
      // vue-i18n 11.x 与 Vite 8 的 esbuild 预构建不兼容，
      // 预构建会丢失 init_runtime_dom_esm_bundler / init_shared_esm_bundler 的 import
      exclude: ['vue-i18n'],
    },
    define: {
      __APP_UI_LANGUAGE__: JSON.stringify(uiLanguage),
      __VUE_I18N_FULL_INSTALL__: false,
      __VUE_I18N_LEGACY_API__: false,
    },
    server: {
      port: frontendPort,
      proxy: {
        '/api': {
          target: backendUrl,
          changeOrigin: true
        },
        '/v1': {
          target: backendUrl,
          changeOrigin: true
        },
        '/health': {
          target: backendUrl,
          changeOrigin: true
        }
      }
    },
    css: {
      preprocessorOptions: {
        scss: {
          silenceDeprecations: ['import', 'global-builtin', 'if-function']
        }
      }
    },
    build: {
      // 与 tsconfig 的 ES2022 基线保持一致：不为语法糖抬高旧浏览器要求
      target: 'es2022',
      outDir: 'dist',
      emptyOutDir: true,
      // 确保资源路径正确
      assetsDir: 'assets',
      // 优化代码分割
      rollupOptions: {
        output: {
          manualChunks(id) {
            if (id.includes('apexcharts') || id.includes('vue3-apexcharts')) return 'charts'
            if (id.includes('vuetify')) return 'vuetify'
            if (id.includes('vue') || id.includes('pinia')) return 'vue-vendor'
          }
        }
      }
    }
  }
})
