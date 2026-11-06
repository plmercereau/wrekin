import { html } from "hono/html";
import { layout, type PageOptions } from "./layout";

export type DeviceStatus = "pending" | "approved" | "denied" | "unknown";

/** The page `wrekin auth` sends the user to; opened from a phone as often as not. */
export function devicePage(options: { userCode: string | null; status: DeviceStatus; user: PageOptions["user"] }) {
  const { userCode, status } = options;
  const script = userCode
    ? `
const userCode = ${JSON.stringify(userCode)};
const statusEl = document.getElementById("status");
const buttons = document.getElementById("buttons");
const done = document.getElementById("done");
async function decide(action) {
  for (const b of buttons.querySelectorAll("button")) b.disabled = true;
  statusEl.textContent = action === "approve" ? "Approving…" : "Denying…";
  statusEl.className = "status";
  try {
    const res = await fetch("/api/auth/device/" + action, {
      method: "POST", credentials: "same-origin",
      headers: { "content-type": "application/json" }, body: JSON.stringify({ userCode }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error_description || data.message || data.error || ("HTTP " + res.status));
    buttons.hidden = true;
    statusEl.textContent = "";
    done.hidden = false;
    done.textContent = action === "approve"
      ? "Approved. You can return to the terminal: the agent finishes pairing by itself."
      : "Denied. Nothing was paired; you can close this page.";
  } catch (error) {
    for (const b of buttons.querySelectorAll("button")) b.disabled = false;
    statusEl.textContent = "That did not work: " + error.message;
    statusEl.className = "status error";
  }
}
document.getElementById("approve").addEventListener("click", () => decide("approve"));
document.getElementById("deny").addEventListener("click", () => decide("deny"));`
    : undefined;

  const body = !userCode
    ? html`
<h1>Pair a machine</h1>
<p>Type the code <code>wrekin auth</code> printed in your terminal.</p>
<form method="get" action="/device" class="card">
  <label for="user_code" class="muted">Code</label>
  <input id="user_code" name="user_code" autocomplete="off" autocapitalize="characters" spellcheck="false" style="width:100%;font:1.4rem ui-monospace,monospace;letter-spacing:0.15em;padding:10px;margin:8px 0 14px;border:1px solid var(--line);border-radius:8px;background:transparent;color:inherit">
  <button class="primary" type="submit">Continue</button>
</form>`
    : status === "unknown"
      ? html`
<h1>Pair a machine</h1>
<div class="card">
  <p>The code <strong>${userCode}</strong> is not known or has expired.</p>
  <p class="muted">Codes last 15 minutes. Run <code>wrekin auth</code> again and open the new link.</p>
  <p><a class="button" href="/device">Type another code</a></p>
</div>`
      : status !== "pending"
        ? html`
<h1>Pair a machine</h1>
<div class="card">
  <p>The code <strong>${userCode}</strong> was already ${status}.</p>
  <p class="muted">${status === "approved" ? "The agent should be paired; check with wrekin status." : "Run wrekin auth again if you changed your mind."}</p>
</div>`
        : html`
<h1>Pair a machine</h1>
<p>A <strong>Wrekin agent</strong> is asking to join your account. Approve only if this code is the one shown in your terminal.</p>
<div class="card">
  <div class="code">${userCode}</div>
  <div id="buttons" class="actions">
    <button id="approve" class="primary">Approve</button>
    <button id="deny" class="danger">Deny</button>
  </div>
  <p id="status" class="status"></p>
  <p id="done" hidden></p>
</div>
<p class="muted">Approving lets this machine register itself with its own key. It never receives your credentials; the relay never receives its private key.</p>`;

  return layout({ title: "Pair a machine", user: options.user, script }, body);
}
