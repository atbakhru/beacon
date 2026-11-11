import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  base: "./",
  server: {
    proxy: {
      "/v1": { target: "http://localhost:8080", ws: true },
      "/healthz": "http://localhost:8080",
    },
  },
});
