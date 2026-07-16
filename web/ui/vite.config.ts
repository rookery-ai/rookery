/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

export default defineConfig({
  base: "/app/",
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  server: {
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
