import { cloudflareTest } from "@cloudflare/vitest-plugin";
import { defineConfig } from "vitest/config";

// Every test runs inside workerd with the bindings of wrangler.jsonc (D1, the
// Durable Object) and the dummy secrets below. Nothing here reaches the network.
export default defineConfig({
  plugins: [
    cloudflareTest({
      wrangler: { configPath: "./wrangler.jsonc" },
      miniflare: {
        bindings: {
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
  },
});
