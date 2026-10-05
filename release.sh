#!/usr/bin/env bash
# release.sh vX.Y.Z "release notes"
# Tags, builds every platform, publishes the GitHub release, and bumps the
# Homebrew tap to the new tarball. Run from a clean checkout of main.
set -euo pipefail
V=${1:?version like v0.3.0}; NOTES=${2:?release notes}
TAP=${TAP:-$HOME/ws/homebrew-tap}
cd "$(dirname "$0")"
[ -z "$(git status --porcelain)" ] || { echo "working tree not clean" >&2; exit 1; }
go vet ./... && go test ./... >/dev/null
export GH_TOKEN; GH_TOKEN=$(gh auth token -u brady1408)
git tag -a "$V" -m "dotamp $V" && git push -q origin "$V"
rm -rf dist && mkdir -p dist
for t in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64; do
  os=${t%/*}; arch=${t#*/}; ext=""; [ "$os" = windows ] && ext=".exe"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -ldflags "-s -w -X main.version=$V" -o "dist/dotamp-$os-$arch$ext" ./cmd/dotamp
done
( cd dist && for f in dotamp-*; do case "$f" in *.exe) zip -q "${f%.exe}.zip" "$f" && rm "$f";; *) tar -czf "$f.tar.gz" "$f" && rm "$f";; esac; done && sha256sum -- * > SHA256SUMS )
gh release create "$V" dist/* --title "dotamp $V" --notes "$NOTES" >/dev/null
echo "released https://github.com/brady1408/dotamp/releases/tag/$V"
# Homebrew tap: point the formula at the new source tarball.
SHA=$(curl -sL "https://github.com/brady1408/dotamp/archive/refs/tags/$V.tar.gz" | sha256sum | cut -d' ' -f1)
sed -i -E "s|refs/tags/v[0-9.]+\.tar\.gz|refs/tags/$V.tar.gz|; s|sha256 \"[0-9a-f]{64}\"|sha256 \"$SHA\"|" "$TAP/Formula/dotamp.rb"
( cd "$TAP" && git add Formula/dotamp.rb && git commit -q -m "dotamp ${V#v}" && git push -q origin main )
echo "tap bumped to ${V#v}"
