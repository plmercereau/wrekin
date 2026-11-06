import { requireMcpAuth } from "@better-auth/mcp";
import { createMcpHandler } from "@modelcontextprotocol/server";
import type { Services } from "../services";
import { buildMcpServer } from "./server";

/** The resource identifier tokens are bound to: the MCP endpoint itself. */
export const mcpResource = (baseURL: string) => `${baseURL}/mcp`;

/**
 * POST /mcp. `requireMcpAuth` verifies the bearer token against the authorization server's
 * JWKS and answers 401 with the RFC 9728 challenge otherwise; the token's subject is the
 * account every tool call is scoped to. One McpServer answers one request and is dropped:
 * the v2 SDK handler serves the 2026-07-28 per-request protocol and, in its `stateless`
 * posture, the 2025-era streamable HTTP clients still in use (GET and DELETE, the 2025
 * session operations, are refused before reaching it, see the server entry).
 */
export function createMcpFetch(services: Services, baseURL: string): (request: Request) => Promise<Response> {
  return requireMcpAuth(
    services.auth,
    async (request, claims) => {
      const accountId = typeof claims.sub === "string" && claims.sub ? claims.sub : null;
      if (!accountId) {
        return Response.json(
          { jsonrpc: "2.0", error: { code: -32000, message: "access token has no subject" }, id: null },
          { status: 401 },
        );
      }
      const handler = createMcpHandler(
        () => buildMcpServer({ accountId, nodes: services.nodes, hub: services.hub(accountId) }),
        { legacy: "stateless" },
      );
      return handler.fetch(request);
    },
    { resource: mcpResource(baseURL) },
  );
}
