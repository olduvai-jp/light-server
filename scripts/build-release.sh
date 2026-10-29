#!/usr/bin/env bash
set -euo pipefail
version=${1:?version required}
mkdir -p dist
for target in linux/amd64 linux/arm64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${target%/*}
  arch=${target#*/}
  stage=$(mktemp -d)
  binary=light-server
  if [ "$os" = windows ]; then binary=light-server.exe; fi
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags='-s -w' -o "$stage/$binary" .
  cp README.md "$stage/README.md"
  if [ "$os" = windows ]; then
    destination="$PWD/dist/light-server_${version}_${os}_${arch}.zip"
    (cd "$stage" && zip -q "$destination" "$binary" README.md)
  else
    tar -czf "dist/light-server_${version}_${os}_${arch}.tar.gz" -C "$stage" "$binary" README.md
  fi
  rm -rf "$stage"
done
(cd dist && sha256sum ./*.tar.gz ./*.zip > checksums.txt)
