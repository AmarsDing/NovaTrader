import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { visualizer } from 'rollup-plugin-visualizer'
import path from 'path'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const analyze = process.env.ANALYZE === '1'

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    vue(),
    // npm run analyze → 产物体积报告（treemap），输出到 web/bundle-stats.html（不进 dist）
    ...(analyze ? [visualizer({ filename: 'bundle-stats.html', template: 'treemap', gzipSize: true, brotliSize: true })] : []),
  ],
  clearScreen: false,
  envPrefix: ['VITE_', 'TAURI_'],
  server: {
    port: 5173,
    strictPort: true,
  },
  build: {
    rollupOptions: {
      input: {
        main: path.resolve(__dirname, 'index.html'),
        popout: path.resolve(__dirname, 'popout.html'),
      },
      output: {
        // 重依赖各自独立块：echarts/dockview/lightweight-charts，便于缓存与体积审计
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined
          if (id.includes('echarts') || id.includes('zrender')) return 'echarts'
          if (id.includes('dockview-core')) return 'dockview'
          if (id.includes('lightweight-charts')) return 'lwc'
          if (id.includes('/vue/') || id.includes('vue-router') || id.includes('pinia') || id.includes('@vue')) {
            return 'vue-vendor'
          }
          return 'vendor'
        },
      },
    },
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
  test: {
    environment: 'jsdom',
  },
})
