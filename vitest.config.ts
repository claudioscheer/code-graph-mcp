import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["extractors/**/*.test.ts"],
    exclude: ["testdata/**", "node_modules/**", "dist/**"],
  },
});
