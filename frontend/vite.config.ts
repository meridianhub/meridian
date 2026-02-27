import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/ws/events': { target: 'http://localhost:8090', ws: true },
      '/meridian.': { target: 'http://localhost:8090', changeOrigin: true },
      '/v1': { target: 'http://localhost:8090', changeOrigin: true },
    },
  },
  preview: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/ws/events': { target: 'http://localhost:8090', ws: true },
      '/meridian.': { target: 'http://localhost:8090', changeOrigin: true },
      '/v1': { target: 'http://localhost:8090', changeOrigin: true },
    },
  },
})
