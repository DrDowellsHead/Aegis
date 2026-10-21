import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// This is a STATIC UX scaffold. Never proxy the privileged loopback
// history API or embed AEGIS_HISTORY_TOKEN into browser-visible code.
export default defineConfig({
  plugins: [react()],
  server: { host: '127.0.0.1', port: 5173, strictPort: true },
  preview: { host: '127.0.0.1', port: 4173, strictPort: true },
  build: { outDir: 'dist', sourcemap: false }
})
