import { env, SELF } from "cloudflare:test";
import { describe, expect, it } from "vitest";

describe("POST /mcp in workerd", () => {
  it("challenges an unauthenticated request", async () => {
    const res = await SELF.fetch(`${env.BASE_URL}/mcp`, {
      method: "POST",
      headers: { "content-type": "application/json", accept: "application/json, text/event-stream" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
    });
    expect(res.status).toBe(401);
    expect(res.headers.get("www-authenticate")).toContain(`resource_metadata="${env.BASE_URL}/.well-known/oauth-protected-resource/mcp"`);
  });

  it("answers 405 to GET", async () => {
    expect((await SELF.fetch(`${env.BASE_URL}/mcp`)).status).toBe(405);
  });
});
