import { oauthProviderAuthServerMetadata, oauthProviderOpenIdConfigMetadata } from "@better-auth/oauth-provider";
import { Hono } from "hono";
import { nodesApi } from "./api/nodes";
import { connect } from "./connect";
import type { Env } from "./env";
import { layoutError, pageRoutes } from "./pages/routes";
import { servicesFor } from "./services";

export type AppEnv = { Bindings: Env };

/** Machine clients talk to these prefixes and get JSON errors; browsers get HTML. */
const wantsJson = (path: string) => path.startsWith("/api/") || path === "/mcp" || path.startsWith("/mcp/");

/** The Hono application; kept apart from the Durable Object export so plain Node can host it. */
export function createApp() {
  const app = new Hono<AppEnv>();

  // Better Auth owns everything under /api/auth, before any other route. Its device
  // token endpoint only reads JSON while RFC 8628 clients post a form, so a form
  // body on that one path is re-encoded as JSON first.
  app.post("/api/auth/device/token", async (c) => {
    const contentType = c.req.header("content-type") ?? "";
    if (!contentType.toLowerCase().startsWith("application/x-www-form-urlencoded")) {
      return servicesFor(c.env).auth.handler(c.req.raw);
    }
    const form = await c.req.formData();
    const headers = new Headers(c.req.raw.headers);
    headers.set("content-type", "application/json");
    const asJson = new Request(c.req.raw.url, {
      method: "POST",
      headers,
      body: JSON.stringify(Object.fromEntries(form.entries())),
    });
    return servicesFor(c.env).auth.handler(asJson);
  });
  app.all("/api/auth/*", (c) => servicesFor(c.env).auth.handler(c.req.raw));

  // OAuth discovery lives at the root of the origin (RFC 8414, RFC 9728, OIDC), not
  // under the auth base path: the authorization-server documents come from the
  // provider's helpers, the protected-resource document from the MCP plugin itself.
  app.get("/.well-known/oauth-authorization-server", (c) =>
    oauthProviderAuthServerMetadata(servicesFor(c.env).auth)(c.req.raw),
  );
  app.get("/.well-known/oauth-authorization-server/*", (c) =>
    oauthProviderAuthServerMetadata(servicesFor(c.env).auth)(c.req.raw),
  );
  app.get("/.well-known/openid-configuration", (c) =>
    oauthProviderOpenIdConfigMetadata(servicesFor(c.env).auth)(c.req.raw),
  );
  app.get("/.well-known/openid-configuration/*", (c) =>
    oauthProviderOpenIdConfigMetadata(servicesFor(c.env).auth)(c.req.raw),
  );
  app.on(["GET", "HEAD"], "/.well-known/oauth-protected-resource", (c) =>
    servicesFor(c.env).auth.handler(c.req.raw),
  );
  app.on(["GET", "HEAD"], "/.well-known/oauth-protected-resource/*", (c) =>
    servicesFor(c.env).auth.handler(c.req.raw),
  );

  app.route("/api/nodes", nodesApi());

  // The nodes' WebSocket.
  app.get("/connect", connect);

  app.route("/", pageRoutes());

  app.notFound((c) =>
    wantsJson(new URL(c.req.url).pathname)
      ? c.json({ error: "not_found", message: `no route for ${c.req.method} ${new URL(c.req.url).pathname}` }, 404)
      : c.html(layoutError("There is nothing at this address.", "Not found"), 404),
  );

  app.onError((error, c) => {
    console.error("unhandled error", error instanceof Error ? error.stack ?? error.message : String(error));
    return wantsJson(new URL(c.req.url).pathname)
      ? c.json({ error: "internal", message: "something went wrong on the relay" }, 500)
      : c.html(layoutError("Something went wrong on the relay. Please try again.", "Error"), 500);
  });

  return app;
}
