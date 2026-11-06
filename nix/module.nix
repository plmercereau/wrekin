# Home Manager module: runs the Wrekin agent as a user-level service.
#
#   services.wrekin = {
#     enable = true;
#     settings.name = "workstation";
#     settings.workspace.allowedDirectories = [ "~/projects" ];
#     settings.workspace.repositories."owner/name" = "~/projects/name";
#   };
#
# Linux gets a `systemd --user` unit, macOS a launchd agent. The module
# evaluates on both; the guards are `pkgs.stdenv.hostPlatform.isLinux` / `isDarwin`.
# Pairing (`wrekin auth`) stays a manual, one-time step: it needs a browser.
# It also signs Claude Code in (`claude auth login`) when it is not, with the
# Claude Code of `claudePackage` if the user has none of their own.
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

  # nixpkgs' claude-code is unfree: merely referencing it in a configuration
  # that does not allow unfree packages aborts the whole evaluation, so the
  # default of `claudePackage` cannot simply be `pkgs.claude-code`. It is
  # pkgs.claude-code when that package exists in the user's nixpkgs and
  # evaluates (allowUnfree, an allowUnfreePredicate that admits it, or
  # NIXPKGS_ALLOW_UNFREE=1 with --impure), and null otherwise. tryEval catches
  # nixpkgs' refusal, which is a throw.
  claudeCodeEvaluates =
    (pkgs ? claude-code) && (builtins.tryEval (builtins.seq pkgs.claude-code.outPath true)).success;

  # Where the daemon looks for `git`, `tmux` and `claude`: it execs them by
  # name (internal/gitx, internal/terminal, internal/harness/claude) and no
  # setting overrides that. Services inherit none of the interactive shell's
  # environment, so the module builds the PATH itself:
  #   1. ~/.local/bin and the Home Manager profile: the tools the user
  #      installed and updates themselves come first, notably `claude` from
  #      Claude Code's native installer, which keeps ~/.local/bin/claude
  #      current on its own; on macOS Homebrew's directories, where
  #      `brew install --cask claude-code` lands, follow;
  #   2. `services.wrekin.path`: the module's own guarantee that git, tmux and
  #      (when `claudePackage` is set) Claude Code exist;
  #   3. the system directories last.
  servicePath = lib.concatStringsSep ":" (
    [
      "${config.home.homeDirectory}/.local/bin"
      "${config.home.profileDirectory}/bin"
    ]
    ++ lib.optionals pkgs.stdenv.hostPlatform.isDarwin [
      "/opt/homebrew/bin"
      "/usr/local/bin"
    ]
    ++ lib.optional (cfg.path != [ ]) (lib.makeBinPath cfg.path)
    ++ [
      "/usr/bin"
      "/bin"
    ]
  );

  # The `wrekin` on the user's PATH gets the daemon's tools appended to its
  # PATH, as the daemon has them: `wrekin auth` checks and signs in the very
  # Claude Code a teleport launches (the module's `claudePackage` when the
  # user has none of their own), and Claude Code keeps its credentials per
  # user, so the daemon finds them. Appended, so the user's own tools win.
  cliPackage =
    if cfg.path == [ ] then
      cfg.package
    else
      pkgs.symlinkJoin {
        name = "wrekin-${lib.getVersion cfg.package}";
        paths = [ cfg.package ];
        nativeBuildInputs = [ pkgs.makeBinaryWrapper ];
        postBuild = ''
          wrapProgram $out/bin/wrekin --suffix PATH : ${lib.makeBinPath cfg.path}
        '';
        meta.mainProgram = "wrekin";
      };
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
            repositories."plmercereau/wrekin" = "~/projects/wrekin";
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

    claudePackage = lib.mkOption {
      type = lib.types.nullOr lib.types.package;
      default = if claudeCodeEvaluates then pkgs.claude-code else null;
      defaultText = lib.literalExpression "if unfree packages are allowed then pkgs.claude-code else null";
      example = lib.literalExpression "pkgs.claude-code";
      description = ''
        The Claude Code package put on the daemon's PATH, or `null` to rely
        on a Claude Code installed outside Nix: the native installer's
        `~/.local/bin/claude` and, on macOS, Homebrew's cask are on the PATH
        already, and whatever is there takes precedence over this package.

        nixpkgs' `claude-code` is unfree, so the default is that package only
        when the configuration allows it (`nixpkgs.config.allowUnfree = true`
        or an `allowUnfreePredicate` that admits `claude-code`), and `null`
        otherwise. It also lags upstream; the native installer updates itself.
      '';
    };

    path = lib.mkOption {
      type = lib.types.listOf lib.types.package;
      default = [
        pkgs.git
        pkgs.tmux
      ]
      ++ lib.optional (cfg.claudePackage != null) cfg.claudePackage;
      defaultText = lib.literalExpression "[ pkgs.git pkgs.tmux ] ++ lib.optional (claudePackage != null) claudePackage";
      description = ''
        Packages on the daemon's PATH, after `~/.local/bin`, the Home Manager
        profile and (on macOS) Homebrew, and before the system directories:
        the daemon runs `git`, `tmux` and `claude`, and finds them here when
        the user has not installed them otherwise.
      '';
    };
  };

  config = lib.mkIf cfg.enable (
    lib.mkMerge [
      {
        home.packages = [ cliPackage ];

        xdg.configFile."wrekin/settings.json" = lib.mkIf (cfg.settings != { }) {
          source = settingsFormat.generate "wrekin-settings.json" cfg.settings;
        };
      }

      (lib.mkIf pkgs.stdenv.hostPlatform.isLinux {
        systemd.user.services.wrekin = {
          Unit = {
            Description = "Wrekin agent";
            Documentation = "https://github.com/plmercereau/wrekin";
            # `After` alone orders nothing unless the target is pulled in.
            Wants = [ "network-online.target" ];
            After = [ "network-online.target" ];
          };
          Service = {
            ExecStart = "${cfg.package}/bin/wrekin daemon";
            Restart = "on-failure";
            RestartSec = 5;
            Environment = [ "PATH=${servicePath}" ];
            # The teleported sessions live in a tmux server the daemon starts,
            # inside this unit's control group: the default KillMode
            # (control-group) kills it, and every session with it, on each
            # restart, a Home Manager activation that updates Wrekin included.
            KillMode = "process";
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
            # Same reason as KillMode above: the tmux server outlives the daemon.
            AbandonProcessGroup = true;
            # launchd starts agents with PATH=/usr/bin:/bin:/usr/sbin:/sbin.
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
