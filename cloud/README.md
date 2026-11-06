# Wrekin cloud

The relay between Claude and your machines: a Cloudflare Worker (Hono) with
Better Auth on D1, one Durable Object per account that holds the node
WebSockets and routes commands, and the remote MCP server Claude talks to.

The wire contract with the Go agent is `../protocol/PROTOCOL.md`; its zod
schemas live in `src/protocol/`.

## Layout

```
src/index.ts            Worker entry: the Hono app + the AccountHub export
src/app.ts              routes: auth, discovery, node API, connect, MCP, pages, 404/500
src/env.ts              bindings and secrets
src/services.ts         auth + node store + hub, built from env (tests inject their own)
src/auth.ts             Better Auth: GitHub, Google, device authorization, MCP OAuth
src/crypto.ts           base64, Ed25519 verification, key fingerprints, random ids
src/db/                 Kysely types and the node store
src/hub/                core.ts (handshake, routing), account-hub.ts (the Durable
                        Object), client.ts (typed wrapper over the stub)
src/api/nodes.ts        node registration and signed unregistration
src/connect.ts          GET /connect: the nodes' WebSocket
src/pages/              server-rendered pages (hono/html) and their routes
src/mcp/                McpServer, tools, session id parsing, the /mcp route
scripts/                the migration generator and the deploy-config renderer (Bun, outside the Worker)
migrations/             D1 migrations (0001 is generated, 0002 is the node table)
test/*.test.ts          run inside workerd only (D1, Durable Object, WebSockets)
test/unit/*.test.ts     pure modules, run both inside workerd and in plain Node
test/shared/            suites shared by the two runs; test/helpers: fakes
```

## Local development

```sh
bun install
cp .dev.vars.example .dev.vars     # then fill the OAuth client ids and secrets
bun run db:migrate:local           # applies migrations/ to the local D1
bun run dev                        # http://localhost:8787
```

The OAuth apps must allow these callback URLs (with `BASE_URL` =
`http://localhost:8787` locally, `https://wrekin.platyplus.io` in
production):

- GitHub OAuth App: `<BASE_URL>/api/auth/callback/github`
- Google OAuth client: `<BASE_URL>/api/auth/callback/google`

An agent pairs against a local relay with
`wrekin auth --relay ws://localhost:8787/connect`.

## Tests and checks

```sh
bun run typecheck   # tsc --noEmit
bun run test        # every test, inside workerd (D1, Durable Object, WebSockets)
bun run test:node   # test/unit only, in plain Node, for sandboxes that cannot bind a port
```

`bun run test` needs to listen on 127.0.0.1 (workerd); CI runs it. The pure
modules (protocol schemas, MCP tools with a fake hub, pages, the hub core) are
also covered by `test:node`.

## Database schema

`migrations/0001_auth.sql` is generated from the Better Auth configuration:

```sh
bun run db:generate   # scripts/generate-auth-migration.ts
```

The script calls the schema generator Better Auth ships (`getMigrations`, the
one `@better-auth/cli generate` wraps) from the installed version of the
library, because the published CLI lags the library and omitted three unique
indexes. Re-generate whenever `src/auth.ts` changes its plugins, then review
the diff. `0002_nodes.sql` is hand-written.

Better Auth endpoints the agent uses: `POST /api/auth/device/code` (form or
JSON), `POST /api/auth/device/token` (JSON, or a form body that the Worker
re-encodes, RFC 8628 style). Sessions can be presented as
`Authorization: Bearer <session token>`.

## Deploy

Nothing in this directory deploys from a laptop or from GitHub Actions.
Cloudflare's Workers Builds is connected to this repository and deploys
`cloud/` on every push to `main`, the way Vercel builds from Git; the
platyplus/infrastructure stack (`sites/wrekin/tofu`) owns the D1 database,
the Worker entity and the name `wrekin.platyplus.io`, and its runbook
(`docs/runbooks/2026-11-wrekin-site.md` there) walks through the first
deploy.

`wrangler.jsonc` holds what belongs to the code: name, entry point,
compatibility date and flags, the bindings' names, the Durable Object
migrations. What belongs to a deployment is not in the repository:

| Value | Where it lives | How it reaches wrangler |
|---|---|---|
| The D1 database id | the stack (`d1_database_id` output) | build variable `WREKIN_D1_DATABASE_ID` |
| The public URL (`BASE_URL`) | the stack (`base_url` output) | build variable `WREKIN_BASE_URL` |
| The name `wrekin.platyplus.io` | the stack's custom domain | not in the config at all (`workers_dev: false`, no `routes`) |
| The five secrets | the Worker's *Variables and Secrets* in the dashboard | `wrangler deploy` never touches secrets |

Workers Builds settings: root directory `cloud`, build command `bun run
build`, deploy command `bun run deploy`, production branch `main`,
non-production branch builds off. `bun run build`
(`scripts/render-wrangler.ts`) writes `dist/wrangler.json` from
`wrangler.jsonc` plus the two build variables and refuses to run without
them; `bun run deploy` applies the migrations to the remote database and
deploys, both with `--config dist/wrangler.json`. The values of `BASE_URL`
and `database_id` committed in `wrangler.jsonc` serve local development
only.

To rehearse the deploy configuration locally:

```sh
WREKIN_D1_DATABASE_ID=<uuid> WREKIN_BASE_URL=https://wrekin.platyplus.io bun run build
cat dist/wrangler.json
```

The MCP endpoint is `<BASE_URL>/mcp`; nodes connect to `wss://<host>/connect`.

## What the Worker stores about a node

Its id, account, name, public key, agent version, platform string and
created/last-seen timestamps. Never a private key, a token or any terminal
output; nothing in an MCP response is raw terminal output either.
