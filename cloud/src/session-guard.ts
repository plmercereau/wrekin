import { redirect } from "@tanstack/react-router";
import type { SessionUser } from "./trpc/context";

/** `/login?redirect=<where the visitor was going>`. */
export const loginRedirect = (target: string) => `/login?redirect=${encodeURIComponent(target)}`;

/** A fictional origin to resolve candidates against: a same-origin path keeps it, anything else leaves it. */
const PROBE_ORIGIN = "https://wrekin.invalid";
const CONTROL_CHARACTERS = /[\u0000-\u001f\u007f]/;

/**
 * Only same-origin relative paths are accepted as redirect targets; anything else yields
 * `fallback`. A single leading `/` is required (`//host` is scheme-relative), backslashes
 * are refused (WHATWG URL parsing reads `/\host` as `//host`), so are control characters
 * and an encoded `/` or `\` in the path (`%2f`, `%5c`), and the value must resolve on the
 * origin it was given. The query string of an accepted path is kept.
 */
export function safeRedirect(value: string | null | undefined, fallback: string): string {
  if (typeof value !== "string" || !value.startsWith("/") || value.startsWith("//")) return fallback;
  if (value.includes("\\") || CONTROL_CHARACTERS.test(value)) return fallback;
  const path = value.split(/[?#]/, 1)[0] ?? "";
  if (/%2f|%5c/i.test(path)) return fallback;
  try {
    if (new URL(value, PROBE_ORIGIN).origin !== PROBE_ORIGIN) return fallback;
  } catch {
    return fallback;
  }
  return value;
}

/**
 * For a session-gated route's `beforeLoad`: the user the root route read, or a 302 to the
 * login page that comes back to `href` (path and query of the current location).
 */
export function requireUser(user: SessionUser | null, href: string): SessionUser {
  if (!user) throw redirect({ href: loginRedirect(href), statusCode: 302 });
  return user;
}
