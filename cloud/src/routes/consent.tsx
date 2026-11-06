import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { authClient, errorMessage, signedOAuthQuery } from "../client/auth";
import { ErrorPage, Status } from "../components";
import { redirectUriHost } from "../oauth-redirects";
import { getConsentClient } from "../server/pages";
import { requireUser } from "../session-guard";

export const Route = createFileRoute("/consent")({
  beforeLoad: ({ context, location }) => {
    const user = requireUser(context.user, location.href);
    const query = new URLSearchParams(location.searchStr);
    return {
      user,
      clientId: query.get("client_id"),
      // From the provider's signed query: the one claim about the requester the user can verify.
      redirectHost: redirectUriHost(query.get("redirect_uri")),
      scopes: (query.get("scope") ?? "").split(" ").filter(Boolean),
    };
  },
  loader: ({ context }) => (context.clientId ? getConsentClient({ data: context.clientId }) : null),
  head: () => ({ meta: [{ title: "Allow access · Wrekin" }] }),
  component: Consent,
});

/** The OAuth consent page the MCP authorization server redirects to (Claude connecting to /mcp). */
function Consent() {
  const { clientId, redirectHost, scopes } = Route.useRouteContext();
  const client = Route.useLoaderData();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!clientId || !client) {
    return <ErrorPage title="Something is missing" message="This page is only reached from an application asking for access." />;
  }

  const decide = async (accept: boolean) => {
    setBusy(true);
    setError(null);
    // The signed query goes back exactly as the provider wrote it in the URL: the router's
    // parsed search would turn the repeated `ba_param` into a JSON array and break `sig`.
    const { data, error: failure } = await authClient.oauth2.consent({ accept, oauth_query: signedOAuthQuery() });
    if (failure) {
      setBusy(false);
      setError(`That did not work: ${errorMessage(failure)}`);
      return;
    }
    // The provider answers with where to go next; the client follows it by itself, this covers one configured not to.
    if (data?.url) window.location.href = data.url;
  };

  return (
    <>
      <h1>Allow access?</h1>
      <p>
        <strong>{client.name}</strong>
        {client.uri ? <span className="muted"> ({client.uri})</span> : null} wants to use Wrekin as you.
      </p>
      <p>
        {redirectHost ? (
          <>
            Wrekin will send the authorization to <strong id="redirect-host">{redirectHost}</strong>. The name and link above are what the
            application declared about itself; the address is where it receives your access, so allow only if that is the
            application you meant to connect.
          </>
        ) : (
          <>The name above is what the application declared about itself; this request does not say where it receives your access.</>
        )}
      </p>
      <div className="card">
        <p>
          Allowing lets the application start Claude Code sessions on the machines you paired with Wrekin and read the state of
          the jobs it started. It will be able to:
        </p>
        <ul>
          <li>list your machines and whether they are connected,</li>
          <li>teleport a Claude Code session to one of them and follow its progress,</li>
          <li>confirm the use of a new repository when a teleport asks for it.</li>
        </ul>
        {scopes.length ? (
          <p className="muted">
            Requested scopes: <code>{scopes.join(" ")}</code>
          </p>
        ) : null}
        <div className="actions">
          <button id="approve" className="primary" disabled={busy} onClick={() => void decide(true)}>
            Allow
          </button>
          <button id="deny" disabled={busy} onClick={() => void decide(false)}>
            Deny
          </button>
        </div>
        <Status text={error ?? (busy ? "Working…" : null)} error={error !== null} />
      </div>
      <p className="muted">
        It never sees your machines' keys or terminal output, only the structured answers the agent sends back.
      </p>
    </>
  );
}
