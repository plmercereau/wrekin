#!/usr/bin/env bun
// `bun run build`: writes dist/wrangler.json, the configuration `bun run deploy`
// passes to wrangler with --config. Run by Workers Builds, where the two build
// variables come from the platyplus/infrastructure stack's outputs.
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { renderDeployConfig } from "./wrangler-config";

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
mkdirSync(join(root, "dist"), { recursive: true });
writeFileSync(join(root, "dist", "wrangler.json"), rendered);
console.log("render-wrangler: wrote dist/wrangler.json");
