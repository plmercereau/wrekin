{
  description = "Wrekin: teleport a Claude Code Cloud session onto one of your own machines, from Claude, with no inbound connection";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { self, nixpkgs }:
    let
      inherit (nixpkgs) lib;

      # No x86_64-darwin: nixpkgs-unstable (26.11) dropped it. GoReleaser still
      # ships a darwin/amd64 binary; Intel Macs install from Homebrew or the
      # GitHub release.
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = f: lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

      # A checkout is 0.0.0-<short rev>; tagged releases are built by
      # GoReleaser. `nix run github:plmercereau/wrekin/v0.1.0` still builds
      # from the tag's sources, with that tag's revision as the version.
      version = "0.0.0-${self.shortRev or self.dirtyShortRev or "dev"}";

      mkWrekin = pkgs: pkgs.callPackage ./nix/package.nix { inherit version; };
    in
    {
      packages = forAllSystems (pkgs: rec {
        wrekin = mkWrekin pkgs;
        default = wrekin;
      });

      overlays.default = final: _prev: { wrekin = mkWrekin final; };

      apps = forAllSystems (pkgs: rec {
        wrekin = {
          type = "app";
          program = lib.getExe self.packages.${pkgs.stdenv.hostPlatform.system}.wrekin;
        };
        default = wrekin;
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            bun # the Worker in cloud/ (wrangler comes with `bunx wrangler`)
            goreleaser
            tmux
            git
          ];
        };
      });

      # services.wrekin: see nix/module.nix. The flake variant points
      # `package` at this flake's build so no overlay is needed.
      homeManagerModules = rec {
        wrekin =
          { pkgs, lib, ... }:
          {
            imports = [ ./nix/module.nix ];
            services.wrekin.package = lib.mkDefault self.packages.${pkgs.stdenv.hostPlatform.system}.wrekin;
          };
        default = wrekin;
      };

      formatter = forAllSystems (pkgs: pkgs.nixfmt);

      checks = forAllSystems (
        pkgs:
        let
          system = pkgs.stdenv.hostPlatform.system;

          # Evaluate the Home Manager module against stub declarations of the
          # few Home Manager options it sets, so that `nix flake check`
          # catches a broken module on Linux and on darwin without making
          # home-manager an input of this flake.
          homeManagerStubs =
            { lib, ... }:
            let
              anyAttrs = lib.mkOption {
                type = lib.types.attrsOf lib.types.anything;
                default = { };
              };
            in
            {
              options = {
                home.packages = lib.mkOption {
                  type = lib.types.listOf lib.types.package;
                  default = [ ];
                };
                home.homeDirectory = lib.mkOption {
                  type = lib.types.str;
                  default = "/home/wrekin";
                };
                home.profileDirectory = lib.mkOption {
                  type = lib.types.str;
                  default = "/home/wrekin/.nix-profile";
                };
                xdg.configFile = anyAttrs;
                systemd.user.services = anyAttrs;
                launchd.agents = anyAttrs;
              };
            };

          moduleEval = lib.evalModules {
            specialArgs = { inherit pkgs; };
            modules = [
              homeManagerStubs
              self.homeManagerModules.wrekin
              {
                services.wrekin = {
                  enable = true;
                  settings = {
                    name = "check";
                    workspace.allowedDirectories = [ "~/projects" ];
                  };
                };
              }
            ];
          };
          evaluated = moduleEval.config;
        in
        {
          package = self.packages.${system}.wrekin;

          home-manager-module = pkgs.writeText "wrekin-home-manager-module-${system}.json" (
            builtins.toJSON {
              packages = map lib.getName evaluated.home.packages;
              settings = evaluated.xdg.configFile."wrekin/settings.json".source;
              systemd = evaluated.systemd.user.services;
              launchd = evaluated.launchd.agents;
            }
          );
        }
      );
    };
}
