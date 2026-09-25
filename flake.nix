{
  description = "Visualiser for linuxmatters.sh";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:

    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ (final: prev: { go = prev.go_1_26; }) ];
        };
      in
      {
        devShells.default = pkgs.mkShell {
          packages =
            with pkgs;
            [
              actionlint
              cosign
              curl
              ffmpeg-full
              gnugrep
              gcc
              go_1_26
              gocyclo
              # golangci-lint follows the nixpkgs pin in flake.lock (currently
              # 2.12.2). Keep this in step with the version: pin on
              # golangci/golangci-lint-action in .github/workflows/builder.yml so
              # local and CI lint results agree.
              golangci-lint
              ineffassign
              just
            ]
            ++ pkgs.lib.optionals pkgs.stdenv.isLinux [
              vulkan-loader # Required for Vulkan accelerated encoders on Linux
              intel-media-driver # VA-API driver for Intel GPUs (iHD_drv_video.so)
              vpl-gpu-rt # oneVPL runtime for Intel GPUs (QSV backend)
            ]
            ++ import ./nix/loader.nix { inherit pkgs; };

          shellHook =
            import ./nix/hooks.nix { inherit pkgs; }
            + pkgs.lib.optionalString pkgs.stdenv.isLinux ''
              # Keep the application libraries ahead of host libraries.
              if tailor_nixos_drivers; then
                tailor_prepend_path LD_LIBRARY_PATH "${pkgs.vpl-gpu-rt}/lib:${pkgs.intel-media-driver}/lib:${pkgs.vulkan-loader}/lib"
              fi
            '';
        };
      }
    );
}
