import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  base: "/",
  build: {
    outDir: "../internal/app/assets",
    emptyOutDir: true,
    assetsDir: "ui",
    target: "chrome110",
  },
});
