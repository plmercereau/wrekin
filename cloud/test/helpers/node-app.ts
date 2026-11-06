import { memoryAdapter, type MemoryDB } from "better-auth/adapters/memory";
import { buildAuth } from "../../src/auth";
import type { Env } from "../../src/env";
import { createApiHandler } from "../../src/http";
import { provideServices, type Services } from "../../src/services";
import { createContext } from "../../src/trpc/context";
import { createCaller } from "../../src/trpc/router";
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

/**
 * Better Auth on the memory adapter. The adapter expects one array per model and
 * the model list depends on the plugins, so the store creates each array on first
 * access instead of enumerating the schema.
 */
export function memoryAuth(env: Env) {
  const tables: MemoryDB = {};
  const db = new Proxy(tables, {
    get: (target, key) => (typeof key === "string" ? (target[key] ??= []) : undefined),
    has: (target, key) => typeof key === "string" && (target[key] ??= []) !== undefined,
  });
  return buildAuth({
    baseURL: env.BASE_URL,
    secret: env.BETTER_AUTH_SECRET,
    database: memoryAdapter(db),
    github: { clientId: env.GITHUB_CLIENT_ID, clientSecret: env.GITHUB_CLIENT_SECRET },
    google: { clientId: env.GOOGLE_CLIENT_ID, clientSecret: env.GOOGLE_CLIENT_SECRET },
  });
}

/**
 * The Worker's machine surfaces (src/http.ts) and tRPC router hosted in plain Node: memory
 * auth, an in-memory node store and a fake hub unless overridden. The pages need the built
 * Worker and are covered by the workerd run only.
 */
export function nodeWorker(overrides: Partial<Services> = {}) {
  const env = fakeEnv();
  const services: Services = {
    auth: overrides.auth ?? memoryAuth(env),
    nodes: overrides.nodes ?? new MemoryNodeStore(),
    hub: overrides.hub ?? new FakeHub().factory,
  };
  provideServices(env, services);
  const api = createApiHandler(env, services);
  const fetcher: Fetcher = async (path, init) => {
    const response = await api(new Request(`${TEST_BASE_URL}${path}`, init));
    return response ?? new Response("a page: not served by the API dispatcher", { status: 501 });
  };
  /** The tRPC procedures called in-process, as the bearer of `headers` (anonymous without). */
  const caller = async (headers: Record<string, string> = {}) =>
    createCaller(await createContext({ request: new Request(`${TEST_BASE_URL}/api/trpc`, { headers }), env, services }));
  return { env, services, fetcher, caller };
}
