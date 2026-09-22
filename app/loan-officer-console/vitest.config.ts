import { defineConfig } from 'vitest/config'

// Unit tests cover the pure display helpers in src/format.ts. The React plugin
// is deliberately absent: no component rendering is needed, and leaving it out
// keeps this config free of the Vite version clash described in vite.config.ts.
export default defineConfig({
  test: {
    environment: 'node',
    globals: true,
    include: ['src/**/*.test.ts'],
  },
})
