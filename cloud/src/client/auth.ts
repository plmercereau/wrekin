import { oauthProviderClient } from "@better-auth/oauth-provider/client";
import { deviceAuthorizationClient } from "better-auth/client/plugins";
import { createAuthClient } from "better-auth/react";

/**
 * The browser's Better Auth client: social sign-in, device approval and OAuth consent. Same
 * origin, so no baseURL. The provider's client plugin types the oauth2 calls and, when the
 * page URL carries the provider's signed query (`sig`), adds it to requests as `oauth_query`.
 */
export const authClient = createAuthClient({ plugins: [deviceAuthorizationClient(), oauthProviderClient()] });

/** The message of a failed client call, for the status line. */
export const errorMessage = (error: unknown): string => {
  if (error && typeof error === "object") {
    const e = error as { error_description?: string; message?: string; error?: string; status?: number; statusText?: string };
    return e.error_description ?? e.message ?? e.error ?? (e.status ? `HTTP ${e.status}` : "unknown error");
  }
  return error instanceof Error ? error.message : String(error);
};

/**
 * The provider's signed query as it stands in the page URL, for `oauth_query`. Read from
 * `window.location.search`, never from the router's parsed search: TanStack Router turns a
 * repeated parameter (the provider's `ba_param`) into a JSON array, and the signature no
 * longer matches.
 */
export function signedOAuthQuery(): string {
  return typeof window === "undefined" ? "" : window.location.search.replace(/^\?/, "");
}
