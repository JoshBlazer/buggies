import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Buggies serves the dashboard as static assets out of the same daemon that
// receives webhooks (cmd/buggies serve). base "./" keeps every asset reference
// relative so the bundle works whether the daemon mounts it at / or under a
// subpath behind a reverse proxy. In dev, /api is proxied to a locally running
// `buggies serve` on :8080 so the SPA and the Go JSON API share an origin.
export default defineConfig({
  plugins: [react()],
  base: "./",
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: process.env.BUGGIES_API ?? "http://localhost:8080",
        changeOrigin: true,
      },
    },
  },
});
