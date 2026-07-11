#!/bin/sh
set -eu
mkdir -p build
docker run --rm -v "$PWD":/src -w /src golang:1.22-alpine \
  sh -c 'go test ./... && GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o build/retouch-afvalwijzer-armv7l . && sha256sum build/retouch-afvalwijzer-armv7l > build/SHA256SUMS'
