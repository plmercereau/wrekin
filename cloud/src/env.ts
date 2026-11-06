import type { AccountHub } from "./hub/account-hub";

/** Bindings and variables of the Worker, as declared in wrangler.jsonc plus the secrets. */
export type Env = {
  DB: D1Database;
  ACCOUNT_HUB: DurableObjectNamespace<AccountHub>;
  /** Public origin of the deployment, e.g. https://wrekin.platyplus.io */
  BASE_URL: string;
  BETTER_AUTH_SECRET: string;
  GITHUB_CLIENT_ID: string;
  GITHUB_CLIENT_SECRET: string;
  GOOGLE_CLIENT_ID: string;
  GOOGLE_CLIENT_SECRET: string;
  /**
   * Optional. Comma-separated host suffixes (`claude.ai, claude.com`) the `redirect_uri` of
   * an OAuth authorization request must end with; the plugin refuses the request otherwise.
   * Absent or empty (the default), every URI a client registered is accepted, which is what
   * Claude's connector needs out of the box.
   */
  MCP_ALLOWED_REDIRECT_HOSTS?: string;
};
