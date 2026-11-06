import { applyD1Migrations, env } from "cloudflare:test";

// Runs before each test file in the workers pool; the migrations are read in vitest.config.ts.
await applyD1Migrations(env.DB, env.TEST_MIGRATIONS);
