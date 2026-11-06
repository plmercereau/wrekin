import { requireMcpAuth } from "@better-auth/mcp";
import { StreamableHTTPTransport } from "@hono/mcp";
import { Hono } from "hono";
import type { AppEnv } from "../app";
import { servicesFor } from "../services";
import { buildMcpServer } from "./server";

/** The resource identifier tokens are bound to: the MCP endpoint itself. */
export const mcpResource = (baseURL: string) => `${baseURL}/mcp`;

export function mcpRoutes() {
  const routes = new Hono<AppEnv>();

  routes.post("/mcp", (c) => {
    const { auth, nodes, hub } = servicesFor(c.env);
    const protectedHandler = requireMcpAuth(
      auth,
      async (_request, claims) => {
        const accountId = typeof claims.sub === "string" && claims.sub ? claims.sub : null;
        if (!accountId) {
          return Response.json(
            { jsonrpc: "2.0", error: { code: -32000, message: "access token has no subject" }, id: null },
            { status: 401 },
          );
        }
        // Stateless: a new server and transport per request, nothing kept between calls.
        const server = buildMcpServer({ accountId, nodes, hub: hub(accountId) });
        const transport = new StreamableHTTPTransport({ sessionIdGenerator: undefined });
        await server.connect(transport);
        const response = await transport.handleRequest(c);
        return response ?? new Response(null, { status: 202 });
      },
      { resource: mcpResource(c.env.BASE_URL) },
    );
    return protectedHandler(c.req.raw);
  });

  // No sessions to resume or end on a stateless server.
  routes.on(["GET", "DELETE"], "/mcp", (c) =>
    c.json({ error: "method_not_allowed", message: "the Wrekin MCP server is stateless: POST only" }, 405, { Allow: "POST" }),
  );

  return routes;
}
