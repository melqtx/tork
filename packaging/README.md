# Packaging tork

This directory holds packaging templates for the first public release.

## Release Prerequisites

1. Push the repo to `https://github.com/melqtx/tork`.
2. Create a stable tag, for example `v0.1.0`.
3. Make sure this works from outside the repo:

   ```sh
   go install github.com/melqtx/tork/cmd/tork@v0.1.0
   tork --version
   ```

4. Generate the release tarball SHA256:

   ```sh
   curl -L https://github.com/melqtx/tork/archive/refs/tags/v0.1.0.tar.gz -o tork-0.1.0.tar.gz
   shasum -a 256 tork-0.1.0.tar.gz
   ```

## Homebrew

Start with a personal tap before trying `homebrew-core`:

```sh
brew tap-new melqtx/tap
cp packaging/homebrew/tork.rb "$(brew --repository melqtx/tap)/Formula/tork.rb"
brew install --build-from-source melqtx/tap/tork
brew test melqtx/tap/tork
brew audit --strict --online melqtx/tap/tork
```

Replace `REPLACE_WITH_RELEASE_TARBALL_SHA256` first.

## Arch AUR

Copy `packaging/aur/PKGBUILD` into a separate AUR checkout:

```sh
makepkg -si
makepkg --printsrcinfo > .SRCINFO
```

Replace `REPLACE_WITH_RELEASE_TARBALL_SHA256` first. Commit both `PKGBUILD`
and `.SRCINFO` to the AUR package repository.

## Nix / NUR

Use `packaging/nix/package.nix` as the package expression for NUR or an overlay.
For the two hashes, Nix usually tells you the expected hash when you first
build with a fake one:

```sh
nix-build -E 'with import <nixpkgs> {}; callPackage ./packaging/nix/package.nix {}'
```

Replace `sha256-REPLACE_WITH_SOURCE_HASH` and `sha256-REPLACE_WITH_VENDOR_HASH`
with the values Nix reports.
