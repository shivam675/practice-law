import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    host: true,
    port: 5173,
    watch: { usePolling: true, interval: 500 },
    // The API is reached through the dev server so the refresh cookie stays
    // same-origin and SameSite=Strict keeps working in development.
    proxy: {
      "/speech": { target: process.env.VITE_MEDIA_PROXY ?? "http://localhost:8200", ws: true },
      "/api": {
        target: process.env.VITE_API_PROXY ?? "http://localhost:8080",
        changeOrigin: true,
      },
    },
  },
});
