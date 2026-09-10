#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
mkdir -p .output/payload
go vet ./...
go test -race -p 2 ./...
mkdir -p .output/payload/bin
for name in semantic-robot-bundle semantic-robot-instance; do
 CGO_ENABLED=0 go build -trimpath -tags netgo,osusergo -o ".output/payload/bin/$name" "./cmd/$name"
 ! readelf -lW ".output/payload/bin/$name" | grep -q INTERP
 ! readelf -dW ".output/payload/bin/$name" | grep -q '(NEEDED)'
 status=0; ".output/payload/bin/$name" --help || status=$?
 [[ "$status" == 0 || "$status" == 2 ]]
done
cp -a type-packages .output/payload/
