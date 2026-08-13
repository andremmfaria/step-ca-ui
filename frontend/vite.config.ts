import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Vite stands in for nginx in dev: same origin from the browser's point of
// view, so cookies and CSRF behave as they do in production. secure:false
// because the backend serves a self-signed leaf (UI_TLS_MODE=self-signed).
const proxied = ['/api', '/auth', '/health', '/ready']

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: Object.fromEntries(
      proxied.map((p) => [p, { target: 'https://localhost:8443', changeOrigin: true, secure: false }]),
    ),
  },
})
