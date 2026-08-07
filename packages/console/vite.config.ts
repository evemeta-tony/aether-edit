// packages/console/vite.config.ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The console talks to four services under a single /api prefix, each with its
// own sub-path (/api/tenancy, /api/upload, /api/jobs, /api/telemetry). In
// development, point these at the running services with the VITE_DEV_*_TARGET
// env vars; unset targets are simply not proxied (real loading/error states
// surface instead of fabricated data, per R10).
const target = (v: string | undefined, fallback: string) => v || fallback;

export default defineConfig(({ mode }) => {
  const dev = mode === "development";
  return {
    plugins: [react()],
    server: dev
      ? {
          proxy: {
            "/api/tenancy": {
              target: target(process.env.VITE_DEV_TENANCY_TARGET, "http://127.0.0.1:8091"),
              changeOrigin: true,
              rewrite: (p: string) => p.replace(/^\/api\/tenancy/, ""),
            },
            "/api/upload": {
              target: target(process.env.VITE_DEV_UPLOAD_TARGET, "http://127.0.0.1:8092"),
              changeOrigin: true,
              rewrite: (p: string) => p.replace(/^\/api\/upload/, ""),
            },
            "/api/jobs": {
              target: target(process.env.VITE_DEV_JOBS_TARGET, "http://127.0.0.1:8093"),
              changeOrigin: true,
              rewrite: (p: string) => p.replace(/^\/api\/jobs/, ""),
            },
            "/api/telemetry": {
              target: target(process.env.VITE_DEV_TELEMETRY_TARGET, "http://127.0.0.1:8094"),
              changeOrigin: true,
              rewrite: (p: string) => p.replace(/^\/api\/telemetry/, ""),
            },
          },
        }
      : undefined,
    build: {
      target: "es2020",
      sourcemap: true,
    },
  };
});
