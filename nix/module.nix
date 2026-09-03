# NixOS module for msbd — the Microsandbox REST host.
#
# Usage in a flake-based system config:
#
#   {
#     imports = [ msbd.nixosModules.default ];
#     services.msbd = {
#       enable = true;
#       apiKeyTokenFile = "/run/secrets/msbd-token";  # file containing ONLY the token
#       listen = ":8099";
#     };
#   }
#
# Users and API keys can instead be managed on the host with
#   msbd users add alice --data-dir /var/lib/msbd/state
#   msbd keys create ci  --data-dir /var/lib/msbd/state
# which keeps no secret in the Nix store. See "Managing state" below — the
# service runs as a DynamicUser, which constrains how you run those commands.
#
# URL LAYOUT (changed): the REST API is served under /api/v1, and the web
# dashboard owns the root (/, /sandboxes, /volumes, …). Health probes stay at
# /healthz and /readyz. If you front this with nginx/Caddy, proxy / as a whole
# rather than a /v1 prefix.
#
# DASHBOARD LOCK: msbd refuses to serve an UNAUTHENTICATED dashboard while the
# REST API requires a token — that would be a way around the token. Because the
# dashboard is now at the root, such a deployment answers `GET /` with a 403
# "Dashboard locked" page. Clear it by creating an account (`msbd users add`),
# setting `dashboardUser` + a password via `environmentFile`, or, as a last
# resort, `dashboardAllowInsecure = true`. Probes on /healthz and /readyz are
# unaffected, so point uptime checks there rather than at /.
#
# MANAGING STATE: `serviceConfig.DynamicUser` means the StateDirectory really
# lives at /var/lib/private/msbd (systemd exposes it to the unit as
# /var/lib/msbd) and is owned by a UID allocated for this unit. Running
# `msbd users add --data-dir ...` as root against a service-created database
# works, but SQLite will create its -wal/-shm sidecars as ROOT, which the
# service can then fail to write. Prefer running admin commands as the service
# identity:
#
#   systemd-run --pty --same-dir --wait --collect \
#     --property=DynamicUser=yes --property=StateDirectory=msbd \
#     --property=SupplementaryGroups=kvm \
#     /path/to/msbd users add alice --data-dir /var/lib/msbd/state
#
# (resolve the binary with `systemctl show -p ExecStart --value msbd`), or
# manage users and keys from the dashboard's Settings pages instead.
#
# The host must have KVM enabled (virtualisation in the kernel + /dev/kvm).
self:
{ config, lib, pkgs, ... }:

let
  cfg = config.services.msbd;

  # Prefer the FHS-wrapped package so the runtime-downloaded `msb` supervisor
  # and the embedded FFI .so find a standard dynamic loader on NixOS.
  defaultPackage = self.packages.${pkgs.stdenv.hostPlatform.system}.msbd-fhs;

  # systemd accepts a list of EnvironmentFiles. `apiKeyFile` is the legacy
  # spelling of `environmentFile`; both are honoured so no existing config
  # breaks (see the deprecation warning below).
  envFiles = lib.filter (f: f != null) [ cfg.environmentFile cfg.apiKeyFile ];
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

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/run/secrets/msbd.env";
      description = ''
        Path to a systemd EnvironmentFile holding secrets that must not enter
        the Nix store, one `KEY=value` per line. This is the place for
        MSBD_API_KEY and MSBD_DASHBOARD_PASS.

        For a secret-manager file containing ONLY the bare token (agenix, sops
        and friends usually produce these), use `apiKeyTokenFile` instead —
        msbd reads that format natively.
      '';
    };

    apiKeyTokenFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/run/secrets/msbd-token";
      description = ''
        Path to a file whose ENTIRE CONTENTS are the bearer token
        (MSBD_API_KEY_FILE). msbd reads and trims it at startup.

        This is the natural fit for agenix/sops-nix outputs. Do not point it at
        a `KEY=value` environment file — the whole line would become the token.
        Use `environmentFile` for that shape.
      '';
    };

    apiKeyFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/run/secrets/msbd.env";
      description = ''
        DEPRECATED — use `environmentFile` (identical behaviour) or
        `apiKeyTokenFile` (bare-token file).

        Despite the name this is a systemd EnvironmentFile that must set
        `MSBD_API_KEY=...`, not a file containing the token. It keeps working
        unchanged; the clearer options exist because the original name invited
        exactly the wrong file format.
      '';
    };

    dataDir = lib.mkOption {
      type = lib.types.path;
      default = "/var/lib/msbd/state";
      description = ''
        Directory holding msbd's SQLite database of dashboard users, API keys
        and sessions (MSBD_DATA_DIR).

        It lives under the service StateDirectory so it survives restarts and
        redeploys. See "Managing state" in this module's header before running
        `msbd users` / `msbd keys` against it by hand.
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

    dashboard = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = ''
        Serve the web dashboard at the root (MSBD_DASHBOARD). Set false to run
        a headless REST-only server, in which case / returns 404.
      '';
    };

    dashboardUser = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "admin";
      description = ''
        LEGACY single-account HTTP Basic username for the dashboard
        (MSBD_DASHBOARD_USER).

        There is deliberately no `dashboardPass` option: it would write the
        password into the world-readable Nix store. Set MSBD_DASHBOARD_PASS via
        `environmentFile`, or skip Basic auth entirely and create a real account
        with `msbd users add`, which supersedes it.
      '';
    };

    dashboardAllowInsecure = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Serve the dashboard with NO authentication even when the REST API
        requires a token (MSBD_DASHBOARD_ALLOW_INSECURE).

        This overrides a deliberate safety refusal: the dashboard can drive
        every sandbox, so an unauthenticated one at the root is a full bypass of
        the bearer token. Only sane when something in front of msbd is doing the
        authenticating.
      '';
    };

    hostPaths = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      example = [ "/srv/msbd-transfer" ];
      description = ''
        Allowlisted HOST path prefixes for the files/copy-from-host,
        files/copy-to-host and snapshot export/import endpoints
        (MSBD_HOST_PATHS).

        These read and write the daemon's own filesystem, not a guest's. The
        default empty list DENIES every such request with a 403 — keep it that
        way unless you need host transfers, and then list the narrowest prefix
        that works.
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

    pullTimeoutSecs = lib.mkOption {
      type = lib.types.int;
      default = 900;
      description = ''
        Deadline for a standalone image pull (MSBD_PULL_TIMEOUT_SECS). Larger
        than the create timeout because a cold pull of a big image can outlast
        a boot.
      '';
    };

    jobTtlSecs = lib.mkOption {
      type = lib.types.int;
      default = 0;
      description = ''
        How long a finished async job's output is retained before eviction;
        0 uses the built-in default of 15m (MSBD_JOB_TTL_SECS).
      '';
    };

    jobMaxBytes = lib.mkOption {
      type = lib.types.int;
      default = 0;
      description = ''
        Per-stream cap on an async job's stdout/stderr ring buffer; 0 uses the
        built-in default of 1 MiB (MSBD_JOB_MAX_BYTES).
      '';
    };

    shutdownTimeoutSecs = lib.mkOption {
      type = lib.types.int;
      default = 60;
      description = ''
        Graceful-drain deadline on stop/restart (MSBD_SHUTDOWN_TIMEOUT_SECS).
        Keep TimeoutStopSec above this so systemd lets the drain finish.
      '';
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
    warnings =
      lib.optional (cfg.apiKeyFile != null) ''
        services.msbd.apiKeyFile is deprecated and will be removed. It is a
        systemd EnvironmentFile (must contain MSBD_API_KEY=...), which the name
        does not suggest. Rename it to services.msbd.environmentFile for the
        same behaviour, or switch to services.msbd.apiKeyTokenFile if the file
        holds only the bare token.
      '';

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
        MSBD_PULL_TIMEOUT_SECS = toString cfg.pullTimeoutSecs;
        MSBD_JOB_TTL_SECS = toString cfg.jobTtlSecs;
        MSBD_JOB_MAX_BYTES = toString cfg.jobMaxBytes;
        MSBD_SHUTDOWN_TIMEOUT_SECS = toString cfg.shutdownTimeoutSecs;
        MSBD_SESSION_TTL_SECS = toString cfg.sessionTtlSecs;
        MSBD_LOG_LEVEL = cfg.logLevel;
        MSBD_DASHBOARD = lib.boolToString cfg.dashboard;
        MSBD_DASHBOARD_ALLOW_INSECURE = lib.boolToString cfg.dashboardAllowInsecure;
        # Users, API keys and dashboard sessions. Kept under StateDirectory so
        # it outlives redeploys; msbd creates it 0700 and the database 0600.
        MSBD_DATA_DIR = cfg.dataDir;
        # EnsureInstalled + the OCI image cache live here; StateDirectory below
        # maps it to /var/lib/msbd.
        HOME = "/var/lib/msbd";
      }
      # Only set when configured: an empty MSBD_HOST_PATHS and an unset one mean
      # the same thing (deny), but an empty MSBD_DASHBOARD_USER would look like
      # a deliberate blank username.
      // lib.optionalAttrs (cfg.hostPaths != [ ]) {
        MSBD_HOST_PATHS = lib.concatStringsSep "," cfg.hostPaths;
      }
      // lib.optionalAttrs (cfg.dashboardUser != null) {
        MSBD_DASHBOARD_USER = cfg.dashboardUser;
      }
      // lib.optionalAttrs (cfg.apiKeyTokenFile != null) {
        MSBD_API_KEY_FILE = toString cfg.apiKeyTokenFile;
      }
      // cfg.environment;

      serviceConfig = {
        ExecStart = lib.getExe cfg.package;
        Restart = "on-failure";
        RestartSec = 5;

        EnvironmentFile = lib.mkIf (envFiles != [ ]) envFiles;

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

        # Let the in-process graceful drain finish before systemd escalates to
        # SIGKILL, with a little headroom over msbd's own deadline.
        TimeoutStopSec = toString (cfg.shutdownTimeoutSecs + 15);

        # Light hardening that doesn't fight the microVM runtime.
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
      };
    };
  };
}
