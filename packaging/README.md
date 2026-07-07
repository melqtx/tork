# Packaging tork

This directory keeps package recipes for downstream installs.

Current release: `v0.1.0`

## Files

- `aur/PKGBUILD` is the Arch AUR recipe. The `tork` package is live on AUR.
- `homebrew/tork.rb` is ready for a future `melqtx/homebrew-tap` repo.
- `nix/package.nix` is the reusable Nix package expression.
- `../flake.nix` wraps the Nix package so users can run `nix run github:melqtx/tork`.

## Release Flow

When cutting a new release:

1. Update the version in:
   - `packaging/aur/PKGBUILD`
   - `packaging/homebrew/tork.rb`
   - `packaging/nix/package.nix`
   - `flake.nix`
2. Tag and publish the release:

   ```sh
   git tag -a v0.1.1 main -m "v0.1.1"
   git push origin v0.1.1
   gh release create v0.1.1 --verify-tag --title "v0.1.1" --generate-notes
   ```

3. Get the GitHub source tarball hash:

   ```sh
   curl -L https://github.com/melqtx/tork/archive/refs/tags/v0.1.1.tar.gz -o /tmp/tork-0.1.1.tar.gz
   shasum -a 256 /tmp/tork-0.1.1.tar.gz
   ```

4. Put that hash in:
   - `packaging/aur/PKGBUILD`
   - `packaging/homebrew/tork.rb`

5. Refresh the Nix hashes with fake hashes first, then let Nix print the real values:

   ```sh
   nix-build -E 'with import <nixpkgs> {}; callPackage ./packaging/nix/package.nix {}'
   ```

6. Verify the flake:

   ```sh
   nix flake check
   nix run . -- --version
   ```

## Arch AUR

The AUR repo is `ssh://aur@aur.archlinux.org/tork.git`.

Update it from this repo root on an Arch machine:

```sh
git clone ssh://aur@aur.archlinux.org/tork.git /tmp/tork-aur
cp packaging/aur/PKGBUILD /tmp/tork-aur/PKGBUILD
cd /tmp/tork-aur
makepkg -Csr
makepkg --printsrcinfo > .SRCINFO
git add PKGBUILD .SRCINFO
git commit -m "update tork"
git push origin master
```

Only `PKGBUILD` and `.SRCINFO` belong in the AUR repo.

## Homebrew

The formula is ready, but the tap repo is not published yet.

When you make the tap:

```sh
brew tap-new melqtx/tap
cp packaging/homebrew/tork.rb "$(brew --repository melqtx/tap)/Formula/tork.rb"
brew install --build-from-source melqtx/tap/tork
brew test melqtx/tap/tork
brew audit --strict --online melqtx/tap/tork
gh repo create melqtx/homebrew-tap --public --source "$(brew --repository melqtx/tap)" --push
```

Then users can install with:

```sh
brew install melqtx/tap/tork
```

## Nix

The flake is the easiest path:

```sh
nix run github:melqtx/tork
nix profile install github:melqtx/tork
```

The package expression also works without flakes:

```sh
nix-build -E 'with import <nixpkgs> {}; callPackage ./packaging/nix/package.nix {}'
```

For NUR later, create a separate `nur-packages` repo with a top-level
`default.nix` that exposes `tork = pkgs.callPackage ./pkgs/tork {};`, then open
a PR to `nix-community/NUR` adding that repo to `repos.json`.
