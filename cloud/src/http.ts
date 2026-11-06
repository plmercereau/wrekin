import { oauthProviderAuthServerMetadata, oauthProviderOpenIdConfigMetadata } from "@better-auth/oauth-provider";
import { z } from "zod";
import { connect } from "./connect";
import type { Env } from "./env";
import { createMcpFetch } from "./mcp/handler";
import { NodeServiceError, registerNode, registerSchema, unregisterNode, unregisterSchema } from "./nodes/service";
import type { Services } from "./services";

const json = (body: unknown, status: number, headers?: HeadersInit) => Response.json(body, { status, headers });

/** Machine clients talk to these prefixes and get JSON errors; browsers get HTML from the pages. */
export const wantsJson = (path: string) => path.startsWith("/api/") || path === "/mcp" || path.startsWith("/mcp/");

/** Under /api, only the tRPC endpoint is a TanStack Start server route; everything else is answered here. */
const isTrpc = (path: string) => path === "/api/trpc" || path.startsWith("/api/trpc/");

const under = (path: string, prefix: string) => path === prefix || path.startsWith(`${prefix}/`);

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

  async function register(request: Request): Promise<Response> {
    const user = await sessionUser(request);
    if (!user) return json({ error: "unauthenticated" }, 401);
    const body = registerSchema.safeParse(await request.json().catch(() => null));
    if (!body.success) return json({ error: "invalid_body", message: z.prettifyError(body.error) }, 400);
    return json(await registerNode(services, user.id, body.data), 201);
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
