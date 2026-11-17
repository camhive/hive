/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { VitePWA } from "vite-plugin-pwa";
import { fileURLToPath } from "node:url";

// Dev: the API runs on :8080 (`go run ./cmd/cloud api`); same-origin proxy
// keeps the session cookie simple. Production: Caddy serves dist/ at
// app.<domain> and the app talks to api.<domain> (VITE_API_URL).
export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    // PWA (#56): app shell only. Video, hive and cloud API answers are never cached.
    // prompt: a new version waits until the user taps Update (#70, src/lib/swUpdate.ts),
    // so no reload cuts a playing stream.
    // Icon files carry a version in their names (icon-192.v2.png): when the logo
    // changes, bump it here, in index.html and in public/push-sw.js. Android picks
    // up a new name; iOS keeps the old icon until the app is added again.
    VitePWA({
      registerType: "prompt",
      includeAssets: ["icons/icon.v2.svg", "robots.txt"],
      manifest: {
        name: "Camhive",
        short_name: "Camhive",
        description: "Your cameras. Your footage stays home.",
        theme_color: "#faf8f4",
        background_color: "#faf8f4",
        display: "standalone",
        start_url: "/",
        scope: "/",
        icons: [
          { src: "icons/icon-192.v2.png", sizes: "192x192", type: "image/png" },
          { src: "icons/icon-512.v2.png", sizes: "512x512", type: "image/png" },
          { src: "icons/maskable-512.v2.png", sizes: "512x512", type: "image/png", purpose: "maskable" },
        ],
      },
      workbox: {
        globPatterns: ["**/*.{js,css,html,svg,png,woff2}"],
        navigateFallback: "/index.html",
        navigateFallbackDenylist: [/^\/v1\//, /^\/v0\//, /^\/healthz/],
        runtimeCaching: [], // nothing beyond the precached shell
        importScripts: ["push-sw.js"], // Web Push: show and open notifications (ADR-0021)
      },
      devOptions: { enabled: false },
    }),
  ],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  server: { proxy: { "/v1": "http://127.0.0.1:8080", "/healthz": "http://127.0.0.1:8080" } },
  preview: { proxy: { "/v1": "http://127.0.0.1:8080", "/healthz": "http://127.0.0.1:8080" } },
  build: { sourcemap: true, target: "es2022" },
  test: { environment: "jsdom", setupFiles: ["./test/setup.ts"], globals: false, css: false },
});
