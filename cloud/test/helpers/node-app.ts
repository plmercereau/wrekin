import { getAuthTables } from "@better-auth/core/db";
import { memoryAdapter, type MemoryDB } from "better-auth/adapters/memory";
import { createApp } from "../../src/app";
import { buildAuth } from "../../src/auth";
import type { Env } from "../../src/env";
import { provideServices, type Services } from "../../src/services";
import type { Fetcher } from "../shared/auth-suite";
import { FakeHub } from "./fake-hub";
import { MemoryNodeStore } from "./memory-nodes";

export const TEST_BASE_URL = "http://localhost:8787";

/** The Worker's env, as a stand-in: nothing reads its bindings because every service is injected. */
export function fakeEnv(): Env {
  return {
    DB: {} as D1Database,
    ACCOUNT_HUB: {} as Env["ACCOUNT_HUB"],
    BASE_URL: TEST_BASE_URL,
    BETTER_AUTH_SECRET: "test-secret-that-is-long-enough-for-better-auth",
    GITHUB_CLIENT_ID: "github-id",
    GITHUB_CLIENT_SECRET: "github-secret",
    GOOGLE_CLIENT_ID: "google-id",
    GOOGLE_CLIENT_SECRET: "google-secret",
  };
}

/** Better Auth on the memory adapter, with a table for every model of the configuration. */
export function memoryAuth(env: Env) {
  const db: MemoryDB = {};
  const auth = buildAuth({
    baseURL: env.BASE_URL,
    secret: env.BETTER_AUTH_SECRET,
    database: memoryAdapter(db),
    github: { clientId: env.GITHUB_CLIENT_ID, clientSecret: env.GITHUB_CLIENT_SECRET },
    google: { clientId: env.GOOGLE_CLIENT_ID, clientSecret: env.GOOGLE_CLIENT_SECRET },
  });
  for (const table of Object.values(getAuthTables(auth.options))) db[table.modelName] ??= [];
  return auth;
}

/** The Worker hosted in plain Node: memory auth, an in-memory node store and a fake hub unless overridden. */
export function nodeApp(overrides: Partial<Services> = {}) {
  const env = fakeEnv();
  const services: Services = {
    auth: overrides.auth ?? memoryAuth(env),
    nodes: overrides.nodes ?? new MemoryNodeStore(),
    hub: overrides.hub ?? new FakeHub().factory,
  };
  provideServices(env, services);
  const app = createApp();
  const fetcher: Fetcher = async (path, init) => app.request(path, init, env);
  return { app, env, services, fetcher };
}
