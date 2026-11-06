import { createMcpHandler } from "@modelcontextprotocol/server";
import { createLocalJWKSet, jwtVerify, type JSONWebKeySet, type JWTPayload } from "jose";
import type { Services } from "../services";
import { buildMcpServer } from "./server";

/** The resource identifier tokens are bound to: the MCP endpoint itself. */
export const mcpResource = (baseURL: string) => `${baseURL}/mcp`;

/** The RFC 9728 challenge that sends an MCP client to the authorization flow. */
function unauthorized(baseURL: string, message: string, error?: "invalid_token"): Response {
  const params = [`resource_metadata="${baseURL}/.well-known/oauth-protected-resource/mcp"`];
  if (error) params.push(`error="${error}"`);
  return Response.json(
    { jsonrpc: "2.0", error: { code: -32000, message }, id: null },
    { status: 401, headers: { "www-authenticate": `Bearer ${params.join(", ")}` } },
  );
}

/**
 * Verifies an access token the way `requireMcpAuth` of @better-auth/mcp does (signature,
 * issuer, audience = the MCP resource, expiry, required `azp`), but against the JWKS read
 * from the auth instance itself rather than fetched over HTTP: a Worker cannot fetch its
 * own hostname, so the library's remote JWKS lookup failed in production and every
 * authenticated /mcp request answered 500.
 */
export async function verifyAccessToken(services: Services, baseURL: string, token: string): Promise<JWTPayload | null> {
  const { auth } = services;
  const context = await auth.$context;
  const jwks = (await auth.api.getJwks()) as JSONWebKeySet;
  try {
    const { payload } = await jwtVerify(token, createLocalJWKSet(jwks), {
      issuer: context.baseURL,
      audience: mcpResource(baseURL),
      requiredClaims: ["azp", "sub", "exp"],
    });
    return payload;
  } catch {
    return null;
  }
}

/**
 * POST /mcp. A bearer access token issued by this Worker's OAuth provider for the
 * resource `<BASE_URL>/mcp` is required; its subject is the account every tool call is
 * scoped to. One McpServer answers one request and is dropped: the v2 SDK handler serves
 * the 2026-07-28 per-request protocol and, in its `stateless` posture, the 2025-era
 * streamable HTTP clients still in use (GET and DELETE, the 2025 session operations, are
 * refused before reaching it, see the server entry).
 */
export function createMcpFetch(services: Services, baseURL: string): (request: Request) => Promise<Response> {
  return async (request) => {
    const header = request.headers.get("authorization") ?? "";
    const match = /^Bearer\s+(\S+)$/i.exec(header);
    if (!match) return unauthorized(baseURL, "authentication required");
    const claims = await verifyAccessToken(services, baseURL, match[1]!);
    if (!claims) return unauthorized(baseURL, "invalid access token", "invalid_token");
    const accountId = typeof claims.sub === "string" && claims.sub ? claims.sub : null;
    if (!accountId) return unauthorized(baseURL, "access token has no subject", "invalid_token");
    const handler = createMcpHandler(
      () => buildMcpServer({ accountId, nodes: services.nodes, hub: services.hub(accountId) }),
      { legacy: "stateless" },
    );
    return handler.fetch(request);
  };
}
