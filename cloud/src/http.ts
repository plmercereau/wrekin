import { oauthProviderAuthServerMetadata, oauthProviderOpenIdConfigMetadata } from "@better-auth/oauth-provider";
import { z } from "zod";
import { connect } from "./connect";
import type { Env } from "./env";
import { LIVE_PATH } from "./live";
import { createMcpFetch } from "./mcp/handler";
import { NodeServiceError, registerNode, registerSchema, unregisterNode, unregisterSchema } from "./nodes/service";
import type { Services } from "./services";

const json = (body: unknown, status: number, headers?: HeadersInit) => Response.json(body, { status, headers });

/** Machine clients talk to these prefixes and get JSON errors; browsers get HTML from the pages. */
export const wantsJson = (path: string) => path.startsWith("/api/") || path === "/mcp" || path.startsWith("/mcp/");

/** Under /api, only the tRPC endpoint is a TanStack Start server route; everything else is answered here. */
const isTrpc = (path: string) => path === "/api/trpc" || path.startsWith("/api/trpc/");

const under = (path: string, prefix: string) => path === prefix || path.startsWith(`${prefix}/`);

const isJson = (request: Request) => /^application\/json\s*(;|$)/i.test((request.headers.get("content-type") ?? "").trim());

/** The session is presented as `Authorization: Bearer …` (the Go agent), not as a cookie. */
const hasBearer = (request: Request) => /^bearer\s/i.test(request.headers.get("authorization") ?? "");

/**
 * Whether a request carrying a cookie session may come from the relay's own pages: the
 * `Origin` header, when present, must be the relay's origin, and `Sec-Fetch-Site`, when
 * present, must be `same-origin` or `none`. A request without either header (a
 * non-browser client, or an old browser) is let through, since no browser omits both on
 * a cross-site POST.
 */
export function sameSite(request: Request, ownOrigin: string): boolean {
  const origin = request.headers.get("origin");
  if (origin !== null && origin !== ownOrigin) return false;
  const site = request.headers.get("sec-fetch-site");
  if (site !== null && site !== "same-origin" && site !== "none") return false;
  return true;
}

/**
 * Better Auth's device token endpoint only reads JSON while RFC 8628 clients post a form,
 * so a form body on that one path is re-encoded as JSON first.
 */
async function deviceTokenAsJson(request: Request): Promise<Request> {
  const contentType = request.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().startsWith("application/x-www-form-urlencoded")) return request;
  const form = await request.formData();
  const headers = new Headers(request.headers);
  headers.set("content-type", "application/json");
  return new Request(request.url, { method: "POST", headers, body: JSON.stringify(Object.fromEntries(form.entries())) });
}

/**
 * The surfaces TanStack Start's file routes do not express well, answered before Start sees
 * the request: Better Auth, OAuth discovery, the node API the Go agent calls, the nodes'
 * WebSocket and the MCP endpoint. Resolves to null for a page or the tRPC route, which Start
 * renders. Pure with respect to the runtime: plain Node hosts it in the tests.
 */
export function createApiHandler(env: Env, services: Services): (request: Request) => Promise<Response | null> {
  const { auth } = services;
  const mcp = createMcpFetch(services, env.BASE_URL);
  // The authorization-server documents come from the provider's helpers, the
  // protected-resource document from the MCP plugin itself (through the auth handler).
  const authServerMetadata = oauthProviderAuthServerMetadata(auth);
  const openIdMetadata = oauthProviderOpenIdConfigMetadata(auth);

  // The session comes from the cookie or `Authorization: Bearer` (a terminal, not a browser).
  const sessionUser = async (request: Request) => (await auth.api.getSession({ headers: request.headers }))?.user ?? null;
  const ownOrigin = new URL(env.BASE_URL).origin;

  async function register(request: Request): Promise<Response> {
    // `request.json()` does not care about the Content-Type, a cross-site <form enctype="text/plain">
    // does produce a parseable body, and Better Auth's origin check covers /api/auth only: the
    // route refuses what a browser would send on another site's behalf before reading the session.
    if (!isJson(request)) return json({ error: "unsupported_media_type", message: "the body must be application/json" }, 415);
    if (!hasBearer(request) && !sameSite(request, ownOrigin)) {
      return json({ error: "cross_site", message: "a cookie session is accepted from the relay's own origin only" }, 403);
    }
    const user = await sessionUser(request);
    if (!user) return json({ error: "unauthenticated" }, 401);
    const body = registerSchema.safeParse(await request.json().catch(() => null));
    if (!body.success) return json({ error: "invalid_body", message: z.prettifyError(body.error) }, 400);
    const registered = await registerNode(services, user.id, body.data);
    if (hasBearer(request)) await revokeBearerSession(request);
    return json(registered, 201);
  }

  /**
   * The device flow hands the agent a full session token, scoped to nothing: once the node
   * row is written the token has served its purpose, so the relay ends that session itself
   * rather than trust the agent's sign-out. The bearer plugin has turned the header into
   * the session cookie by the time `signOut` runs, so it deletes exactly that session.
   */
  async function revokeBearerSession(request: Request): Promise<void> {
    try {
      await auth.api.signOut({ headers: request.headers });
    } catch (error) {
      console.warn("register: could not revoke the device-flow session", error instanceof Error ? error.message : String(error));
    }
  }

  /**
   * The nodes page's live channel (src/live.ts). A WebSocket handshake is not subject to
   * CORS and carries the cookie, so the origin is required, not merely checked when present:
   * every browser sends one on a WebSocket, and another site's page must not watch this
   * account. The channel carries no data, only a hint to fetch `nodes.list` again.
   */
  async function live(request: Request): Promise<Response> {
    if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
      return json({ error: "upgrade_required", message: "expected a WebSocket upgrade" }, 426);
    }
    if (request.headers.get("origin") !== ownOrigin) {
      return json({ error: "cross_site", message: "the live channel is open to the relay's own pages only" }, 403);
    }
    const user = await sessionUser(request);
    if (!user) return json({ error: "unauthenticated" }, 401);
    return services.hub(user.id).watch(request);
  }

  async function unregister(request: Request): Promise<Response> {
    const body = unregisterSchema.safeParse(await request.json().catch(() => null));
    if (!body.success) return json({ error: "invalid_body", message: z.prettifyError(body.error) }, 400);
    await unregisterNode(services, body.data);
    return new Response(null, { status: 204 });
  }

  async function route(request: Request, path: string): Promise<Response | null> {
    const { method } = request;
    // Better Auth owns everything under /api/auth, before any other route.
    if (path === "/api/auth/device/token" && method === "POST") return auth.handler(await deviceTokenAsJson(request));
    if (under(path, "/api/auth")) return auth.handler(request);
    // OAuth discovery lives at the root of the origin (RFC 8414, RFC 9728, OIDC), not under the auth base path.
    if (method === "GET" && under(path, "/.well-known/oauth-authorization-server")) return authServerMetadata(request);
    if (method === "GET" && under(path, "/.well-known/openid-configuration")) return openIdMetadata(request);
    if ((method === "GET" || method === "HEAD") && under(path, "/.well-known/oauth-protected-resource")) return auth.handler(request);
    // The node API the Go agent speaks; the browser uses the same operations through tRPC.
    if (path === "/api/nodes" && method === "POST") return register(request);
    if (path === "/api/nodes/unregister" && method === "POST") return unregister(request);
    if (path === LIVE_PATH && method === "GET") return live(request);
    // The nodes' WebSocket.
    if (path === "/connect" && method === "GET") return connect(request, env);
    // The remote MCP server Claude talks to. No sessions to resume or end on a stateless server.
    if (path === "/mcp") {
      if (method === "POST") return mcp(request);
      return json({ error: "method_not_allowed", message: "the Wrekin MCP server is stateless: POST only" }, 405, { Allow: "POST" });
    }
    if (isTrpc(path)) return null;
    if (wantsJson(path)) return json({ error: "not_found", message: `no route for ${method} ${path}` }, 404);
    return null;
  }

  return async (request) => {
    const path = new URL(request.url).pathname;
    try {
      return await route(request, path);
    } catch (error) {
      if (error instanceof NodeServiceError) return json({ error: error.code, message: error.message }, error.status);
      console.error("unhandled error", error instanceof Error ? (error.stack ?? error.message) : String(error));
      return json({ error: "internal", message: "something went wrong on the relay" }, 500);
    }
  };
}
