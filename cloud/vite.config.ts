import { cloudflare } from "@cloudflare/vite-plugin";
import { tanstackStart } from "@tanstack/react-start/plugin/vite";
import viteReact from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The Worker is built and served by the Cloudflare plugin from wrangler.jsonc. `bun run build`
// points CLOUDFLARE_VITE_WRANGLER_CONFIG_PATH at wrangler.deploy.json instead (the plugin
// reads that variable itself), so the deploy configuration flows into dist/wrekin/wrangler.json.
export default defineConfig({
  // The port BASE_URL and the OAuth callback URLs assume locally (wrangler's default, kept for the docs and .dev.vars).
  server: { port: 8787, strictPort: true },
  plugins: [cloudflare({ viteEnvironment: { name: "ssr" } }), tanstackStart(), viteReact()],
});
