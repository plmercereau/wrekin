# Packaging and release

How the Wrekin agent reaches users, and what has to exist outside this
repository for that to work. GoReleaser does the heavy lifting; the Nix flake
at the repository root is the other distribution channel and is described in
[the README](../README.md#install).

## Channels

| Channel | Produced by | Notes |
|---|---|---|
| GitHub Releases | `.goreleaser.yaml`, `release.yml` | `tar.gz` per `linux`/`darwin` × `amd64`/`arm64`, plus `checksums.txt` |
| Homebrew | `homebrew/wrekin.rb.tmpl` + `homebrew/render.sh`, run by `release.yml` | Formula written to `plmercereau/homebrew-tap`, `Formula/wrekin.rb`, with a `service` block for `brew services` and a `livecheck` block |
| Nix | `flake.nix`, `nix/` | `nix run github:plmercereau/wrekin`; a Home Manager module runs the daemon |

## Runtime dependencies

The binary is static and has no library dependencies, but the daemon execs
three programs by name, found on its PATH (`internal/gitx`,
`internal/terminal`, `internal/harness/claude`; nothing in `settings.json`
overrides them): `git`, `tmux` and `claude` (Claude Code). Each channel
covers them differently; the daemon runs as a user service, whose PATH is not
the interactive shell's, so each channel also sets that PATH.

| | `git` | `tmux` | `claude` | Service PATH |
|---|---|---|---|---|
| Homebrew formula | not declared: Homebrew itself requires git (Xcode's Command Line Tools, or the distribution's package on Linux) | `depends_on "tmux"` | not installable by a formula (`claude-code` is a cask, macOS only, or the native installer); the `caveats` say so | `~/.local/bin`, then `std_service_path_env` (Homebrew's prefix and the system directories) |
| Nix package | `gitMinimal`, appended to the binary's PATH by a wrapper | appended likewise | not wrapped in: see below | `wrekin start` installs the wrapper (the path the user invoked) and records a PATH: `~/.local/bin`, the invoking shell's PATH, the system directories |
| Home Manager module | `services.wrekin.path` (default `pkgs.git`) | `services.wrekin.path` (default `pkgs.tmux`) | `services.wrekin.claudePackage`: `pkgs.claude-code` when unfree packages are allowed, `null` otherwise | `~/.local/bin`, the profile, Homebrew on macOS, `services.wrekin.path`, the system directories |
| Release archive | the user's | the user's | the user's | `wrekin start` records a PATH in the unit/agent: `~/.local/bin`, the invoking shell's PATH, the system directories |

The order is deliberate: whatever the user installed and keeps up to date
(`~/.local/bin`, where Claude Code's native installer self-updates; the Nix
profile; Homebrew) wins over what the package brings, which is a fallback.

Claude Code from nixpkgs (`claude-code`) is **unfree**. A hard dependency
would make `nix build`, `nix run` and `nix flake check` fail for anyone who
has not set `allowUnfree`, CI included, so the package never references it;
the module and the dev shell reference it only when the evaluation allows it
(`builtins.tryEval` around `pkgs.claude-code`), and the module exposes the
choice as `services.wrekin.claudePackage`. It also lags upstream; the native
installer does not.

## Cutting a release

1. Make sure `main` is green (the `CI` workflow: Go tests on Linux and
   macOS, the Worker tests, `goreleaser check`, `nix flake check` and
   `nix build`, the Homebrew formula render).
2. Tag and push:

   ```sh
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

3. The `Release` workflow (`.github/workflows/release.yml`) runs
   `goreleaser release --clean`:
   - builds the four binaries with `CGO_ENABLED=0`, `-trimpath`, a
     reproducible module timestamp and `-X main.version=<tag without v>`,
     so `wrekin --version` prints `wrekin version 0.1.0`;
   - archives them with `README.md` and `LICENSE`, writes `checksums.txt`;
   - creates the GitHub release with a changelog grouped by conventional
     commit prefix (`feat`, `fix`, `build`, `docs`; `chore`, `ci`, `test`
     and `style` are left out) and an install footer.
   Then the `homebrew` job of the same workflow renders
   `Formula/wrekin.rb` from the release's `checksums.txt` with
   `packaging/homebrew/render.sh`, checks it with `ruby -c`, and pushes it
   to the tap. Pre-release tags (`v0.2.0-rc1`) are published on GitHub,
   marked as pre-releases, and not pushed to the tap.
4. Check `brew install plmercereau/tap/wrekin` and
   `nix run github:plmercereau/wrekin/v0.1.0 -- --version` on a machine.

Version numbers follow semantic versioning; the MVP is `0.x`.

To rehearse locally without publishing:

```sh
nix shell nixpkgs#goreleaser -c goreleaser release --snapshot --clean
ls dist/
sh packaging/homebrew/render.sh 0.1.0 dist/checksums.txt | ruby -c
```

## Things that must exist outside this repository

### The Homebrew tap

A repository `plmercereau/homebrew-tap` on GitHub, with a `Formula/`
directory (it may start empty). Users then run
`brew install plmercereau/tap/wrekin`; Homebrew resolves `plmercereau/tap`
to that repository. The `homebrew` job of `release.yml` commits
`Formula/wrekin.rb` to its default branch on every stable release.

### GitHub Actions secrets

| Secret | Used by | Purpose |
|---|---|---|
| `HOMEBREW_TAP_GITHUB_TOKEN` | `release.yml`, `homebrew` job | A fine-grained personal access token with *Contents: read and write* on `plmercereau/homebrew-tap` only (GitHub → Settings → Developer settings → Fine-grained tokens). The default `GITHUB_TOKEN` cannot push to another repository. |

`GITHUB_TOKEN` is provided by GitHub Actions; the `goreleaser` job asks for
`contents: write` so it can create the release, the `homebrew` job only
reads it.

The Worker's own runtime secrets (`BETTER_AUTH_SECRET`, the GitHub and
Google OAuth client ids and secrets) are set in the Cloudflare dashboard on
the Worker, never in GitHub; see `cloud/README.md`.

### Deploying the Worker

No workflow here deploys the Worker. Cloudflare's Workers Builds is
connected to this repository and builds `cloud/` on every push to `main`
(root directory `cloud`, `bun run build`, then `bun run deploy`), with the
deployment's identity (the D1 database id, the public URL) as build
variables read from the platyplus/infrastructure stack's outputs. That
repository's runbook `docs/runbooks/2026-11-wrekin-site.md` sets it up; the
D1 database, the Worker entity and the custom domain are its stack's.

## Notes

- `goreleaser check` validates `.goreleaser.yaml`. GoReleaser is not a
  dependency of the repository: run it through
  `nix shell nixpkgs#goreleaser -c goreleaser check` or
  `go run github.com/goreleaser/goreleaser/v2@latest check`. Without
  GoReleaser, the file can be checked against its JSON schema,
  <https://goreleaser.com/static/schema.json>.
- The formula is not GoReleaser's: its `brews` section is deprecated in
  favour of `homebrew_casks` (and `goreleaser check` fails on deprecated
  options), while a cask cannot carry the `service` block that
  `brew services` needs to run the daemon. `packaging/homebrew/render.sh`
  fills `wrekin.rb.tmpl` with the version and the four archive checksums;
  CI renders it from `testdata/checksums.txt` and checks the Ruby syntax.
- The formula's `service` block sets `PATH` explicitly
  (`environment_variables PATH: "#{Dir.home}/.local/bin:#{std_service_path_env}"`):
  launchd starts agents with `/usr/bin:/bin:/usr/sbin:/sbin` only, which has
  neither Homebrew's tmux and `claude-code` cask nor the native installer's
  `~/.local/bin/claude`. Homebrew supports `Dir.home` in service strings (it
  is what its `/$HOME` API placeholder stands for).
- The formula runs `wrekin daemon` as a user-level service; the Home Manager
  module in `nix/module.nix` does the same with `systemd --user` or launchd.
  `wrekin start` does it too, from the Go code (`internal/service`), for
  binaries installed any other way.
