import { redirect } from "@tanstack/react-router";
import type { SessionUser } from "./trpc/context";

/** `/login?redirect=<where the visitor was going>`. */
export const loginRedirect = (target: string) => `/login?redirect=${encodeURIComponent(target)}`;

/** Only same-origin relative paths are accepted as redirect targets. */
export const safeRedirect = (value: string | null | undefined, fallback: string): string =>
  value && value.startsWith("/") && !value.startsWith("//") ? value : fallback;

/**
 * For a session-gated route's `beforeLoad`: the user the root route read, or a 302 to the
 * login page that comes back to `href` (path and query of the current location).
 */
export function requireUser(user: SessionUser | null, href: string): SessionUser {
  if (!user) throw redirect({ href: loginRedirect(href), statusCode: 302 });
  return user;
}
