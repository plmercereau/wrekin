import { html } from "hono/html";
import type { NodeView } from "../api/nodes";
import { layout, type PageOptions } from "./layout";

/** The account's machines, with their fingerprint and connection state. */
export function nodesPage(options: { nodes: NodeView[]; user: NonNullable<PageOptions["user"]> }) {
  const script = `
for (const button of document.querySelectorAll("button[data-remove]")) {
  button.addEventListener("click", async () => {
    if (!confirm("Remove " + button.dataset.name + "? The agent on it will have to pair again.")) return;
    button.disabled = true;
    const res = await fetch("/api/nodes/" + encodeURIComponent(button.dataset.remove), { method: "DELETE", credentials: "same-origin" });
    if (res.status === 204) window.location.reload();
    else { button.disabled = false; alert("Could not remove the node (HTTP " + res.status + ")."); }
  });
}`;
  const format = (iso: string | null) => (iso ? iso.replace("T", " ").replace(/\.\d+Z$/, " UTC").replace(/Z$/, " UTC") : "never");
  return layout(
    { title: "Your nodes", user: options.user, script },
    html`
<h1>Your nodes</h1>
${
  options.nodes.length === 0
    ? html`<div class="card"><p>No machine is paired yet.</p><p class="muted">Run <code>wrekin auth</code> on a machine and approve the code it prints.</p></div>`
    : options.nodes.map(
        (node) => html`
<div class="card">
  <div class="row">
    <strong>${node.name}</strong>
    <span class="badge ${node.online ? "online" : ""}">${node.online ? "online" : "offline"}</span>
  </div>
  <p class="fingerprint">${node.fingerprint}</p>
  <p class="muted">${node.online ? html`Connected since ${format(node.connectedAt)}` : html`Last seen ${format(node.lastSeenAt)}`}${node.agentVersion ? html` · agent ${node.agentVersion}` : ""}${node.platform ? html` · ${node.platform}` : ""}</p>
  ${node.harnesses && node.harnesses.length ? html`<p class="muted">Harnesses: ${node.harnesses.map((h) => `${h.id}${h.available ? "" : " (unavailable)"}${h.version ? ` ${h.version}` : ""}`).join(", ")}</p>` : ""}
  <div class="actions"><button class="danger" data-remove="${node.id}" data-name="${node.name}">Remove</button></div>
</div>`,
      )
}
<p class="muted">To add a machine: <code>wrekin auth</code> there, then approve the code on <a href="/device">the pairing page</a>.</p>
`,
  );
}
