import { describe, expect, it } from "vitest";
import { nodeWorker, TEST_BASE_URL } from "../helpers/node-app";
import { createTestSession } from "../helpers/session";

// What Claude's connector does, end to end: register, authorize with PKCE and the
// resource indicator, consent, exchange the code, then initialize the MCP session.
async function pkce() {
  const verifier = "verifier-".repeat(6);
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
  const challenge = btoa(String.fromCharCode(...new Uint8Array(digest))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  return { verifier, challenge };
}

describe("Claude's connector, end to end", () => {
  it("gets a token and initializes /mcp", async () => {
    const { fetcher, services } = nodeWorker();
    const redirectUri = "https://claude.ai/api/mcp/auth_callback";
    const reg = await fetcher("/api/auth/oauth2/register", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ client_name: "Claude", redirect_uris: [redirectUri], token_endpoint_auth_method: "none", grant_types: ["authorization_code", "refresh_token"], response_types: ["code"], scope: "claudeai" }),
    });
    expect(reg.status, await reg.clone().text()).toBe(201);
    const { client_id } = (await reg.json()) as { client_id: string };
    const { headers, cookieHeaders } = await createTestSession(services.auth, "e2e-user");
    const { verifier, challenge } = await pkce();
    const q = new URLSearchParams({ client_id, redirect_uri: redirectUri, response_type: "code", scope: "claudeai offline_access", state: "s", code_challenge: challenge, code_challenge_method: "S256", resource: `${TEST_BASE_URL}/mcp` });
    const auth = await fetcher(`/api/auth/oauth2/authorize?${q}`, { headers });
    const loc = auth.headers.get("location") ?? ((await auth.json()) as { url: string }).url;
    const consentUrl = new URL(loc, TEST_BASE_URL);
    const consent = await fetcher("/api/auth/oauth2/consent", {
      method: "POST",
      headers: { "content-type": "application/json", origin: TEST_BASE_URL, ...cookieHeaders },
      body: JSON.stringify({ accept: true, oauth_query: consentUrl.search.slice(1) }),
    });
    const consentBody = (await consent.json()) as { url: string };
    const code = new URL(consentBody.url).searchParams.get("code");
    expect(code, JSON.stringify(consentBody)).toBeTruthy();
    const tok = await fetcher("/api/auth/oauth2/token", {
      method: "POST",
      headers: { "content-type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({ grant_type: "authorization_code", code: code!, redirect_uri: redirectUri, client_id, code_verifier: verifier, resource: `${TEST_BASE_URL}/mcp` }),
    });
    const tokBody = (await tok.json()) as { access_token?: string };
    expect(tok.status, JSON.stringify(tokBody)).toBe(200);
    const init = await fetcher("/mcp", {
      method: "POST",
      headers: { "content-type": "application/json", accept: "application/json, text/event-stream", authorization: `Bearer ${tokBody.access_token}`, "mcp-protocol-version": "2025-06-18" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "claude-ai", version: "0.1.0" } } }),
    });
    const initText = await init.text();
    expect(init.status, initText).toBe(200);
    const list = await fetcher("/mcp", {
      method: "POST",
      headers: { "content-type": "application/json", accept: "application/json, text/event-stream", authorization: `Bearer ${tokBody.access_token}`, "mcp-protocol-version": "2025-06-18" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 2, method: "tools/list", params: {} }),
    });
    const listText = await list.text();
    expect(list.status, listText).toBe(200);
  });
});
