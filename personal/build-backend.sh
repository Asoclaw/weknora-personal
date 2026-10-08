#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
version=$(tr -d '\r\n ' < VERSION)
version=${version#v}
commit=${PERSONAL_BUILD_COMMIT:-$(git rev-parse --short HEAD)}
build_time=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go_version=$(go env GOVERSION)
pkg=github.com/Tencent/WeKnora/internal/handler
go build -tags anydoc -ldflags="-X $pkg.Version=$version -X $pkg.Edition=standard -X $pkg.CommitID=$commit -X $pkg.BuildTime=$build_time -X $pkg.GoVersion=$go_version -X google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn" -o WeKnora-personal ./cmd/server
