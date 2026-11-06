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
};
