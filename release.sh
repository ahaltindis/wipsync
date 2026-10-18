#!/bin/bash

# Releases a new version to GitHub and the AUR:
#   1. sets the version in pkg/PKGBUILD, commits and pushes
#   2. creates and pushes the tag, waits for the release workflow to attach the tarball
#   3. puts the tarball's checksum in pkg/PKGBUILD, regenerates pkg/.SRCINFO, commits and pushes
#   4. clones the AUR package to a temporary folder, copies the pkg files there, commits and pushes
# Usage: ./release.sh <tag>   (e.g. v0.1.0)

set -euo pipefail

tag=${1:-}
if [[ ! $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Usage: ./release.sh <tag>   (e.g. v0.1.0)" >&2
  exit 1
fi

version=${tag#v}
pkgname=wipsync-bin
tarball=wipsync-$version-x86_64.tar.gz
release_url=https://github.com/ahaltindis/wipsync/releases/download/$tag
aur_url=ssh://aur@aur.archlinux.org/$pkgname.git

cd "$(dirname "$0")"

fail() {
  echo "$1" >&2
  exit 1
}

[[ $(git branch --show-current) == main ]] || fail "Not on main"
[[ -z $(git status --porcelain) ]] || fail "Working tree isn't clean"
git fetch --quiet --tags origin
[[ $(git rev-parse HEAD) == "$(git rev-parse origin/main)" ]] || fail "main and origin/main differ; push or pull first"
[[ -z $(git tag --list "$tag") ]] || fail "Tag $tag already exists"

echo "==> Setting version $version"
sed -i -e "s/^pkgver=.*/pkgver=$version/" -e "s/^pkgrel=.*/pkgrel=1/" pkg/PKGBUILD
if [[ -n $(git status --porcelain) ]]; then
  git commit --quiet -am "Release $version"
  git push --quiet origin main
fi

echo "==> Tagging $tag"
git tag "$tag"
git push --quiet origin "$tag"

echo "==> Waiting for the release workflow"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
for _ in $(seq 60); do
  if curl -fsSL -o "$work/$tarball.sha256" "$release_url/$tarball.sha256" 2>/dev/null; then
    break
  fi
  sleep 10
done
[[ -s $work/$tarball.sha256 ]] ||
  fail "No release after 10 minutes, see https://github.com/ahaltindis/wipsync/actions"

curl -fsSL -o "$work/$tarball" "$release_url/$tarball"
(cd "$work" && sha256sum --check --quiet "$tarball.sha256")
sha=$(cut -d' ' -f1 "$work/$tarball.sha256")

echo "==> Updating pkg files ($sha)"
sed -i "s/^sha256sums=.*/sha256sums=('$sha')/" pkg/PKGBUILD
(cd pkg && makepkg --printsrcinfo >.SRCINFO)
git add pkg/PKGBUILD pkg/.SRCINFO
git commit --quiet -m "Update checksum for $version"
git push --quiet origin main

echo "==> Updating the AUR package"
git clone --quiet "$aur_url" "$work/aur"
cp pkg/PKGBUILD pkg/.SRCINFO pkg/wipsync.install "$work/aur/"
cd "$work/aur"
makepkg --verifysource >/dev/null
git add PKGBUILD .SRCINFO wipsync.install
git commit --quiet -m "Update to $version"
# The AUR only takes master, whatever the local default branch is called;
# this matters for the very first push, when the clone is empty.
git push --quiet origin HEAD:master

echo "==> Released $version: https://aur.archlinux.org/packages/$pkgname"
