import { defineConfig } from "vite-plus";

export default defineConfig({
  build: {
    outDir: "../public",
    emptyOutDir: false,
  },
  plugins: [],
  fmt: {},
  lint: { options: { typeAware: true, typeCheck: true } },
});
