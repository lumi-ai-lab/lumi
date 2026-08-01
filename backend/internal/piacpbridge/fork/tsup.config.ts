import { defineConfig } from 'tsup'

export default defineConfig({
  entry: ['src/index.ts'],
  format: ['esm'],
  platform: 'node',
  target: 'node20',
  sourcemap: false,
  clean: true,
  dts: false,
  splitting: false,
  minify: false,
  noExternal: [/.*/],
  banner: {
    js: '#!/usr/bin/env node'
  }
})
