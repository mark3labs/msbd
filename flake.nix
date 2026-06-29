{
  description = "msbd — Microsandbox REST host (cgo wrapper around the microsandbox Go SDK)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    { self
    , nixpkgs
    , flake-utils
    }:
    let
      # microVM boot needs /dev/kvm, so the daemon is Linux-only by design
      # (this mirrors .goreleaser.yaml — no macOS/Windows targets).
      supportedSystems = [ "x86_64-linux" "aarch64-linux" ];

      # Human version, single source of truth shared with git tags: tag a
      # release `v$(cat VERSION)`. Nix flakes can't read git tags (only the
      # commit is fetched), so the VERSION file is what ties a flake build to a
      # release number. GoReleaser overrides this from the actual tag at
      # release time.
      version = nixpkgs.lib.strings.removeSuffix "\n" (builtins.readFile ./VERSION);

      # Commit + build date from flake metadata, so a `nix build` off a tagged
      # checkout records exactly which revision it came from.
      commit = self.rev or self.dirtyRev or "unknown";
      date =
        let d = self.lastModifiedDate or "";
        in
        if d == "" then "unknown"
        else "${builtins.substring 0 4 d}-${builtins.substring 4 2 d}-${builtins.substring 6 2 d}";
    in
    flake-utils.lib.eachSystem supportedSystems
      (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        inherit (pkgs) lib;

        # Libraries the *prebuilt* microsandbox FFI `.so` (go:embed'd into the
        # binary and extracted to ~/.microsandbox at runtime) and the
        # downloaded `msb` supervisor dlopen/link against. glibc (NOT musl) and
        # libcap-ng are the load-bearing ones; the rest are belt-and-suspenders
        # for the runtime download + TLS image pulls.
        runtimeLibs = with pkgs; [
          stdenv.cc.cc.lib # libgcc_s / libstdc++
          glibc
          libcap_ng
          openssl
          zlib
        ];

        # ---------------------------------------------------------------------
        # The msbd binary. cgo is enabled, but the only thing the C side links
        # at build time is libdl (-ldl): the Rust FFI library is loaded via
        # dlopen at runtime, so no Rust toolchain is needed to compile msbd.
        # ---------------------------------------------------------------------
        msbd = pkgs.buildGoModule {
          pname = "msbd";
          inherit version;

          src = lib.cleanSource ./.;

          # Single direct dependency (the microsandbox SDK). If go.mod changes,
          # run `nix build .#msbd` and replace this with the hash Nix reports.
          vendorHash = "sha256-1HYRvsQRxkw/8AKONVggJD/BvoYJMaHggOutxf1hmZA=";

          subPackages = [ "cmd/msbd" ];

          env.CGO_ENABLED = "1";

          ldflags = [
            "-s"
            "-w"
            "-X main.version=${version}"
            "-X main.commit=${commit}"
            "-X main.date=${date}"
          ];

          # The repo's only tests are unit-level; the microVM integration tests
          # need /dev/kvm and are gated behind -tags integration (not built in
          # the sandbox).
          doCheck = true;

          meta = {
            description = "HTTP server that wraps the microsandbox Go SDK and exposes a REST API for local microVMs";
            homepage = "https://github.com/mark3labs/msbd";
            license = lib.licenses.asl20;
            mainProgram = "msbd";
            platforms = supportedSystems;
          };
        };

        # ---------------------------------------------------------------------
        # NixOS-friendly wrapper.
        #
        # msbd itself is a Nix-built binary and runs fine anywhere. The problem
        # is what it execs/dlopens at RUNTIME: the `msb` supervisor it downloads
        # and the embedded FFI `.so` are vanilla glibc binaries that expect a
        # dynamic loader at /lib64/ld-linux-*.so.2 and libcap-ng.so.0 on a
        # standard search path. NixOS has neither. buildFHSEnv gives those
        # downloaded binaries the FHS layout they assume.
        #
        # On a regular glibc distro (Debian/Ubuntu/Fedora) you can just use the
        # plain `msbd` package — the FHS wrapper is only needed on NixOS.
        # ---------------------------------------------------------------------
        msbd-fhs = pkgs.buildFHSEnv {
          name = "msbd";

          targetPkgs = p: (with p; [
            msbd
            glibc
            libcap_ng
            openssl
            cacert
            zlib
            curl
            stdenv.cc.cc.lib
          ]);

          runScript = "${msbd}/bin/msbd";

          meta.mainProgram = "msbd";
        };
      in
      {
        packages = {
          default = msbd;
          inherit msbd msbd-fhs;
        };

        # `nix run` uses the FHS wrapper so it Just Works on NixOS hosts too.
        apps.default = {
          type = "app";
          program = "${msbd-fhs}/bin/msbd";
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            golangci-lint
            go-task
            gcc
            pkg-config
            libcap_ng
            openssl
          ];

          env.CGO_ENABLED = "1";

          # So a `go run ./cmd/msbd` in the shell can dlopen the runtime FFI.
          LD_LIBRARY_PATH = lib.makeLibraryPath runtimeLibs;

          shellHook = ''
            echo "msbd dev shell — go $(go version | awk '{print $3}'), CGO_ENABLED=1"
            echo "note: booting a microVM needs /dev/kvm on this host."
          '';
        };

        formatter = pkgs.nixpkgs-fmt;
      })
    // {
      # NixOS module to run msbd as a system service with /dev/kvm access.
      nixosModules.default = import ./nix/module.nix self;
      nixosModules.msbd = self.nixosModules.default;
    };
}
