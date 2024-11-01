import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig(() => {
  const backend = process.env["BACKEND"]
    ? process.env["BACKEND"]
    : "http://localhost:8080";

  return {
    plugins: [react()],
    server: {
      proxy: {
        "/api": backend,
      },
    },
  };
});
