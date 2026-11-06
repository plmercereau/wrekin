# Contributing

Everything in this repository is in English: code, comments, docs, commit
messages. Commits follow the conventional prefixes (`feat`, `fix`, `build`,
`ci`, `docs`, `chore`, `test`), with a scope when useful (`feat(agent): …`,
`feat(cloud): …`); the release changelog is grouped by them.

## Layout

Two halves share one contract, `protocol/PROTOCOL.md`:

- the Go agent (`cmd/wrekin`, `internal/`), module
  `github.com/plmercereau/wrekin`, Go 1.24, no cgo;
- the Cloudflare Worker (`cloud/`), Bun + TypeScript, Hono, Better Auth,
  Kysely on D1, one Durable Object per account, the MCP tools.

The design spec and the implementation plan are in `docs/superpowers/`.

## Tooling

`nix develop` gives a shell with Go, gopls, Bun, GoReleaser, tmux and git.
Without Nix, install Go ≥ 1.24, Bun, tmux and git yourself.

## The Go agent

```sh
gofmt -l .            # must print nothing
go vet ./...
go test -race ./...   # tmux tests skip when tmux is absent
go build ./cmd/wrekin
```

## The Worker

```sh
cd cloud
bun install --frozen-lockfile
bun run typecheck
bun run test          # vitest with @cloudflare/vitest-pool-workers: local D1 + Durable Objects
```

## Running both halves locally

1. Start the Worker with local D1 and Durable Objects. Copy
   `cloud/.dev.vars.example` to `cloud/.dev.vars` (dummy OAuth secrets are
   fine for a local run), then:

   ```sh
   cd cloud
   bunx wrangler d1 migrations apply wrekin --local
   bunx wrangler dev
   ```

   The relay then listens on `ws://localhost:8787/connect`, the pages and the
   MCP endpoint on `http://localhost:8787`.

2. Point an agent at it. The settings validation accepts a plain `ws://`
   relay only for `localhost` / `127.0.0.1`:

   ```sh
   XDG_CONFIG_HOME=$PWD/.local/config XDG_STATE_HOME=$PWD/.local/state \
     go run ./cmd/wrekin auth --name dev --relay ws://localhost:8787/connect
   XDG_CONFIG_HOME=$PWD/.local/config XDG_STATE_HOME=$PWD/.local/state \
     go run ./cmd/wrekin start --foreground
   ```

   `wrekin auth` prints a code and a URL on the local Worker; sign-in needs a
   real GitHub or Google OAuth app configured in `.dev.vars` (callback
   `http://localhost:8787/api/auth/callback/<provider>`). Without one, insert
   a user and a node row in the local D1 and write `identity.json` by hand,
   as described in the plan's Integration section.

3. Exercise the MCP tools against `http://localhost:8787/mcp` with an MCP
   client, or Claude Desktop pointed at the local URL.

## Packaging

- `goreleaser check` validates `.goreleaser.yaml`
  (`nix shell nixpkgs#goreleaser -c goreleaser check`).
- `nix flake check` evaluates the package, the dev shell and the Home Manager
  module on Linux and darwin; `nix build .#wrekin` builds the agent. After a
  change to `go.mod`/`go.sum`, refresh `vendorHash` in `nix/package.nix`
  (set it to `lib.fakeHash`, build, paste the hash Nix prints).
- `packaging/README.md` explains releases, the Homebrew tap and the GitHub
  secrets.

## Pull requests

CI runs the Go tests on Linux and macOS, the Worker tests, `goreleaser check`
and `nix flake check`. Keep the protocol document in step with both
implementations: a wire change bumps `v` and gets a changelog line in
`protocol/PROTOCOL.md`.
