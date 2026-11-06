// The redirect helpers, free of any React or router import so that they load in every
// runtime the tests use (the Workers pool cannot resolve react/jsx-runtime for a plain
// module); the route guard in session-guard.ts re-exports them.

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
