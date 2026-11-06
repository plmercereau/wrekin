// The test run adds the migrations to the Worker's bindings (see vitest.config.ts); the
// Worker's own bindings are declared in src/cloudflare-env.d.ts.
declare global {
  namespace Cloudflare {
    interface Env {
      TEST_MIGRATIONS: D1Migration[];
    }
  }
}

export {};
