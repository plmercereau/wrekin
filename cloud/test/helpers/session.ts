import type { Auth } from "../../src/auth";

/**
 * A signed-in user for the tests: the user and session rows are written through Better
 * Auth's internal adapter (social sign-in cannot run offline), and the session token is
 * presented as a bearer token, which the bearer plugin accepts in place of the cookie.
 */
export async function createTestSession(auth: Auth, label: string) {
  const ctx = await auth.$context;
  const user = await ctx.internalAdapter.createUser(
    { email: `${label}-${crypto.randomUUID()}@example.test`, name: label, emailVerified: true },
    { method: "admin" },
  );
  const session = await ctx.internalAdapter.createSession(user.id);
  return { userId: user.id, headers: { authorization: `Bearer ${session.token}` } };
}
