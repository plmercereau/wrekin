/**
 * The deploy-time Wrangler configuration: the committed wrangler.jsonc (what
 * belongs to the code) with the two values that belong to a deployment, the
 * D1 database id and the public URL, which Workers Builds passes as build
 * variables from the platyplus/infrastructure stack's outputs. Pure: no I/O,
 * so plain Node tests can exercise it (scripts/render-wrangler.ts does the
 * reading and writing).
 */

/**
 * Where `bun run build` writes the rendered configuration: next to wrangler.jsonc, so the
 * relative `main` and `migrations_dir` resolve the same from both files (Vite builds the Worker
 * from it, `bun run deploy` applies the migrations with it).
 */
export const DEPLOY_CONFIG_FILE = "wrangler.deploy.json";

export type DeployEnv = {
  WREKIN_D1_DATABASE_ID?: string;
  WREKIN_BASE_URL?: string;
};

export type WranglerConfig = {
  $schema?: string;
  name: string;
  vars?: Record<string, string>;
  d1_databases?: Array<{ binding: string; database_name: string; database_id: string; migrations_dir?: string }>;
  [key: string]: unknown;
};

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Strips // and /* *\/ comments outside strings, so JSON.parse can read JSONC. */
export function stripJsonComments(source: string): string {
  let out = "";
  let i = 0;
  while (i < source.length) {
    const ch = source[i]!;
    if (ch === '"') {
      let j = i + 1;
      while (j < source.length && source[j] !== '"') {
        if (source[j] === "\\") j++;
        j++;
      }
      out += source.slice(i, j + 1);
      i = j + 1;
    } else if (ch === "/" && source[i + 1] === "/") {
      while (i < source.length && source[i] !== "\n") i++;
    } else if (ch === "/" && source[i + 1] === "*") {
      const end = source.indexOf("*/", i + 2);
      i = end === -1 ? source.length : end + 2;
    } else {
      out += ch;
      i++;
    }
  }
  return out;
}

export function parseWranglerConfig(source: string): WranglerConfig {
  const parsed: unknown = JSON.parse(stripJsonComments(source));
  if (typeof parsed !== "object" || parsed === null || typeof (parsed as WranglerConfig).name !== "string") {
    throw new Error("wrangler.jsonc: expected an object with a name");
  }
  return parsed as WranglerConfig;
}

/**
 * Returns the deploy configuration as a JSON string, or throws with a sentence
 * naming the missing or malformed build variable.
 */
export function renderDeployConfig(source: string, env: DeployEnv): string {
  const databaseId = env.WREKIN_D1_DATABASE_ID?.trim();
  if (!databaseId || !UUID.test(databaseId)) {
    throw new Error("WREKIN_D1_DATABASE_ID must be the D1 database's UUID (the stack's d1_database_id output)");
  }
  const baseURL = env.WREKIN_BASE_URL?.trim().replace(/\/+$/, "");
  if (!baseURL || !/^https:\/\/[^/\s]+$/.test(baseURL)) {
    throw new Error("WREKIN_BASE_URL must be the public origin, https://<host> with no path");
  }
  const config = parseWranglerConfig(source);
  const { $schema: _schema, ...rest } = config;
  const d1 = rest.d1_databases ?? [];
  if (d1.length !== 1 || d1[0]!.binding !== "DB") {
    throw new Error("wrangler.jsonc: expected exactly one D1 binding named DB");
  }
  const deploy: WranglerConfig = {
    ...rest,
    vars: { ...(rest.vars ?? {}), BASE_URL: baseURL },
    d1_databases: [{ ...d1[0]!, database_id: databaseId }],
  };
  return JSON.stringify(deploy, null, 2) + "\n";
}
