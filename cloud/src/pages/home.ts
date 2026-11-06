import { html } from "hono/html";
import { layout, type PageOptions } from "./layout";

/** The front page: what Wrekin is, how to install the agent and how to connect Claude. */
export function homePage(baseURL: string, user: PageOptions["user"]) {
  return layout(
    { title: "Teleport Claude Code sessions to your machines", user },
    html`
<h1>To all agents round the Wrekin</h1>
<p>Wrekin moves a Claude Code Cloud session onto a Linux or macOS machine you own, from Claude on your phone, in the browser or on the desktop. The machine runs a small agent that keeps one outbound connection to this relay: no SSH, no inbound port, no secret of yours stored here.</p>

<h2>1. Install the agent</h2>
<pre>brew install plmercereau/tap/wrekin
# or
nix run github:plmercereau/wrekin -- auth
# or download a release: https://github.com/plmercereau/wrekin/releases</pre>

<h2>2. Pair the machine</h2>
<pre>wrekin auth
wrekin start</pre>
<p><code>wrekin auth</code> prints a code; open the link it shows on any device, sign in and approve the machine. Then <code>wrekin start</code> installs the user service that keeps the agent connected.</p>

<h2>3. Connect Claude</h2>
<p>Add <code>${baseURL}/mcp</code> as a remote MCP server in Claude's connectors and sign in when asked. Claude can then <em>list your nodes</em> and <em>teleport</em> the current session to one of them; you find it later with <code>wrekin attach</code>.</p>

<h2>What stays where</h2>
<p class="muted">The relay keeps your account, the names and public keys of your machines and the routing metadata a teleport needs. Private keys, Git credentials, local logs and terminal output never leave the machine.</p>
<p><a class="button" href="/nodes">Your nodes</a></p>
`,
  );
}
