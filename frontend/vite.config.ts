/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Served under /admin/ behind the edge proxy. The dev server proxies the API to a
// locally running ledger-admin so the browser stays same-origin.
export default defineConfig({
  base: '/admin/',
  plugins: [react()],
  build: {
    outDir: 'dist',
    sourcemap: false,
    modulePreload: { polyfill: false },
  },
  server: {
    proxy: {
      '/admin/api': 'http://127.0.0.1:8084',
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['src/test/setup.ts'],
    css: false,
    restoreMocks: true,
  },
})
