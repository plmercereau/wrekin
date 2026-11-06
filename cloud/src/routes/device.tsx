import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { authClient, errorMessage } from "../client/auth";
import { Status } from "../components";
import { getDeviceStatus } from "../server/pages";
import { requireUser } from "../session-guard";

export const Route = createFileRoute("/device")({
  beforeLoad: ({ context, location }) => {
    const user = requireUser(context.user, location.href);
    const raw = new URLSearchParams(location.searchStr).get("user_code")?.trim();
    // Codes are printed grouped (ABCD-EFGH) and typed in any case.
    const userCode = raw ? raw.replace(/[^a-zA-Z0-9]/g, "").toUpperCase() : null;
    return { user, userCode };
  },
  loader: ({ context }) => (context.userCode ? getDeviceStatus({ data: context.userCode }) : null),
  head: () => ({ meta: [{ title: "Pair a machine · Wrekin" }] }),
  component: Device,
});

type Decision = "approve" | "deny";

/** The page `wrekin auth` sends the user to; opened from a phone as often as not. */
function Device() {
  const { userCode } = Route.useRouteContext();
  const status = Route.useLoaderData();
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState<Decision | null>(null);
  const [error, setError] = useState<string | null>(null);

  if (!userCode) {
    return (
      <>
        <h1>Pair a machine</h1>
        <p>
          Type the code <code>wrekin auth</code> printed in your terminal.
        </p>
        <form method="get" action="/device" className="card">
          <label htmlFor="user_code" className="muted">
            Code
          </label>
          <input id="user_code" name="user_code" className="code-input" autoComplete="off" autoCapitalize="characters" spellCheck={false} />
          <button className="primary" type="submit">
            Continue
          </button>
        </form>
      </>
    );
  }

  if (status === "unknown" || status === null) {
    return (
      <>
        <h1>Pair a machine</h1>
        <div className="card">
          <p>
            The code <strong>{userCode}</strong> is not known or has expired.
          </p>
          <p className="muted">
            Codes last 15 minutes. Run <code>wrekin auth</code> again and open the new link.
          </p>
          <p>
            <a className="button" href="/device">
              Type another code
            </a>
          </p>
        </div>
      </>
    );
  }

  if (status !== "pending") {
    return (
      <>
        <h1>Pair a machine</h1>
        <div className="card">
          <p>
            The code <strong>{userCode}</strong> was already {status}.
          </p>
          <p className="muted">
            {status === "approved" ? "The agent should be paired; check with wrekin status." : "Run wrekin auth again if you changed your mind."}
          </p>
        </div>
      </>
    );
  }

  const decide = async (decision: Decision) => {
    setBusy(true);
    setError(null);
    const { error: failure } =
      decision === "approve" ? await authClient.device.approve({ userCode }) : await authClient.device.deny({ userCode });
    setBusy(false);
    if (failure) {
      setError(`That did not work: ${errorMessage(failure)}`);
      return;
    }
    setDone(decision);
  };

  return (
    <>
      <h1>Pair a machine</h1>
      <p>
        A <strong>Wrekin agent</strong> is asking to join your account. Approve only if this code is the one shown in your
        terminal.
      </p>
      <div className="card">
        <div className="code">{userCode}</div>
        {done ? (
          <p>
            {done === "approve"
              ? "Approved. You can return to the terminal: the agent finishes pairing by itself."
              : "Denied. Nothing was paired; you can close this page."}
          </p>
        ) : (
          <>
            <div className="actions">
              <button id="approve" className="primary" disabled={busy} onClick={() => void decide("approve")}>
                Approve
              </button>
              <button id="deny" className="danger" disabled={busy} onClick={() => void decide("deny")}>
                Deny
              </button>
            </div>
            <Status text={error ?? (busy ? "Working…" : null)} error={error !== null} />
          </>
        )}
      </div>
      <p className="muted">
        Approving lets this machine register itself with its own key. It never receives your credentials; the relay never
        receives its private key.
      </p>
    </>
  );
}
