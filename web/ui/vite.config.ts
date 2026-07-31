/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

export default defineConfig({
  base: "/",
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
      // The UI font lives in internal/fonts/ because go:embed cannot reach
      // outside its own package dir, and the Go export path needs the same
      // bytes. Aliasing (rather than keeping a second copy under src/) is what
      // keeps the two consumers on one file that cannot drift.
      "@fonts": path.resolve(__dirname, "../../internal/fonts"),
    },
  },
  server: {
    // Dev server must be allowed to read the font from outside the Vite root.
    fs: {
      allow: [path.resolve(__dirname), path.resolve(__dirname, "../../internal/fonts")],
    },
    proxy: {
      // Dev loop: Go server on :8080 answers the API (cookies work same-origin
      // because the proxy forwards them). SSE needs buffering off — proxy
      // handles that natively.
      "/api": { target: "http://127.0.0.1:8080", changeOrigin: false },
    },
  },
  // Vitest config lives here so one file drives build+test.
  test: {
    environment: "jsdom",
    setupFiles: "./vitest.setup.ts",
    globals: true,
  },
});
