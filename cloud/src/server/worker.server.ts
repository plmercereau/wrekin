import { env } from "cloudflare:workers";
import { servicesFor } from "../services";

/**
 * The deployment's bindings and the services built over them, for server functions and
 * server routes. The `.server.ts` suffix makes TanStack Start refuse this module in a
 * client bundle: `cloudflare:workers` exists only in the Worker.
 */
export const worker = () => ({ env, services: servicesFor(env) });
