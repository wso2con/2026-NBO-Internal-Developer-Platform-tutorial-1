import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// `npm run dev` proxies /applications to loan-api so the dev server behaves like
// the container, where both are served from the same origin.
// Test configuration lives in vitest.config.ts — vitest bundles its own Vite, and
// mixing the two `defineConfig` types in one file makes tsc unhappy.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    proxy: {
      '/applications': process.env.API_BASE_URL ?? 'http://localhost:8080',
    },
  },
})
