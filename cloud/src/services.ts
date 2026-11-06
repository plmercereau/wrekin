import { createAuth, type Auth } from "./auth";
import type { Env } from "./env";

/** What the routes need beyond the raw bindings. Built from `env` once per isolate; tests inject their own. */
export type Services = {
  auth: Auth;
};

const provided = new WeakMap<object, Services>();

/** Tests call this with a stand-in `env` so the app runs without Cloudflare bindings. */
export function provideServices(env: Env, services: Services): void {
  provided.set(env.DB, services);
}

export function servicesFor(env: Env): Services {
  const existing = provided.get(env.DB);
  if (existing) return existing;
  const services: Services = { auth: createAuth(env) };
  provided.set(env.DB, services);
  return services;
}
