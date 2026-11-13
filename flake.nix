{
  description = "Visualiser for linuxmatters.sh";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    tailor.url = "github:wimpysworld/tailor/latest";
  };

  outputs =
    {
      self,
      nixpkgs,
      tailor,
      ...
    }:
    let
      supportedSystems = [
        "x86_64-linux"
        "aarch64-darwin"
        "aarch64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          package = import ./nix/package.nix { inherit pkgs self; };
        in
        # Expose the project package under its pname and as the default package.
        pkgs.lib.optionalAttrs
          (
            builtins.pathExists ./nix/package.nix && pkgs.lib.meta.availableOn pkgs.stdenv.hostPlatform package
          )
          {
            ${package.pname} = package;
            default = package;
          }
      );
      devShells = forAllSystems (
        system:
        let
          pkgs = import nixpkgs {
            inherit system;
            overlays = [ (final: prev: { go = prev.go_1_26; }) ];
          };
          # Keep the development shell available when Tailor has no package for this system.
          tailorPkgs = tailor.packages.${system} or { };
        in
        {
          default = pkgs.mkShell {
            packages =
              with pkgs;
              [
                actionlint
                cosign
                curl
                ffmpeg-full
                gh
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
              ]
              # The Intel drivers exist only for x86_64.
              ++ pkgs.lib.optionals (pkgs.stdenv.isLinux && pkgs.stdenv.hostPlatform.isx86_64) [
                intel-media-driver # VA-API driver for Intel GPUs (iHD_drv_video.so)
                vpl-gpu-rt # oneVPL runtime for Intel GPUs (QSV backend)
              ]
              ++ (if tailorPkgs ? tailor then [ tailorPkgs.tailor ] else [ ])
              ++ import ./nix/loader.nix { inherit pkgs; };

            shellHook =
              import ./nix/hooks.nix { inherit pkgs; }
              + pkgs.lib.optionalString pkgs.stdenv.isLinux ''
                # Keep the application libraries ahead of host libraries.
                if tailor_nixos_drivers; then
                  tailor_prepend_path LD_LIBRARY_PATH "${pkgs.lib.optionalString pkgs.stdenv.hostPlatform.isx86_64 "${pkgs.vpl-gpu-rt}/lib:${pkgs.intel-media-driver}/lib:"}${pkgs.vulkan-loader}/lib"
                fi
              '';
          };
        }
      );
    };
}
