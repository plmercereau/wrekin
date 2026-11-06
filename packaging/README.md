# Packaging and release

How the Wrekin agent reaches users, and what has to exist outside this
repository for that to work. GoReleaser does the heavy lifting; the Nix flake
at the repository root is the other distribution channel and is described in
[the README](../README.md#install).

## Channels

| Channel | Produced by | Notes |
|---|---|---|
| GitHub Releases | `.goreleaser.yaml`, `release.yml` | `tar.gz` per `linux`/`darwin` × `amd64`/`arm64`, plus `checksums.txt` |
| Homebrew | GoReleaser `brews` section | Formula written to `plmercereau/homebrew-tap`, `Formula/wrekin.rb`, with a `service` block for `brew services` |
| Nix | `flake.nix`, `nix/` | `nix run github:plmercereau/wrekin`; a Home Manager module runs the daemon |

## Cutting a release

1. Make sure `main` is green (the `CI` workflow: Go tests on Linux and
   macOS, the Worker tests, `goreleaser check`, `nix flake check`).
2. Tag and push:

   ```sh
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

3. The `Release` workflow (`.github/workflows/release.yml`) runs
   `goreleaser release --clean`:
   - builds the four binaries with `CGO_ENABLED=0` and
     `-X main.version=<tag without v>`, so `wrekin --version` prints the
     release version;
   - archives them with `README.md` and `LICENSE`, writes `checksums.txt`;
   - creates the GitHub release with a changelog grouped by conventional
     commit prefix (`feat`, `fix`, `build`, `docs`; `chore`, `ci`, `test`
     and `style` are left out);
   - writes the Homebrew formula to the tap. Pre-release tags
     (`v0.2.0-rc1`) are published on GitHub but not pushed to the tap
     (`skip_upload: auto`).
4. Check `brew install plmercereau/tap/wrekin` and
   `nix run github:plmercereau/wrekin/v0.1.0 -- --version` on a machine.

Version numbers follow semantic versioning; the MVP is `0.x`.

To rehearse locally without publishing:

```sh
nix shell nixpkgs#goreleaser -c goreleaser release --snapshot --clean
ls dist/
```

## Things that must exist outside this repository

### The Homebrew tap

A repository `plmercereau/homebrew-tap` on GitHub, with a `Formula/`
directory (it may start empty). Users then run
`brew install plmercereau/tap/wrekin`; Homebrew resolves `plmercereau/tap`
to that repository. GoReleaser commits `Formula/wrekin.rb` to its `main`
branch on every stable release.

### GitHub Actions secrets

| Secret | Used by | Purpose |
|---|---|---|
| `HOMEBREW_TAP_GITHUB_TOKEN` | `release.yml` | A fine-grained personal access token with *Contents: read and write* on `plmercereau/homebrew-tap` only. The default `GITHUB_TOKEN` cannot push to another repository. |
| `CLOUDFLARE_API_TOKEN` | `deploy-cloud.yml` | A Cloudflare API token with *Workers Scripts: Edit* and *D1: Edit* on the account that hosts `wrekin.platyplus.io` (the account and zone also need *Workers Routes: Edit* for the custom domain). Used by `wrangler d1 migrations apply` and `wrangler deploy`. |
| `CLOUDFLARE_ACCOUNT_ID` | `deploy-cloud.yml` | The Cloudflare account id the Worker is deployed to. Not secret in itself, but kept with the token so the workflow has no account-specific value in the repository. |

`GITHUB_TOKEN` is provided by GitHub Actions; `release.yml` asks for
`contents: write` so it can create the release.

The two Cloudflare secrets live in the `production` environment of the
repository settings (Settings → Environments → `production`), which is what
`deploy-cloud.yml` targets; protection rules on that environment (required
reviewers, branch restriction to `main`) apply to every deploy. The
Worker's own runtime secrets (`BETTER_AUTH_SECRET`, the GitHub and Google
OAuth client ids and secrets) are *Wrangler* secrets set once with
`wrangler secret put`, not GitHub secrets; see `cloud/README.md`.

### Deploying the Worker

`.github/workflows/deploy-cloud.yml` runs on every push to `main` that
touches `cloud/**`, and on demand from the Actions tab (`workflow_dispatch`).
It installs the dependencies with `bun install --frozen-lockfile`, runs
`bun run typecheck` and `bun run test`, applies the D1 migrations to the
remote database (`wrangler d1 migrations apply wrekin --remote`) and then
`wrangler deploy`. The D1 database and the custom domain are created by the
`platyplus/infrastructure` repository (OpenTofu), not by this workflow.

## Notes

- `goreleaser check` validates `.goreleaser.yaml`. GoReleaser is not a
  dependency of the repository: run it through
  `nix shell nixpkgs#goreleaser -c goreleaser check` or
  `go run github.com/goreleaser/goreleaser/v2@latest check`.
- The `brews` section is the formula publisher. GoReleaser also offers
  `homebrew_casks`; Wrekin stays a formula because `brew services` (the
  `service` block) is what runs the daemon on macOS and Linux.
- The formula runs `wrekin daemon` as a user-level service; the Home Manager
  module in `nix/module.nix` does the same with `systemd --user` or launchd.
