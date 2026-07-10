import { defineConfig } from "vite";

// During `npm run dev`, proxy /api to the Go backend so the SPA and API share
// an origin. In production the Go binary serves dist/ and handles /api itself.
export default defineConfig({
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://localhost:8090", changeOrigin: true },
      "/auth": { target: "http://localhost:8090", changeOrigin: true },
    },
  },
  build: {
    target: "es2022",
    chunkSizeWarningLimit: 1200,
  },
});
