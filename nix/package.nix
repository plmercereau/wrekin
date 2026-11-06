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

  # `null` is the correct value while the module has no dependencies (the
  # placeholder main from Track C): there is nothing to vendor.
  #
  # Once Track A lands (go.sum with cobra, coder/websocket, ...), this MUST be
  # recomputed: set it to `lib.fakeHash`, run `nix build .#wrekin`, and paste
  # the hash Nix prints in "got: sha256-...".
  vendorHash = null;

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
