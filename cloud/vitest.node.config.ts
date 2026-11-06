import { defineConfig } from "vitest/config";

// Plain Node run of the pure modules (schemas, tools with fakes, pages, hub core).
// `vitest.config.ts` runs every test, these included, inside workerd; this file
// exists for environments where workerd cannot bind a local port.
export default defineConfig({
  test: {
    include: ["test/unit/**/*.test.ts"],
    environment: "node",
  },
});
