import { mcp } from "@better-auth/mcp";
import { betterAuth, type BetterAuthOptions } from "better-auth";
import { bearer, deviceAuthorization, jwt } from "better-auth/plugins";
import { D1Dialect } from "kysely-d1";
import type { Env } from "./env";

/** The client id the Go agent presents to the device authorization endpoints. */
export const AGENT_CLIENT_ID = "wrekin-agent";

export type OAuthClient = { clientId: string; clientSecret: string };

export type AuthConfig = {
  /** Public origin, e.g. https://wrekin.platyplus.io; the MCP resource is `${baseURL}/mcp`. */
  baseURL: string;
  secret: string;
  database: NonNullable<BetterAuthOptions["database"]>;
  github: OAuthClient;
  google: OAuthClient;
};

/**
 * The Better Auth configuration, independent of where the database comes from so the
 * same function serves the Worker (D1), the tests (memory adapter) and the CLI that
 * generates `migrations/0001_auth.sql`.
 */
export function buildAuth(config: AuthConfig) {
  return betterAuth({
    appName: "Wrekin",
    baseURL: config.baseURL,
    secret: config.secret,
    database: config.database,
    socialProviders: {
      github: config.github,
      google: config.google,
    },
    account: {
      // The same person may sign in with GitHub one day and Google the next: one
      // Wrekin account, as the spec asks, when both providers verified the e-mail.
      accountLinking: { enabled: true, trustedProviders: ["github", "google"] },
    },
    plugins: [
      // The MCP OAuth provider signs access tokens with the jwt plugin's keys; the
      // header must stay off so ordinary sessions are not turned into JWTs.
      jwt({ disableSettingJwtHeader: true }),
      // `Authorization: Bearer <session token>` acts as the session cookie (the
      // node registration API is called from a terminal, not a browser).
      bearer(),
      // `wrekin auth`: the agent asks for a code, the user approves it on /device.
      deviceAuthorization({
        verificationUri: "/device",
        expiresIn: "15m",
        interval: "5s",
        userCodeLength: 8,
        validateClient: (clientId) => clientId === AGENT_CLIENT_ID,
      }),
      // Claude's remote MCP connector discovers this server through RFC 9728 and
      // registers itself with Dynamic Client Registration (RFC 7591): the MCP
      // plugin leaves registration off by default, and the Client ID Metadata
      // Documents alternative (@better-auth/cimd) relies on a Node fetch helper
      // that does not run on Workers, so DCR is enabled explicitly here.
      mcp({
        loginPage: "/login",
        consentPage: "/consent",
        resource: `${config.baseURL}/mcp`,
        allowDynamicClientRegistration: true,
        allowUnauthenticatedClientRegistration: true,
      }),
    ],
    // Required by the MCP plugin: the generic token endpoint must not shadow /oauth2/token.
    disabledPaths: ["/token"],
  });
}

export type Auth = ReturnType<typeof buildAuth>;

const cache = new WeakMap<D1Database, Auth>();

/** The Worker's Better Auth instance on D1, one per database binding. */
export function createAuth(env: Env): Auth {
  const cached = cache.get(env.DB);
  if (cached) return cached;
  const auth = buildAuth({
    baseURL: env.BASE_URL,
    secret: env.BETTER_AUTH_SECRET,
    database: { dialect: new D1Dialect({ database: env.DB }), type: "sqlite" },
    github: { clientId: env.GITHUB_CLIENT_ID, clientSecret: env.GITHUB_CLIENT_SECRET },
    google: { clientId: env.GOOGLE_CLIENT_ID, clientSecret: env.GOOGLE_CLIENT_SECRET },
  });
  cache.set(env.DB, auth);
  return auth;
}
