import { html } from "hono/html";
import { layout, type PageOptions } from "./layout";

/**
 * Two buttons, one per identity provider. `redirect` is where to land afterwards; when the
 * OAuth provider sent the visitor here (its signed query is in the URL), that query goes
 * back as `oauth_query` so the authorization resumes once the session exists.
 */
export function loginPage(options: { redirect: string; oauthQuery: string | null; user: PageOptions["user"] }) {
  const script = `
const redirect = ${JSON.stringify(options.redirect)};
const oauthQuery = ${JSON.stringify(options.oauthQuery)};
const status = document.getElementById("status");
for (const button of document.querySelectorAll("button[data-provider]")) {
  button.addEventListener("click", async () => {
    status.textContent = "Redirecting…";
    status.className = "status";
    try {
      const body = { provider: button.dataset.provider, callbackURL: redirect };
      if (oauthQuery) body.oauth_query = oauthQuery;
      const res = await fetch("/api/auth/sign-in/social", {
        method: "POST", credentials: "same-origin",
        headers: { "content-type": "application/json" }, body: JSON.stringify(body),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data.url) throw new Error(data.message || data.error || ("HTTP " + res.status));
      window.location.href = data.url;
    } catch (error) {
      status.textContent = "Sign-in failed: " + error.message;
      status.className = "status error";
    }
  });
}`;
  return layout(
    { title: "Sign in", user: options.user, script },
    html`
<h1>Sign in</h1>
<p class="muted">Wrekin only needs to know who you are. Either account gives you the same Wrekin account when the e-mail matches.</p>
${options.user ? html`<p>You are signed in as <strong>${options.user.name}</strong>. <a href="${options.redirect}">Continue</a> or pick another account below.</p>` : ""}
<div class="actions">
  <button class="primary" data-provider="github">Continue with GitHub</button>
  <button data-provider="google">Continue with Google</button>
</div>
<p id="status" class="status"></p>
`,
  );
}
