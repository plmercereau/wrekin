#!/usr/bin/env bun
// `bun run build`: writes wrangler.deploy.json next to wrangler.jsonc, the configuration
// Vite (through CLOUDFLARE_VITE_WRANGLER_CONFIG_PATH) builds the Worker from and
// `bun run deploy` applies the migrations with. Same directory as wrangler.jsonc so
// `main` and `migrations_dir` resolve the same. Run by Workers Builds, where the two
// build variables come from the platyplus/infrastructure stack's outputs.
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { DEPLOY_CONFIG_FILE, renderDeployConfig } from "./wrangler-config";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const source = readFileSync(join(root, "wrangler.jsonc"), "utf8");
let rendered: string;
try {
  rendered = renderDeployConfig(source, {
    WREKIN_D1_DATABASE_ID: process.env.WREKIN_D1_DATABASE_ID,
    WREKIN_BASE_URL: process.env.WREKIN_BASE_URL,
  });
} catch (error) {
  console.error(`render-wrangler: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
}
writeFileSync(join(root, DEPLOY_CONFIG_FILE), rendered);
console.log(`render-wrangler: wrote ${DEPLOY_CONFIG_FILE}`);
