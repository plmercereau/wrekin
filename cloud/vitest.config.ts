/// <reference types="vite/client" />
import { cloudflareTest, readD1Migrations } from "@cloudflare/vitest-plugin";
import { defineConfig } from "vitest/config";

// Every test runs inside workerd with the bindings of wrangler.jsonc (D1, the
// Durable Object) and the dummy secrets below. Nothing here reaches the network.
export default defineConfig(async () => {
  // Web APIs only (no @types/node in this project; vite/client types import.meta.url): the
  // migrations directory next to this file.
  const migrationsDir = decodeURIComponent(new URL("./migrations", import.meta.url).pathname);
  const migrations = await readD1Migrations(migrationsDir);
  return {
    plugins: [
      cloudflareTest({
        wrangler: { configPath: "./wrangler.jsonc" },
        miniflare: {
          bindings: {
            TEST_MIGRATIONS: migrations,
            BASE_URL: "http://localhost:8787",
            BETTER_AUTH_SECRET: "test-secret-that-is-long-enough-for-better-auth",
            GITHUB_CLIENT_ID: "github-id",
            GITHUB_CLIENT_SECRET: "github-secret",
            GOOGLE_CLIENT_ID: "google-id",
            GOOGLE_CLIENT_SECRET: "google-secret",
          },
        },
      }),
    ],
    test: {
      include: ["test/**/*.test.ts"],
      setupFiles: ["./test/apply-migrations.ts"],
    },
  };
});
