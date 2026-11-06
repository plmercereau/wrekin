/// <reference types="vite/client" />
import { describe, expect, it } from "vitest";
import committed from "../../wrangler.jsonc?raw";
import { DEPLOY_CONFIG_FILE, parseWranglerConfig, renderDeployConfig, stripJsonComments } from "../../scripts/wrangler-config";

const source = `// a comment
{
  "$schema": "node_modules/wrangler/config-schema.json",
  "name": "wrekin", /* block */
  "vars": { "BASE_URL": "http://localhost:8787", "KEEP": "a // not a comment" },
  "d1_databases": [{ "binding": "DB", "database_name": "wrekin", "database_id": "00000000-0000-0000-0000-000000000000", "migrations_dir": "migrations" }],
  "observability": { "enabled": true }
}
`;
const env = { WREKIN_D1_DATABASE_ID: "6f1c2a3b-4d5e-4f60-8a9b-0c1d2e3f4a5b", WREKIN_BASE_URL: "https://wrekin.platyplus.io" };

describe("stripJsonComments", () => {
  it("removes comments but not slashes inside strings", () => {
    const parsed = JSON.parse(stripJsonComments(source));
    expect(parsed.name).toBe("wrekin");
    expect(parsed.vars.KEEP).toBe("a // not a comment");
  });
});

describe("renderDeployConfig", () => {
  it("replaces the database id and BASE_URL, drops $schema, keeps the rest", () => {
    const out = JSON.parse(renderDeployConfig(source, env));
    expect(out.$schema).toBeUndefined();
    expect(out.name).toBe("wrekin");
    expect(out.vars).toEqual({ BASE_URL: "https://wrekin.platyplus.io", KEEP: "a // not a comment" });
    expect(out.d1_databases).toEqual([
      { binding: "DB", database_name: "wrekin", database_id: env.WREKIN_D1_DATABASE_ID, migrations_dir: "migrations" },
    ]);
    expect(out.observability).toEqual({ enabled: true });
  });

  it("strips a trailing slash from the base URL", () => {
    const out = JSON.parse(renderDeployConfig(source, { ...env, WREKIN_BASE_URL: "https://wrekin.platyplus.io/" }));
    expect(out.vars.BASE_URL).toBe("https://wrekin.platyplus.io");
  });

  it("refuses a missing or malformed database id", () => {
    expect(() => renderDeployConfig(source, { ...env, WREKIN_D1_DATABASE_ID: undefined })).toThrow(/WREKIN_D1_DATABASE_ID/);
    expect(() => renderDeployConfig(source, { ...env, WREKIN_D1_DATABASE_ID: "REPLACE_WITH_D1_ID" })).toThrow(/UUID/);
  });

  it("refuses a base URL that is not an https origin", () => {
    expect(() => renderDeployConfig(source, { ...env, WREKIN_BASE_URL: "http://wrekin.platyplus.io" })).toThrow(/https/);
    expect(() => renderDeployConfig(source, { ...env, WREKIN_BASE_URL: "https://wrekin.platyplus.io/mcp" })).toThrow(/no path/);
  });

  it("renders the committed wrangler.jsonc itself", () => {
    const out = JSON.parse(renderDeployConfig(committed, env));
    expect(out.name).toBe("wrekin");
    expect(out.routes).toBeUndefined();
    expect(out.workers_dev).toBe(false);
    expect(parseWranglerConfig(committed).d1_databases?.[0]?.database_id).toBe("00000000-0000-0000-0000-000000000000");
  });

  it("keeps the relative paths verbatim, since the file is written next to wrangler.jsonc", () => {
    expect(DEPLOY_CONFIG_FILE).toBe("wrangler.deploy.json");
    const out = JSON.parse(renderDeployConfig(committed, env));
    // The custom TanStack Start server entry that exports the Durable Object class.
    expect(out.main).toBe("src/server.ts");
    expect(out.d1_databases[0].migrations_dir).toBe("migrations");
    expect(out.durable_objects).toEqual({ bindings: [{ name: "ACCOUNT_HUB", class_name: "AccountHub" }] });
  });
});
