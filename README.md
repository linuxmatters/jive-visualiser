<!-- tailor:social-header:start -->
<p align="center">
  <a href="https://linuxmatters.sh/jive/visualiser/"><picture><source media="(prefers-color-scheme: dark)" srcset="pages/social/hero-dark.svg"><img src="pages/social/hero-light.svg" alt="Jive Visualiser"></picture></a>
</p>
<!-- tailor:social-header:end -->

<!-- tailor:social-links:start -->
<p align="center">
  <a href="https://bsky.app/profile/linuxmatters.sh"><picture><source media="(prefers-color-scheme: dark)" srcset="pages/social/link-bluesky-dark.svg"><img src="pages/social/link-bluesky-light.svg" alt="Bluesky"></picture></a>
  <a href="https://ubuntu.social/@linuxmatters"><picture><source media="(prefers-color-scheme: dark)" srcset="pages/social/link-mastodon-dark.svg"><img src="pages/social/link-mastodon-light.svg" alt="Mastodon"></picture></a>
  <a href="https://linkedin.com/in/martinwimpress"><picture><source media="(prefers-color-scheme: dark)" srcset="pages/social/link-linkedin-dark.svg"><img src="pages/social/link-linkedin-light.svg" alt="LinkedIn"></picture></a>
</p>
<!-- tailor:social-links:end -->

<!-- tailor:social-community:start -->
<p align="center">
  <a href="https://discord.com/invite/vUsydfP"><picture><source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/linuxmatters/jive-visualiser/social-assets/community-dark.svg"><img src="https://raw.githubusercontent.com/linuxmatters/jive-visualiser/social-assets/community-light.svg" alt="Discord community"></picture></a>
</p>
<!-- tailor:social-community:end -->

Jive Visualiser transforms WAV, MP3, or FLAC podcast audio into 720p visualiser videos with spring-driven real-time audio frequency bars. Formerly known as Jivefire. Website: [linuxmatters.sh/jive/visualiser](https://linuxmatters.sh/jive/visualiser/).

Your podcast audio deserves more than a static image on YouTube. Jive Visualiser turns recordings into discrete frequency bars that breathe with your dialogue, rise with your laughter, and groove through every frequency.

## Features

- 🖼️ **Thumbnail generator**: YouTube-style PNG with your title, saved alongside the video.
- 🎬 **1280×720 at 30 fps**: H.264/AAC YouTube-ready MP4, no questions asked.
  - 🎚️ **64 discrete frequency bars**: crisp bars rather than a smeared spectrum.
  - 🪞 **Symmetric mirroring**: mirrored above and below centre to double the visual impact.
  - 🔬 **FFT-based analysis**: 2048-point Hanning window, linear frequency binning, and log-scaled amplitude.
  - ✨ **Spring-driven bar dynamics**: bars snap up immediately and spring back down with harmonica peak-hold.
- 🚀 **Streaming pipeline**: parallel RGB-to-YUV conversion with automatic GPU acceleration (NVENC, Vulkan, VA-API, QuickSync, VideoToolbox).
- 📦 **Single binary**: no Python and no separate FFmpeg installation required on Linux (`amd64`, `arm64`) or macOS (`x86_64`, Apple Silicon).

<!-- tailor:social-stats:start -->
<p align="center">
  <a href="https://github.com/linuxmatters/jive-visualiser/pulse"><picture><source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/linuxmatters/jive-visualiser/social-assets/stats-dark.svg"><img src="https://raw.githubusercontent.com/linuxmatters/jive-visualiser/social-assets/stats-light.svg" alt="Jive Visualiser statistics"></picture></a>
</p>
<!-- tailor:social-stats:end -->

## Install

First-party signed package repositories are published for Linux and macOS through [wimpys.world](https://wimpys.world/). Choose your platform below for setup instructions and automatic updates.

<!-- tailor:social-packages:start -->
<p align="center">
  <a href="https://apt.wimpys.world/#jive-visualiser"><picture><source media="(prefers-color-scheme: dark)" srcset="pages/social/pkg-apt-dark.svg"><img src="pages/social/pkg-apt-light.svg" alt="Debian and Ubuntu"></picture></a>
  <a href="https://rpm.wimpys.world/#jive-visualiser"><picture><source media="(prefers-color-scheme: dark)" srcset="pages/social/pkg-rpm-dark.svg"><img src="pages/social/pkg-rpm-light.svg" alt="Fedora and openSUSE"></picture></a>
  <a href="https://pkg.wimpys.world/#jive-visualiser"><picture><source media="(prefers-color-scheme: dark)" srcset="pages/social/pkg-pkg-dark.svg"><img src="pages/social/pkg-pkg-light.svg" alt="Arch Linux"></picture></a>
  <br>
  <a href="https://brew.wimpys.world/#jive-visualiser"><picture><source media="(prefers-color-scheme: dark)" srcset="pages/social/pkg-brew-dark.svg"><img src="pages/social/pkg-brew-light.svg" alt="macOS (Homebrew)"></picture></a>
  <a href="https://nix.wimpys.world/#jive-visualiser"><picture><source media="(prefers-color-scheme: dark)" srcset="pages/social/pkg-nix-dark.svg"><img src="pages/social/pkg-nix-light.svg" alt="Nix and NixOS"></picture></a>
</p>
<!-- tailor:social-packages:end -->

The Linux packages need glibc 2.38 or later, for example Ubuntu 24.04, Debian 13, or Fedora 39.

<details>
<summary><strong>Any platform (<code>bin</code>)</strong> - install and update the release binary directly</summary>

[bin](https://github.com/marcosnils/bin) installs and updates the release binary on Linux and macOS:

```shell
bin install github.com/linuxmatters/jive-visualiser
```

</details>

<!-- tailor:social-sponsors:start -->
<p align="center">
  <a href="https://github.com/sponsors/flexiondotorg"><picture><source media="(prefers-color-scheme: dark)" srcset="pages/social/sponsors-dark.svg"><img src="pages/social/sponsors-light.svg" alt="Jive Visualiser sponsors"></picture></a>
</p>
<!-- tailor:social-sponsors:end -->

## Quick start

### Generate video

```bash
jive-visualiser input.wav output.mp4
```

### With episode number and title

```bash
jive-visualiser --episode=42 --title="Linux Matters" input.wav output.mp4
```

### Without episode number (unnumbered audio)

```bash
jive-visualiser --title="Linux Matters" input.wav output.mp4
```

<!-- tailor:social-demo:start -->
<p align="center">
  <picture><source media="(prefers-color-scheme: dark)" srcset="pages/social/demo-dark.svg"><img src="pages/social/demo-light.svg" alt="Jive Visualiser demo"></picture>
</p>
<!-- tailor:social-demo:end -->

`--episode` is optional. Omitting it suppresses the episode number overlay completely, which suits archive or bonus audio with no episode number. Passing `--episode=0` still renders `00` on-screen (single-digit values are zero-padded, so `5` renders as `05`).

### Example

<div align="center">
  <a href="https://video.linuxmatters.net/w/4whYe7wNzcSr3dWL98zMTp" target="_blank">
    <img alt="Linux Matters: Episode 65 (macOS Made Me Snap)" src=".github/thumbnail.png" width="640">
  </a>
</div>

## Build

Jive Visualiser uses [FFmpeg statigo](https://github.com/linuxmatters/ffmpeg-statigo) for FFmpeg static bindings.

```bash
# Setup or update ffmpeg-statigo submodule and library
just setup

# Build and test
just build        # Build managed binaries, then the versioned root binary
just test         # Run full vet and tests with console coverage
just lint         # Check modules, formatting, source, vulnerabilities and workflows
just lint correct # Explicitly tidy modules and format source
just test-encoder # Run the separate audio/video checks
```

Go tooling uses Tailor with CGO enabled. `just setup` is explicit. It can update Git configuration, submodules, archives and the index, and use the network. Quality commands never run setup.

The existing `.golangci.yml` remains the project policy. `just/project/build.sh` retains version injection and the root binary. Benchmarks and `test-encoder` remain separate commands. `just release x.y.z` retains unprefixed release tags.

Review the generated `nix/` files before staging them. Git-backed flakes exclude untracked files. The shell watches Nix package and hook files, but reload remains manual.

On NixOS, graphics hooks retain application libraries before host libraries without duplicate paths. They preserve explicit `ONEVPL_SEARCH_PATH`, `LIBVA_DRIVERS_PATH` and `VK_DRIVER_FILES` values, including empty values. Set `TAILOR_NIXOS_DRIVERS=0` to disable driver hooks. GPU detection no longer runs in the shell. Set `LIBVA_DRIVER_NAME` explicitly when driver selection is necessary. For Intel, use `iHD` and include the Intel package's `lib/dri` directory in `LIBVA_DRIVERS_PATH` when host drivers do not provide it.

## Why Jive Visualiser?

FFmpeg's audio visualisation filters (`showfreqs`, `showspectrum`) render continuous frequency spectra, not discrete bars. No amount of FFmpeg filter chain tuning achieves the discrete 64-bar aesthetic required for Linux Matters branding. Jive Visualiser does the FFT analysis and bar rendering in Go, then pipes frames to FFmpeg for encoding.

**Why Go over Python?** The original `djfun/audio-visualizer-python` tool is a moribund Qt5 GUI with significant tech debt. For our podcast production needs we wanted multi-architecture tools that integrate into automation pipelines.

The Jive Visualiser architecture is documented in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

<!-- tailor:social-stars:start -->
<p align="center">
  <a href="https://github.com/linuxmatters/jive-visualiser/stargazers"><picture><source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/linuxmatters/jive-visualiser/social-assets/stars-dark.svg"><img src="https://raw.githubusercontent.com/linuxmatters/jive-visualiser/social-assets/stars-light.svg" alt="Jive Visualiser star history"></picture></a>
</p>
<!-- tailor:social-stars:end -->

<!-- tailor:social-contributors:start -->
<p align="center">
  <a href="https://github.com/linuxmatters/jive-visualiser/graphs/contributors"><picture><source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/linuxmatters/jive-visualiser/social-assets/contributors-dark.svg"><img src="https://raw.githubusercontent.com/linuxmatters/jive-visualiser/social-assets/contributors-light.svg" alt="Jive Visualiser contributors"></picture></a>
</p>
<!-- tailor:social-contributors:end -->

<!-- tailor:social-footer:start -->
<p align="center">
  <a href="https://tailor.wimpys.world"><picture><source media="(prefers-color-scheme: dark)" srcset="pages/social/footer-dark.svg"><img src="pages/social/footer-light.svg" alt="Fitted by Tailor"></picture></a>
</p>
<!-- tailor:social-footer:end -->
