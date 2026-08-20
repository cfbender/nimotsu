import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const apiURL = process.env.NIMOTSU_DEV_API_URL ?? 'http://127.0.0.1:8081'

export default defineConfig({
  root: 'web',
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    allowedHosts: process.env.AMP_ORB ? true : undefined,
    proxy: {
      '/api': apiURL,
    },
  },
})
