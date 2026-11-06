import { env, SELF } from "cloudflare:test";
import { describe, expect, it } from "vitest";
import { createAuth } from "../src/auth";
import { generateNodeKey } from "./helpers/keys";
import { createTestSession } from "./helpers/session";

// The pages TanStack Start renders inside workerd, from the built Worker (dist/server).
const page = (path: string, init?: RequestInit) => SELF.fetch(`${env.BASE_URL}${path}`, { redirect: "manual", ...init });
const newSession = (label: string) => createTestSession(createAuth(env), label);

describe("pages", () => {
  it("explains how to connect Claude on the front page", async () => {
    const res = await page("/");
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toContain("text/html");
    const body = await res.text();
    expect(body).toContain(`${env.BASE_URL}/mcp`);
    expect(body).toContain("brew install plmercereau/tap/wrekin");
    expect(body).toContain('name="viewport"');
    expect(body).not.toMatch(/<script[^>]+src="https?:/); // nothing loaded from a CDN
    expect(body).not.toMatch(/<link[^>]+href="https?:/);
  });

  it("offers GitHub and Google on the login page", async () => {
    const res = await page("/login?redirect=/nodes");
    expect(res.status).toBe(200);
    const body = await res.text();
    expect(body).toContain('data-provider="github"');
    expect(body).toContain('data-provider="google"');
  });

  it("sends an anonymous visitor of /device to the login page and back", async () => {
    const res = await page("/device?user_code=ABCD-EFGH");
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe("/login?redirect=%2Fdevice%3Fuser_code%3DABCD-EFGH");
  });

  it("sends an anonymous visitor of /nodes and /consent to the login page", async () => {
    const nodes = await page("/nodes");
    expect(nodes.status).toBe(302);
    expect(nodes.headers.get("location")).toBe("/login?redirect=%2Fnodes");
    const consent = await page("/consent?client_id=abc&scope=openid");
    expect(consent.status).toBe(302);
    expect(consent.headers.get("location")).toContain("/login?redirect=%2Fconsent");
  });

  it("shows a pending device code to a signed-in user with Approve and Deny", async () => {
    const { headers } = await newSession("device-user");
    const issued = await page("/api/auth/device/code", {
      method: "POST",
      headers: { "content-type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({ client_id: "wrekin-agent" }),
    });
    const { user_code } = (await issued.json()) as { user_code: string };
    const res = await page(`/device?user_code=${user_code.slice(0, 4)}-${user_code.slice(4).toLowerCase()}`, { headers });
    expect(res.status).toBe(200);
    const body = await res.text();
    expect(body).toContain(user_code);
    expect(body).toContain('id="approve"');
    expect(body).toContain('id="deny"');
    expect(body).toContain("Wrekin agent");
  });

  it("tells a signed-in user when a device code is unknown", async () => {
    const { headers } = await newSession("device-user-2");
    const res = await page("/device?user_code=NOPE0000", { headers });
    expect(res.status).toBe(200);
    expect(await res.text()).toContain("not known or has expired");
  });

  it("lists the signed-in user's nodes with fingerprint and state", async () => {
    const { headers } = await newSession("nodes-user");
    const key = await generateNodeKey();
    const created = await page("/api/nodes", {
      method: "POST",
      headers: { "content-type": "application/json", ...headers },
      body: JSON.stringify({ name: "kitchen-mac", publicKey: key.publicKey }),
    });
    expect(created.status).toBe(201);
    const res = await page("/nodes", { headers });
    expect(res.status).toBe(200);
    const body = await res.text();
    expect(body).toContain("kitchen-mac");
    expect(body).toMatch(/[0-9a-f]{4}(:[0-9a-f]{4}){3}/);
    expect(body).toContain("offline");
    expect(body).toContain('data-remove="node_');
  });

  it("escapes what comes from the database", async () => {
    const { headers } = await newSession("xss-user");
    await page("/api/nodes", {
      method: "POST",
      headers: { "content-type": "application/json", ...headers },
      body: JSON.stringify({ name: "<img src=x onerror=alert(1)>", publicKey: (await generateNodeKey()).publicKey }),
    });
    const body = await (await page("/nodes", { headers })).text();
    expect(body).not.toContain("<img src=x");
    expect(body).toContain("&lt;img src=x");
  });

  it("answers 404 as HTML for an unknown page and as JSON under /api", async () => {
    const html = await page("/nothing");
    expect(html.status).toBe(404);
    expect(html.headers.get("content-type")).toContain("text/html");
    expect(await html.text()).toContain("There is nothing at this address.");
    const api = await page("/api/nothing");
    expect(api.status).toBe(404);
    expect(api.headers.get("content-type")).toContain("application/json");
  });
});

describe("tRPC over HTTP", () => {
  it("answers session.me with null for an anonymous visitor", async () => {
    const res = await page("/api/trpc/session.me");
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ result: { data: null } });
  });

  it("answers session.me with the bearer of the session token", async () => {
    const { userId, headers } = await newSession("trpc-user");
    const res = await page("/api/trpc/session.me", { headers });
    expect(res.status).toBe(200);
    expect(await res.json()).toMatchObject({ result: { data: { id: userId, name: "trpc-user" } } });
  });

  it("refuses nodes.list without a session with UNAUTHORIZED", async () => {
    const res = await page("/api/trpc/nodes.list");
    expect(res.status).toBe(401);
    expect(await res.json()).toMatchObject({ error: { data: { code: "UNAUTHORIZED" } } });
  });
});
