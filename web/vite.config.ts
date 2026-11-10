import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The dashboard is built into web/dist and embedded in the heimdall binary.
// In development, API calls are proxied to a locally running heimdall.
export default defineConfig({
  plugins: [react()],
  build: { outDir: "dist", emptyOutDir: true, sourcemap: false, chunkSizeWarningLimit: 900 },
  server: {
    port: 5173,
    proxy: { "/api": "http://127.0.0.1:8787", "/healthz": "http://127.0.0.1:8787" },
  },
});
