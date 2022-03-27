#!/bin/sh

set -e

GOOS=linux
GOARCH=amd64


go get -t -v ./...
go mod tidy

VERSION="$(git describe --tags --first-parent --abbrev=10 --long --dirty --always)"
BUILDTIME="$(date +%Y%m%d-%H%M%S%z)"

GOOS=$GOOS GOARCH=$GOARCH go build \
	-ldflags "-X main.version=$VERSION -X main.buildTime=$BUILDTIME" \
	-v \
	-o build/space \
	./cmd/space

docker build -t space .

