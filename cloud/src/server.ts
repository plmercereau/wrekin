import { createStartHandler, defaultStreamHandler } from "@tanstack/react-start/server";
import { createServerEntry } from "@tanstack/react-start/server-entry";
import { env } from "cloudflare:workers";
import { createApiHandler } from "./http";
import { servicesFor } from "./services";

// The Worker's main module (wrangler.jsonc `main` points here, not at the default Start
// entry) so that the Durable Object class is exported from it, as the runtime requires.
export { AccountHub } from "./hub/account-hub";

const startFetch = createStartHandler(defaultStreamHandler);

/** The dispatcher of the machine surfaces, built once per isolate over this deployment's bindings. */
let api: ReturnType<typeof createApiHandler> | undefined;

/**
 * Every request enters here. The surfaces the Go agent and Claude depend on (/connect, the
 * node API, Better Auth, OAuth discovery, /mcp) are answered first, with the `Env` the
 * runtime exposes through `cloudflare:workers`; everything else is a page or the tRPC route
 * and goes to TanStack Start.
 */
export default createServerEntry({
  async fetch(request, opts) {
    api ??= createApiHandler(env, servicesFor(env));
    const handled = await api(request);
    return handled ?? startFetch(request, opts);
  },
});
