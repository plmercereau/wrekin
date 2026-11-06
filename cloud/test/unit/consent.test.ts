import { describe, expect, it } from "vitest";
import { isAllowedRedirectHost, parseAllowedRedirectHosts, redirectUriHost, redirectUriValidator } from "../../src/oauth-redirects";
import { fakeEnv, memoryAuth, nodeWorker, TEST_BASE_URL } from "../helpers/node-app";
import { createTestSession } from "../helpers/session";
import type { Fetcher } from "../shared/auth-suite";

describe("redirectUriHost", () => {
  it("is the host, port included, of the URI the authorization goes to", () => {
    expect(redirectUriHost("https://attacker.example/cb?x=1")).toBe("attacker.example");
    expect(redirectUriHost("https://claude.ai/api/mcp/auth_callback")).toBe("claude.ai");
    expect(redirectUriHost("http://127.0.0.1:8123/callback")).toBe("127.0.0.1:8123");
    expect(redirectUriHost("cursor://anysphere.cursor-retrieval/oauth/callback")).toBe("anysphere.cursor-retrieval");
  });

  it("is null when there is no host to show", () => {
    expect(redirectUriHost(null)).toBeNull();
    expect(redirectUriHost(undefined)).toBeNull();
    expect(redirectUriHost("")).toBeNull();
    expect(redirectUriHost("not a url")).toBeNull();
    expect(redirectUriHost("urn:ietf:wg:oauth:2.0:oob")).toBeNull();
  });
});

describe("MCP_ALLOWED_REDIRECT_HOSTS", () => {
  it("parses a comma-separated list of suffixes, case and leading dots dropped", () => {
    expect(parseAllowedRedirectHosts(undefined)).toEqual([]);
    expect(parseAllowedRedirectHosts("")).toEqual([]);
    expect(parseAllowedRedirectHosts(" , ")).toEqual([]);
    expect(parseAllowedRedirectHosts("claude.ai, .Claude.com,,127.0.0.1")).toEqual(["claude.ai", "claude.com", "127.0.0.1"]);
  });

  it("matches a suffix exactly or as a parent domain, never as a prefix or a look-alike", () => {
    const allowed = ["claude.ai"];
    expect(isAllowedRedirectHost("claude.ai", allowed)).toBe(true);
    expect(isAllowedRedirectHost("app.claude.ai", allowed)).toBe(true);
    expect(isAllowedRedirectHost("CLAUDE.AI", allowed)).toBe(true);
    expect(isAllowedRedirectHost("claude.ai.evil.example", allowed)).toBe(false);
    expect(isAllowedRedirectHost("notclaude.ai", allowed)).toBe(false);
    expect(isAllowedRedirectHost("attacker.example", allowed)).toBe(false);
  });

  it("restricts nothing when the list is empty: the provider's default stays in force", () => {
    expect(isAllowedRedirectHost("attacker.example", [])).toBe(true);
    expect(redirectUriValidator([])).toBeUndefined();
  });

  it("builds a validateRedirectUri that keeps exact matching and adds the host check", () => {
    const validate = redirectUriValidator(["claude.ai"]);
    expect(validate).toBeDefined();
    expect(validate?.("https://claude.ai/cb", ["https://claude.ai/cb"], true)).toBe(true);
    expect(validate?.("https://claude.ai/other", ["https://claude.ai/cb"], false)).toBe(false); // not registered
    expect(validate?.("https://attacker.example/cb", ["https://attacker.example/cb"], true)).toBe(false);
    expect(validate?.("not a url", [], true)).toBe(false);
  });
});

/** Registers a public client through Dynamic Client Registration, the way Claude's connector does. */
async function registerClient(fetcher: Fetcher, redirectUri: string): Promise<string> {
  const res = await fetcher("/api/auth/oauth2/register", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      client_name: "Claude",
      client_uri: "https://claude.ai",
      redirect_uris: [redirectUri],
      token_endpoint_auth_method: "none",
      grant_types: ["authorization_code", "refresh_token"],
      response_types: ["code"],
    }),
  });
  expect(res.status, await res.clone().text()).toBe(201);
  const { client_id } = (await res.json()) as { client_id: string };
  return client_id;
}

/** Where `/oauth2/authorize` sends a signed-in user for `clientId` and `redirectUri`. */
async function authorize(fetcher: Fetcher, headers: Record<string, string>, clientId: string, redirectUri: string): Promise<URL> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode("verifier-".repeat(6)));
  const challenge = btoa(String.fromCharCode(...new Uint8Array(digest)))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
  const query = new URLSearchParams({
    client_id: clientId,
    redirect_uri: redirectUri,
    response_type: "code",
    scope: "openid",
    state: "opaque-state",
    code_challenge: challenge,
    code_challenge_method: "S256",
    resource: `${TEST_BASE_URL}/mcp`,
  });
  const res = await fetcher(`/api/auth/oauth2/authorize?${query}`, { headers });
  const location = res.headers.get("location");
  if (location) return new URL(location, TEST_BASE_URL);
  // Better Auth answers a non-browser client with the target in the body.
  const body = (await res.json()) as { url?: string };
  if (!body.url) throw new Error(`no redirect in a ${res.status} answer: ${JSON.stringify(body)}`);
  return new URL(body.url, TEST_BASE_URL);
}

describe("the consent page's signed query", () => {
  it("carries the redirect_uri the authorization will be sent to, which the page shows as a host", async () => {
    const { fetcher, services } = nodeWorker();
    const clientId = await registerClient(fetcher, "https://attacker.example/cb");
    const { headers } = await createTestSession(services.auth, "consent-user");
    const target = await authorize(fetcher, headers, clientId, "https://attacker.example/cb");
    expect(target.pathname).toBe("/consent");
    expect(target.searchParams.get("client_id")).toBe(clientId);
    expect(target.searchParams.get("sig")).toBeTruthy();
    // What routes/consent.tsx's beforeLoad derives from location.searchStr, next to the self-declared "Claude".
    expect(redirectUriHost(target.searchParams.get("redirect_uri"))).toBe("attacker.example");
  });

  it("is never reached for a host outside MCP_ALLOWED_REDIRECT_HOSTS, while an allowed host still gets consent", async () => {
    const auth = memoryAuth(fakeEnv(), { allowedRedirectHosts: parseAllowedRedirectHosts("claude.ai") });
    const { fetcher, services } = nodeWorker({ auth });
    const { headers } = await createTestSession(services.auth, "allowlist-user");

    const attacker = await registerClient(fetcher, "https://attacker.example/cb"); // registration stays open
    const refused = await authorize(fetcher, headers, attacker, "https://attacker.example/cb");
    expect(refused.pathname).not.toBe("/consent");
    expect(refused.searchParams.get("error")).toBe("invalid_redirect");

    const claude = await registerClient(fetcher, "https://claude.ai/api/mcp/auth_callback");
    const allowed = await authorize(fetcher, headers, claude, "https://claude.ai/api/mcp/auth_callback");
    expect(allowed.pathname).toBe("/consent");
    expect(redirectUriHost(allowed.searchParams.get("redirect_uri"))).toBe("claude.ai");
  });
});
