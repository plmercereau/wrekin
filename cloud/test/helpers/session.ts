import type { Auth } from "../../src/auth";
import { encodeBase64 } from "../../src/crypto";

export type TestSession = {
  userId: string;
  /** The session token as a terminal presents it (the device-flow path of the Go agent). */
  headers: Record<string, string>;
  /** A second session of the same user as a browser presents it: the signed session cookie. */
  cookieHeaders: Record<string, string>;
};

/**
 * A signed-in user for the tests: the user and session rows are written through Better
 * Auth's internal adapter (social sign-in cannot run offline). Two sessions are opened:
 * one presented as a bearer token, which the bearer plugin accepts in place of the cookie,
 * one as the cookie itself, since the node API treats the two differently.
 */
export async function createTestSession(auth: Auth, label: string): Promise<TestSession> {
  const ctx = await auth.$context;
  const user = await ctx.internalAdapter.createUser(
    { email: `${label}-${crypto.randomUUID()}@example.test`, name: label, emailVerified: true },
    { method: "admin" },
  );
  const terminal = await ctx.internalAdapter.createSession(user.id);
  const browser = await ctx.internalAdapter.createSession(user.id);
  return {
    userId: user.id,
    headers: { authorization: `Bearer ${terminal.token}` },
    cookieHeaders: { cookie: `${ctx.authCookies.sessionToken.name}=${await signedCookieValue(browser.token, ctx.secret)}` },
  };
}

/** `token.signature`, URL-encoded, as better-call writes a signed cookie (HMAC-SHA256 over the token, base64). */
async function signedCookieValue(token: string, secret: string): Promise<string> {
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const signature = new Uint8Array(await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(token)));
  return encodeURIComponent(`${token}.${encodeBase64(signature)}`);
}
