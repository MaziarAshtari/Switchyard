import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const backend = 'http://localhost:8080'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, './src') },
  },
  // One small chunk is fine for an admin GUI embedded in the Go binary.
  build: { outDir: 'dist', chunkSizeWarningLimit: 1000 },
  server: {
    proxy: { '/api': backend, '/v1': backend, '/healthz': backend },
  },
})
