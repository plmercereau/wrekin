import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { authClient, errorMessage, signedOAuthQuery } from "../client/auth";
import { Status } from "../components";
import { safeRedirect } from "../session-guard";

export const Route = createFileRoute("/login")({
  // The raw query, not the parsed search: when the OAuth provider sent the visitor here, its
  // signed query (`sig`) must go back verbatim as `oauth_query` so the authorization resumes.
  beforeLoad: ({ location }) => {
    const query = new URLSearchParams(location.searchStr);
    return {
      redirect: safeRedirect(query.get("redirect"), "/nodes"),
      resumesAuthorization: query.has("sig"),
    };
  },
  head: () => ({ meta: [{ title: "Sign in · Wrekin" }] }),
  component: Login,
});

type Provider = "github" | "google";
type SocialBody = Parameters<typeof authClient.signIn.social>[0];

/** Two buttons, one per identity provider. `redirect` is where to land afterwards. */
function Login() {
  const { user, redirect, resumesAuthorization } = Route.useRouteContext();
  const [status, setStatus] = useState<{ text: string | null; error: boolean }>({ text: null, error: false });

  const signIn = async (provider: Provider) => {
    setStatus({ text: "Redirecting…", error: false });
    const body = { provider, callbackURL: redirect, ...(resumesAuthorization ? { oauth_query: signedOAuthQuery() } : {}) } as SocialBody;
    const { data, error } = await authClient.signIn.social(body);
    if (error) {
      setStatus({ text: `Sign-in failed: ${errorMessage(error)}`, error: true });
      return;
    }
    // The client follows `url` by itself; this covers a client configured not to.
    if (data?.url) window.location.href = data.url;
  };

  return (
    <>
      <h1>Sign in</h1>
      <p className="muted">
        Wrekin only needs to know who you are. Either account gives you the same Wrekin account when the e-mail matches.
      </p>
      {user ? (
        <p>
          You are signed in as <strong>{user.name}</strong>. <a href={redirect}>Continue</a> or pick another account below.
        </p>
      ) : null}
      <div className="actions">
        <button className="primary" data-provider="github" onClick={() => void signIn("github")}>
          Continue with GitHub
        </button>
        <button data-provider="google" onClick={() => void signIn("google")}>
          Continue with Google
        </button>
      </div>
      <Status text={status.text} error={status.error} />
    </>
  );
}
