import { defineConfig } from "vitest/config";

// Plain Node run of the pure modules: protocol schemas, the hub core, the node service, the
// tRPC router with the memory auth and a fake hub, the MCP tools through an MCP client, the
// machine surfaces of src/http.ts, the deploy-config renderer. `vitest.config.ts` runs every
// test, these included, inside workerd; this file exists for environments where workerd
// cannot bind a local port.
export default defineConfig({
  test: {
    include: ["test/unit/**/*.test.ts"],
    environment: "node",
  },
});
