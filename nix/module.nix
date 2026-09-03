# NixOS module for msbd — the Microsandbox REST host.
#
# Usage in a flake-based system config:
#
#   {
#     imports = [ msbd.nixosModules.default ];
#     services.msbd = {
#       enable = true;
#       apiKeyFile = "/run/secrets/msbd-api-key";  # contains MSBD_API_KEY=...
#       listen = ":8099";
#     };
#   }
#
# Users and API keys can instead be managed on the host with
#   msbd users add alice --data-dir /var/lib/msbd/state
#   msbd keys create ci  --data-dir /var/lib/msbd/state
# which keeps no secret in the Nix store.
#
# The host must have KVM enabled (virtualisation in the kernel + /dev/kvm).
self:
{ config, lib, pkgs, ... }:

let
  cfg = config.services.msbd;

  # Prefer the FHS-wrapped package so the runtime-downloaded `msb` supervisor
  # and the embedded FFI .so find a standard dynamic loader on NixOS.
  defaultPackage = self.packages.${pkgs.stdenv.hostPlatform.system}.msbd-fhs;
in
{
  options.services.msbd = {
    enable = lib.mkEnableOption "the msbd Microsandbox REST host";

    package = lib.mkOption {
      type = lib.types.package;
      default = defaultPackage;
      defaultText = lib.literalExpression "msbd.packages.\${system}.msbd-fhs";
      description = "The msbd package to run (use the FHS-wrapped variant on NixOS).";
    };

    listen = lib.mkOption {
      type = lib.types.str;
      default = ":8099";
      description = "Address msbd listens on (MSBD_LISTEN).";
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Open the TCP port from `listen` in the firewall.";
    };

    apiKeyFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/run/secrets/msbd.env";
      description = ''
        Path to an EnvironmentFile (systemd format) that sets MSBD_API_KEY.

        Optional since msbd gained a persisted key store: you can instead run
        `msbd keys create <name> --data-dir <services.msbd.dataDir>` on the host
        and keep no secret in the Nix store or config. With neither, the server
        is UNAUTHENTICATED.
      '';
    };

    dataDir = lib.mkOption {
      type = lib.types.path;
      default = "/var/lib/msbd/state";
      description = ''
        Directory holding msbd's SQLite database of dashboard users, API keys
        and sessions (MSBD_DATA_DIR).

        It lives under the service StateDirectory so it survives restarts and
        redeploys. Manage its contents with `msbd users` / `msbd keys`, passing
        the same --data-dir (or MSBD_DATA_DIR).
      '';
    };

    sessionTtlSecs = lib.mkOption {
      type = lib.types.int;
      default = 0;
      description = ''
        Dashboard login lifetime in seconds; 0 uses the built-in default of 12h
        (MSBD_SESSION_TTL_SECS).
      '';
    };

    defaultImage = lib.mkOption {
      type = lib.types.str;
      default = "microsandbox/python";
      description = "Default OCI image for new sandboxes (MSBD_DEFAULT_IMAGE).";
    };

    maxSandboxes = lib.mkOption {
      type = lib.types.int;
      default = 0;
      description = "Max concurrent sandboxes; 0 = unlimited (MSBD_MAX_SANDBOXES).";
    };

    createTimeoutSecs = lib.mkOption {
      type = lib.types.int;
      default = 300;
      description = "Sandbox create timeout in seconds (MSBD_CREATE_TIMEOUT_SECS).";
    };

    logLevel = lib.mkOption {
      type = lib.types.enum [ "debug" "info" "warn" "error" ];
      default = "info";
      description = "Log verbosity (MSBD_LOG_LEVEL).";
    };

    environment = lib.mkOption {
      type = lib.types.attrsOf lib.types.str;
      default = { };
      description = "Extra environment variables for the service.";
    };
  };

  config = lib.mkIf cfg.enable {
    # msbd boots microVMs through libkrun; the host needs KVM.
    boot.kernelModules = [ "kvm" ]
      ++ lib.optional (pkgs.stdenv.hostPlatform.isx86_64) "kvm-intel"
      ++ lib.optional (pkgs.stdenv.hostPlatform.isx86_64) "kvm-amd";

    networking.firewall.allowedTCPPorts = lib.mkIf cfg.openFirewall [
      (lib.toInt (lib.last (lib.splitString ":" cfg.listen)))
    ];

    systemd.services.msbd = {
      description = "msbd — Microsandbox REST host";
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      wantedBy = [ "multi-user.target" ];

      environment = {
        MSBD_LISTEN = cfg.listen;
        MSBD_DEFAULT_IMAGE = cfg.defaultImage;
        MSBD_MAX_SANDBOXES = toString cfg.maxSandboxes;
        MSBD_CREATE_TIMEOUT_SECS = toString cfg.createTimeoutSecs;
        MSBD_SESSION_TTL_SECS = toString cfg.sessionTtlSecs;
        MSBD_LOG_LEVEL = cfg.logLevel;
        # Users, API keys and dashboard sessions. Kept under StateDirectory so
        # it outlives redeploys; msbd creates it 0700 and the database 0600.
        MSBD_DATA_DIR = cfg.dataDir;
        # EnsureInstalled + the OCI image cache live here; StateDirectory below
        # maps it to /var/lib/msbd.
        HOME = "/var/lib/msbd";
      } // cfg.environment;

      serviceConfig = {
        ExecStart = lib.getExe cfg.package;
        Restart = "on-failure";
        RestartSec = 5;

        EnvironmentFile = lib.mkIf (cfg.apiKeyFile != null) cfg.apiKeyFile;

        # Runs as a dynamic user that's a member of the kvm group and is allowed
        # to open /dev/kvm.
        DynamicUser = true;
        SupplementaryGroups = [ "kvm" ];
        DeviceAllow = [ "/dev/kvm rw" ];

        StateDirectory = "msbd";
        StateDirectoryMode = "0700";
        WorkingDirectory = "/var/lib/msbd";

        # No write/read timeout in msbd by design (Run can block on long
        # builds); give systemd a generous startup window for the first-run
        # runtime download.
        TimeoutStartSec = "300";

        # Light hardening that doesn't fight the microVM runtime.
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
      };
    };
  };
}
