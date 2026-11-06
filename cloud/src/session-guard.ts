import { redirect } from "@tanstack/react-router";
import { loginRedirect } from "./redirects";
import type { SessionUser } from "./trpc/context";

export { loginRedirect, safeRedirect } from "./redirects";

/**
 * For a session-gated route's `beforeLoad`: the user the root route read, or a 302 to the
 * login page that comes back to `href` (path and query of the current location).
 */
export function requireUser(user: SessionUser | null, href: string): SessionUser {
  if (!user) throw redirect({ href: loginRedirect(href), statusCode: 302 });
  return user;
}
