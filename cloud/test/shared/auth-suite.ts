import { describe, expect, it } from "vitest";

/** A function that performs a request against the Worker, however it is hosted in the test run. */
export type Fetcher = (path: string, init?: RequestInit) => Promise<Response>;

/** Assertions shared by the plain-Node and the workerd runs of the auth configuration. */
export function authSuite(fetcher: Fetcher, baseURL: string) {
  describe("OAuth discovery", () => {
    it("publishes the protected resource metadata with the MCP endpoint as resource", async () => {
      for (const path of ["/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"]) {
        const res = await fetcher(path);
        expect(res.status, path).toBe(200);
        const body = (await res.json()) as { resource: string; authorization_servers: string[] };
        expect(body.resource).toBe(`${baseURL}/mcp`);
        expect(body.authorization_servers.length).toBeGreaterThan(0);
      }
    });

    it("publishes the authorization server metadata at the root and at the issuer path", async () => {
      for (const path of ["/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server/api/auth"]) {
        const res = await fetcher(path);
        expect(res.status, path).toBe(200);
        const body = (await res.json()) as Record<string, unknown>;
        expect(body.authorization_endpoint).toBe(`${baseURL}/api/auth/oauth2/authorize`);
        expect(body.token_endpoint).toBe(`${baseURL}/api/auth/oauth2/token`);
        expect(body.registration_endpoint).toBe(`${baseURL}/api/auth/oauth2/register`);
      }
    });

    it("serves the JWKS used to verify MCP access tokens", async () => {
      const res = await fetcher("/api/auth/jwks");
      expect(res.status).toBe(200);
      const body = (await res.json()) as { keys: unknown[] };
      expect(Array.isArray(body.keys)).toBe(true);
    });

    it("has the generic /token endpoint disabled, as the MCP plugin requires", async () => {
      const res = await fetcher("/api/auth/token");
      expect(res.status).toBe(404);
    });
  });

  describe("device authorization", () => {
    it("issues a device code to the Wrekin agent", async () => {
      const res = await fetcher("/api/auth/device/code", {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ client_id: "wrekin-agent", scope: "node" }),
      });
      expect(res.status).toBe(200);
      const body = (await res.json()) as Record<string, unknown>;
      expect(body.user_code).toMatch(/^[A-Z0-9]{8}$/);
      expect(body.verification_uri).toBe(`${baseURL}/device`);
      expect(body.verification_uri_complete).toBe(`${baseURL}/device?user_code=${body.user_code}`);
      expect(body.interval).toBe(5);
      expect(typeof body.device_code).toBe("string");
    });

    it("refuses any other client", async () => {
      const res = await fetcher("/api/auth/device/code", {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ client_id: "other" }),
      });
      expect(res.status).toBeGreaterThanOrEqual(400);
      expect(res.status).toBeLessThan(500);
      const body = (await res.json()) as { error: string };
      expect(body.error).toBeTruthy();
    });

    it("polls as pending until the user approves, with a form body (RFC 8628) or JSON", async () => {
      // One device code per poll: a second poll within `interval` is answered `slow_down`.
      const newCode = async () => {
        const res = await fetcher("/api/auth/device/code", {
          method: "POST",
          headers: { "content-type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({ client_id: "wrekin-agent" }),
        });
        return ((await res.json()) as { device_code: string }).device_code;
      };
      const params = (device_code: string) => ({
        grant_type: "urn:ietf:params:oauth:grant-type:device_code",
        device_code,
        client_id: "wrekin-agent",
      });
      const asForm = await fetcher("/api/auth/device/token", {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams(params(await newCode())),
      });
      expect(asForm.status).toBe(400);
      expect(((await asForm.json()) as { error: string }).error).toBe("authorization_pending");

      const asJson = await fetcher("/api/auth/device/token", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(params(await newCode())),
      });
      expect(asJson.status).toBe(400);
      expect(((await asJson.json()) as { error: string }).error).toBe("authorization_pending");
    });
  });
}
