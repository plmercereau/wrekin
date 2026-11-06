# Home Manager module: runs the Wrekin agent as a user-level service.
#
#   services.wrekin = {
#     enable = true;
#     settings.name = "workstation";
#     settings.workspace.allowedDirectories = [ "~/projects" ];
#   };
#
# Linux gets a `systemd --user` unit, macOS a launchd agent. The module
# evaluates on both; the guards are `pkgs.stdenv.hostPlatform.isLinux` / `isDarwin`.
# Pairing (`wrekin auth`) stays a manual, one-time step: it needs a browser.
{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.wrekin;
  settingsFormat = pkgs.formats.json { };
  logDir =
    if pkgs.stdenv.hostPlatform.isDarwin then
      "${config.home.homeDirectory}/Library/Logs"
    else
      "${config.home.homeDirectory}/.local/state/wrekin";
  # The daemon spawns git, tmux and claude: give it a PATH that finds them
  # even though services do not inherit the interactive shell environment.
  servicePath = lib.concatStringsSep ":" (
    [
      (lib.makeBinPath cfg.path)
      "${config.home.homeDirectory}/.local/bin"
      "${config.home.profileDirectory}/bin"
    ]
    ++ lib.optionals pkgs.stdenv.hostPlatform.isDarwin [
      "/opt/homebrew/bin"
      "/usr/local/bin"
    ]
    ++ [
      "/usr/bin"
      "/bin"
    ]
  );
in
{
  options.services.wrekin = {
    enable = lib.mkEnableOption "the Wrekin agent, a user-level daemon that teleports Claude Code Cloud sessions onto this machine";

    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.wrekin;
      defaultText = lib.literalExpression "pkgs.wrekin";
      description = ''
        The Wrekin package. The flake's `homeManagerModules.wrekin` sets this
        to its own build; when importing `nix/module.nix` directly, add the
        flake's overlay or set it explicitly.
      '';
    };

    settings = lib.mkOption {
      type = settingsFormat.type;
      default = { };
      example = lib.literalExpression ''
        {
          name = "workstation";
          relay.url = "wss://wrekin.platyplus.io/connect";
          workspace = {
            allowedDirectories = [ "~/projects" "~/work" ];
            defaultDirectory = "~/projects";
            autoDiscover = true;
          };
          teleport = {
            terminal = "tmux";
            maxConcurrent = 4;
          };
          logging.level = "info";
        }
      '';
      description = ''
        Contents of `~/.config/wrekin/settings.json` (the keys of the design
        spec, section 3). Leave empty to let `wrekin auth` write the file with
        the defaults on first pairing.
      '';
    };

    path = lib.mkOption {
      type = lib.types.listOf lib.types.package;
      default = [
        pkgs.git
        pkgs.tmux
      ];
      defaultText = lib.literalExpression "[ pkgs.git pkgs.tmux ]";
      description = ''
        Packages added to the daemon's PATH. `~/.local/bin` (where Claude
        Code's installer puts `claude`) and the Home Manager profile are
        always included.
      '';
    };
  };

  config = lib.mkIf cfg.enable (
    lib.mkMerge [
      {
        home.packages = [ cfg.package ];

        xdg.configFile."wrekin/settings.json" = lib.mkIf (cfg.settings != { }) {
          source = settingsFormat.generate "wrekin-settings.json" cfg.settings;
        };
      }

      (lib.mkIf pkgs.stdenv.hostPlatform.isLinux {
        systemd.user.services.wrekin = {
          Unit = {
            Description = "Wrekin agent";
            Documentation = "https://github.com/plmercereau/wrekin";
            After = [ "network-online.target" ];
          };
          Service = {
            ExecStart = "${cfg.package}/bin/wrekin daemon";
            Restart = "on-failure";
            RestartSec = 5;
            Environment = [ "PATH=${servicePath}" ];
          };
          Install = {
            WantedBy = [ "default.target" ];
          };
        };
      })

      (lib.mkIf pkgs.stdenv.hostPlatform.isDarwin {
        launchd.agents.wrekin = {
          enable = true;
          config = {
            Label = "io.platyplus.wrekin";
            ProgramArguments = [
              "${cfg.package}/bin/wrekin"
              "daemon"
            ];
            RunAtLoad = true;
            KeepAlive = true;
            EnvironmentVariables = {
              PATH = servicePath;
            };
            StandardOutPath = "${logDir}/wrekin.launchd.log";
            StandardErrorPath = "${logDir}/wrekin.launchd.log";
          };
        };
      })
    ]
  );
}
