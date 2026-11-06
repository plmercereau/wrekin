# The Wrekin agent, built from this checkout.
#
# Called by flake.nix through `pkgs.callPackage`; `version` is injected there.
{
  lib,
  buildGoModule,
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

  meta = {
    description = "Teleport a Claude Code Cloud session onto one of your own machines, from Claude, with no inbound connection";
    homepage = "https://wrekin.platyplus.io";
    changelog = "https://github.com/plmercereau/wrekin/releases";
    license = lib.licenses.mit;
    mainProgram = "wrekin";
    platforms = lib.platforms.linux ++ lib.platforms.darwin;
  };
}
