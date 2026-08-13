import { defineConfig } from '@hey-api/openapi-ts';

// Reads the committed spec, not the live backend, so generation needs no running server.
export default defineConfig({
  input: '../../backend/openapi/openapi.json',
  output: './src',
  plugins: ['@hey-api/client-fetch', '@hey-api/typescript', '@hey-api/sdk'],
});
