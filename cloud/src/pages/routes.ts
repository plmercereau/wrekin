import { Hono } from "hono";
import { html } from "hono/html";
import { toNodeView } from "../api/nodes";
import type { AppEnv } from "../app";
import { servicesFor } from "../services";
import { consentPage, type ConsentClient } from "./consent";
import { devicePage, type DeviceStatus } from "./device";
import { homePage } from "./home";
import { layout } from "./layout";
import { loginPage } from "./login";
import { nodesPage } from "./nodes";

/** `/login?redirect=<where the visitor was going>`. */
export const loginRedirect = (target: string) => `/login?redirect=${encodeURIComponent(target)}`;

/** The path and query of the current request, as a relative URL. */
const currentPath = (url: string) => {
  const u = new URL(url);
  return `${u.pathname}${u.search}`;
};

/** Only same-origin relative paths are accepted as redirect targets. */
const safeRedirect = (value: string | undefined, fallback: string) =>
  value && value.startsWith("/") && !value.startsWith("//") ? value : fallback;

export function pageRoutes() {
  const pages = new Hono<AppEnv>();

  const sessionUser = async (headers: Headers, env: AppEnv["Bindings"]) => {
    const session = await servicesFor(env).auth.api.getSession({ headers });
    return session ? { id: session.user.id, name: session.user.name } : null;
  };

  // Calls a Better Auth endpoint on behalf of the visitor (cookies forwarded) while rendering.
  const callAuth = async (env: AppEnv["Bindings"], headers: Headers, path: string, init?: { method?: "GET" | "POST"; body?: unknown }) => {
    const forwarded = new Headers();
    const cookie = headers.get("cookie");
    if (cookie) forwarded.set("cookie", cookie);
    forwarded.set("origin", env.BASE_URL);
    if (init?.body !== undefined) forwarded.set("content-type", "application/json");
    const request = new Request(`${env.BASE_URL}/api/auth${path}`, {
      method: init?.method ?? "GET",
      headers: forwarded,
      body: init?.body !== undefined ? JSON.stringify(init.body) : undefined,
    });
    return servicesFor(env).auth.handler(request);
  };

  pages.get("/", async (c) => c.html(homePage(c.env.BASE_URL, await sessionUser(c.req.raw.headers, c.env))));

  pages.get("/login", async (c) => {
    const user = await sessionUser(c.req.raw.headers, c.env);
    const query = new URL(c.req.url).searchParams;
    const oauthQuery = query.has("sig") ? query.toString() : null;
    return c.html(loginPage({ redirect: safeRedirect(query.get("redirect") ?? undefined, "/nodes"), oauthQuery, user }));
  });

  pages.get("/device", async (c) => {
    const user = await sessionUser(c.req.raw.headers, c.env);
    if (!user) return c.redirect(loginRedirect(currentPath(c.req.url)), 302);
    const raw = c.req.query("user_code")?.trim();
    if (!raw) return c.html(devicePage({ userCode: null, status: "pending", user }));
    const userCode = raw.replace(/[^a-zA-Z0-9]/g, "").toUpperCase();
    let status: DeviceStatus = "unknown";
    const res = await callAuth(c.env, c.req.raw.headers, `/device?user_code=${encodeURIComponent(userCode)}`);
    if (res.ok) {
      const body = (await res.json().catch(() => null)) as { status?: string } | null;
      if (body?.status === "pending" || body?.status === "approved" || body?.status === "denied") status = body.status;
    }
    return c.html(devicePage({ userCode, status, user }));
  });

  pages.get("/consent", async (c) => {
    const user = await sessionUser(c.req.raw.headers, c.env);
    if (!user) return c.redirect(loginRedirect(currentPath(c.req.url)), 302);
    const query = new URL(c.req.url).searchParams;
    const clientId = query.get("client_id");
    if (!clientId) return c.html(layoutError("This page is only reached from an application asking for access."), 400);
    const oauthQuery = query.toString();
    let client: ConsentClient = { name: clientId };
    const res = await callAuth(c.env, c.req.raw.headers, "/oauth2/public-client", {
      method: "POST",
      body: { client_id: clientId, oauth_query: oauthQuery },
    });
    if (res.ok) {
      const body = (await res.json().catch(() => null)) as { client_name?: string; name?: string; client_uri?: string; uri?: string } | null;
      client = { name: body?.client_name ?? body?.name ?? clientId, uri: body?.client_uri ?? body?.uri ?? null };
    }
    const scopes = (query.get("scope") ?? "").split(" ").filter(Boolean);
    return c.html(consentPage({ client, scopes, oauthQuery, user }));
  });

  pages.get("/nodes", async (c) => {
    const user = await sessionUser(c.req.raw.headers, c.env);
    if (!user) return c.redirect(loginRedirect("/nodes"), 302);
    const { nodes, hub } = servicesFor(c.env);
    const [rows, online] = await Promise.all([nodes.listNodes(user.id), hub(user.id).online()]);
    return c.html(nodesPage({ nodes: await Promise.all(rows.map((row) => toNodeView(row, online))), user }));
  });

  return pages;
}

/** A plain error page in the shared layout. */
export function layoutError(message: string, title = "Something is missing") {
  return layout({ title }, html`<h1>${title}</h1><p>${message}</p><p><a href="/">Back to the front page</a></p>`);
}
