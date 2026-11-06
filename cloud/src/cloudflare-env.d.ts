import type { Env as WorkerEnv } from "./env";

// `env` from "cloudflare:workers" (and from "cloudflare:test") is typed through this global interface.
declare global {
  namespace Cloudflare {
    interface Env extends WorkerEnv {}
  }
}
