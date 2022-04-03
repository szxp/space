#!/bin/sh

set -e

export GOOS=linux
export GOARCH=amd64
export CGO_ENABLED=0

go get -t -v ./...
go mod tidy

VERSION="$(git describe --tags --first-parent --abbrev=10 --long --dirty --always)"
BUILDTIME="$(date +%Y%m%d-%H%M%S%z)"

go build \
	-ldflags "-extldflags '-static' -X main.version=$VERSION -X main.buildTime=$BUILDTIME" \
	-v \
	-o build/space \
	./cmd/space

docker build -t space .

