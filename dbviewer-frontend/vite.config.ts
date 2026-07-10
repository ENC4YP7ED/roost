import { defineConfig } from "vite";

// The database viewer is served by the Roost binary under /dbviewer/, and its
// API is proxied through Roost's admin-gated /dbviewer/api mount.
export default defineConfig({
  base: "/dbviewer/",
  server: {
    port: 5174,
    proxy: { "/dbviewer": { target: "http://localhost:8090", changeOrigin: true } },
  },
  build: { target: "es2022", chunkSizeWarningLimit: 1200 },
});
