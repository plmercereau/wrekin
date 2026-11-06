import type { Env as WorkerEnv } from "../src/env";

// `env` from "cloudflare:test" / "cloudflare:workers" is typed through this global namespace.
declare global {
  namespace Cloudflare {
    interface Env extends WorkerEnv {
      TEST_MIGRATIONS: D1Migration[];
    }
  }
}
