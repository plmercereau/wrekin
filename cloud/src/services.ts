import { createAuth, type Auth } from "./auth";
import { getDb } from "./db/kysely";
import { KyselyNodeStore, type NodeStore } from "./db/nodes";
import type { Env } from "./env";
import { hubFor, type HubFactory } from "./hub/client";

/** What the routes need beyond the raw bindings. Built from `env` once per isolate; tests inject their own. */
export type Services = {
  auth: Auth;
  nodes: NodeStore;
  hub: HubFactory;
};

const provided = new WeakMap<object, Services>();

/** Tests call this with a stand-in `env` so the app runs without Cloudflare bindings. */
export function provideServices(env: Env, services: Services): void {
  provided.set(env.DB, services);
}

export function servicesFor(env: Env): Services {
  const existing = provided.get(env.DB);
  if (existing) return existing;
  const services: Services = {
    auth: createAuth(env),
    nodes: new KyselyNodeStore(getDb(env)),
    hub: (accountId) => hubFor(env, accountId),
  };
  provided.set(env.DB, services);
  return services;
}
