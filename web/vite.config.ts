import { defineConfig } from "vite-plus";

export default defineConfig({
  plugins: [],
  fmt: {},
  lint: { options: { typeAware: true, typeCheck: true } },
});
