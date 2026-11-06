# The Wrekin agent, built from this checkout.
#
# Called by flake.nix through `pkgs.callPackage`; `version` is injected there.
{
  lib,
  buildGoModule,
  makeBinaryWrapper,
  gitMinimal,
  tmux,
  version ? "dev",
}:

buildGoModule {
  pname = "wrekin";
  inherit version;

  # Only the Go sources: docs, packaging and the Worker never trigger a rebuild.
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      (lib.fileset.maybeMissing ../go.sum)
      ../cmd
      (lib.fileset.maybeMissing ../internal)
    ];
  };

  subPackages = [ "cmd/wrekin" ];

  # The hash of the vendored modules (go.sum: cobra, coder/websocket and their
  # dependencies). Recompute it whenever go.sum changes: either set it to
  # `lib.fakeHash`, run `nix build .#wrekin` and paste the hash Nix prints in
  # "got: sha256-...", or run `go mod vendor` in a scratch copy and
  # `nix hash path --sri ./vendor`, which gives the same value.
  vendorHash = "sha256-Ec1pha6gNpXmpPkc89yiQHeagLSyxPWsYlOP/2AwyII=";

  env.CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  nativeBuildInputs = [ makeBinaryWrapper ];

  # Runtime dependencies. The daemon execs `git`, `tmux` and `claude` by name
  # (a PATH lookup in internal/gitx, internal/terminal and
  # internal/harness/claude; no setting overrides them), so the installed
  # binary gets git and tmux *appended* to its PATH: `nix run` and
  # `nix profile install` then work on a machine that has neither, while the
  # tools the user installed and maintains themselves keep precedence (the
  # user attaches to the daemon's tmux sessions with their own tmux).
  # gitMinimal: the daemon only clones, fetches and manages worktrees; the
  # full git (perl, manual, keychain helper) is the user's to install.
  #
  # Claude Code is deliberately not wrapped in:
  # - nixpkgs' `claude-code` is unfree. A hard dependency would make
  #   `nix build`, `nix run github:plmercereau/wrekin` and `nix flake check`
  #   fail for everyone who has not allowed unfree packages, CI included.
  # - It lags upstream, and most users run the native installer, which keeps
  #   `~/.local/bin/claude` up to date by itself (or the Homebrew cask).
  # The Home Manager module (nix/module.nix) puts `~/.local/bin` on the
  # service's PATH and offers `services.wrekin.claudePackage` for those who
  # want the nixpkgs package.
  #
  # `wrekin start` installs its systemd/launchd service from the path the
  # user invoked (this wrapper, or the profile symlink to it), not from the
  # wrapped binary, and records a PATH built from the invoking shell plus
  # ~/.local/bin, so git and tmux are found there too. The Home Manager module
  # remains the declarative way to run the daemon.
  postInstall = ''
    wrapProgram $out/bin/wrekin \
      --suffix PATH : ${
        lib.makeBinPath [
          gitMinimal
          tmux
        ]
      }
  '';

  meta = {
    description = "Teleport a Claude Code Cloud session onto one of your own machines, from Claude, with no inbound connection";
    homepage = "https://wrekin.platyplus.io";
    changelog = "https://github.com/plmercereau/wrekin/releases";
    license = lib.licenses.mit;
    mainProgram = "wrekin";
    platforms = lib.platforms.linux ++ lib.platforms.darwin;
  };
}
