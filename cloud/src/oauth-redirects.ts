/**
 * Where an OAuth client receives its authorization. A client registered through Dynamic
 * Client Registration declares its own name and link, so the `redirect_uri` of the
 * authorization request is the one thing about it a user can verify on the consent page,
 * and the one thing the optional `MCP_ALLOWED_REDIRECT_HOSTS` bounds. Pure: the consent
 * route runs it in the browser too.
 */

/** `hostname[:port]` of a redirect URI, or null when it has no host (unparseable, `urn:`…). */
export function redirectUriHost(redirectUri: string | null | undefined): string | null {
  if (!redirectUri) return null;
  try {
    return new URL(redirectUri).host || null;
  } catch {
    return null;
  }
}

/** `MCP_ALLOWED_REDIRECT_HOSTS`: comma-separated host suffixes, lower-cased, a leading dot dropped; empty when unset. */
export function parseAllowedRedirectHosts(value: string | undefined): string[] {
  return (value ?? "")
    .split(",")
    .map((entry) => entry.trim().toLowerCase().replace(/^\.+/, ""))
    .filter(Boolean);
}

/** `claude.ai` allows `claude.ai` and every host under it; an empty list allows every host. */
export function isAllowedRedirectHost(hostname: string, allowed: readonly string[]): boolean {
  if (allowed.length === 0) return true;
  const host = hostname.toLowerCase();
  return allowed.some((suffix) => host === suffix || host.endsWith(`.${suffix}`));
}

export type RedirectUriValidator = (redirectUri: string, registeredUris: readonly string[], defaultResult: boolean) => boolean;

/**
 * The OAuth provider's `validateRedirectUri` for a non-empty allowlist: the URI must still
 * match one the client registered (`defaultResult`, exact matching per RFC 9700 §2.1) and its
 * hostname must end with an allowed suffix. Undefined for an empty list, so the provider's
 * default applies untouched.
 */
export function redirectUriValidator(allowed: readonly string[]): RedirectUriValidator | undefined {
  if (allowed.length === 0) return undefined;
  return (redirectUri, _registeredUris, defaultResult) => {
    if (!defaultResult) return false;
    try {
      return isAllowedRedirectHost(new URL(redirectUri).hostname, allowed);
    } catch {
      return false;
    }
  };
}
