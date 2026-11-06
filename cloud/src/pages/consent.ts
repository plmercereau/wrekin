import { html } from "hono/html";
import { layout, type PageOptions } from "./layout";

export type ConsentClient = { name: string; uri?: string | null };

/** The OAuth consent page the MCP authorization server redirects to (Claude connecting to /mcp). */
export function consentPage(options: { client: ConsentClient; scopes: string[]; oauthQuery: string; user: PageOptions["user"] }) {
  const script = `
const oauthQuery = ${JSON.stringify(options.oauthQuery)};
const statusEl = document.getElementById("status");
const buttons = document.getElementById("buttons");
async function decide(accept) {
  for (const b of buttons.querySelectorAll("button")) b.disabled = true;
  statusEl.textContent = accept ? "Granting access…" : "Declining…";
  statusEl.className = "status";
  try {
    const res = await fetch("/api/auth/oauth2/consent", {
      method: "POST", credentials: "same-origin",
      headers: { "content-type": "application/json" }, body: JSON.stringify({ accept, oauth_query: oauthQuery }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok || !data.url) throw new Error(data.error_description || data.message || data.error || ("HTTP " + res.status));
    window.location.href = data.url;
  } catch (error) {
    for (const b of buttons.querySelectorAll("button")) b.disabled = false;
    statusEl.textContent = "That did not work: " + error.message;
    statusEl.className = "status error";
  }
}
document.getElementById("approve").addEventListener("click", () => decide(true));
document.getElementById("deny").addEventListener("click", () => decide(false));`;
  return layout(
    { title: "Allow access", user: options.user, script },
    html`
<h1>Allow access?</h1>
<p><strong>${options.client.name}</strong>${options.client.uri ? html` <span class="muted">(${options.client.uri})</span>` : ""} wants to use Wrekin as you.</p>
<div class="card">
  <p>It will be able to:</p>
  <ul>
    <li>list your machines and whether they are connected,</li>
    <li>teleport a Claude Code session to one of them and follow its progress,</li>
    <li>confirm the use of a new repository when a teleport asks for it.</li>
  </ul>
  ${options.scopes.length ? html`<p class="muted">Requested scopes: <code>${options.scopes.join(" ")}</code></p>` : ""}
  <div id="buttons" class="actions">
    <button id="approve" class="primary">Allow</button>
    <button id="deny">Deny</button>
  </div>
  <p id="status" class="status"></p>
</div>
<p class="muted">It never sees your machines' keys or terminal output, only the structured answers the agent sends back.</p>
`,
  );
}
