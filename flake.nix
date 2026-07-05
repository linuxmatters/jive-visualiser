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
        pkgs = import nixpkgs { inherit system; };
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
            ];

          # Make GPU drivers visible for hardware-accelerated encoding
          # Linux: NixOS mounts GPU drivers under /run/opengl-driver/lib
          # macOS: VideoToolbox uses system frameworks, no LD_LIBRARY_PATH needed
          shellHook = pkgs.lib.optionalString pkgs.stdenv.isLinux ''
            # If the opengl driver directory exists, prepend it to LD_LIBRARY_PATH
            if [ -d "/run/opengl-driver/lib" ]; then
              if [ -z "$LD_LIBRARY_PATH" ]; then
                export LD_LIBRARY_PATH="/run/opengl-driver/lib"
              else
                export LD_LIBRARY_PATH="/run/opengl-driver/lib:$LD_LIBRARY_PATH"
              fi
            fi
            # Add vulkan-loader library path for h264_vulkan encoder
            export LD_LIBRARY_PATH="${pkgs.vulkan-loader}/lib:$LD_LIBRARY_PATH"
            # Add Intel media driver, and oneVPL libraries for QSV
            export LD_LIBRARY_PATH="${pkgs.intel-media-driver}/lib:$LD_LIBRARY_PATH"
            export LD_LIBRARY_PATH="${pkgs.vpl-gpu-rt}/lib:$LD_LIBRARY_PATH"
            # oneVPL runtime search path for QSV (11th gen+ Intel only)
            export ONEVPL_SEARCH_PATH="${pkgs.vpl-gpu-rt}/lib"

            # VA-API driver discovery for libva
            # Use system drivers if available, fall back to nix package for Intel
            if [ -d "/run/opengl-driver/lib/dri" ]; then
              export LIBVA_DRIVERS_PATH="/run/opengl-driver/lib/dri"
            fi
            # Auto-detect VA-API driver based on GPU vendor (prefer Intel for VA-API)
            if lspci -d ::0300 2>/dev/null | grep -qi intel; then
              export LIBVA_DRIVER_NAME="iHD"
              # Ensure Intel driver path is set even without system drivers
              export LIBVA_DRIVERS_PATH="${pkgs.intel-media-driver}/lib/dri:''${LIBVA_DRIVERS_PATH:-}"
            elif lspci -d ::0300 2>/dev/null | grep -qi amd; then
              export LIBVA_DRIVER_NAME="radeonsi"
            elif lspci -d ::0300 2>/dev/null | grep -qi nvidia; then
              export LIBVA_DRIVER_NAME="nvidia"
            fi

            # Vulkan ICD discovery: tell vulkan-loader where to find GPU drivers
            # NixOS installs ICDs under /run/opengl-driver/share/vulkan/icd.d/
            # This enables NVIDIA Vulkan on systems with both Intel iGPU and NVIDIA dGPU
            if [ -d "/run/opengl-driver/share/vulkan/icd.d" ]; then
              export VK_DRIVER_FILES=$(find /run/opengl-driver/share/vulkan/icd.d -name '*.json' 2>/dev/null | tr '\n' ':' | sed 's/:$//')
            fi
          '';
        };
      }
    );
}
