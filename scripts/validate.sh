#!/usr/bin/env bash
set -euo pipefail
race=false; scan=false
for option in "$@"; do
  case "$option" in --race) race=true ;; --scan) scan=true ;; *) printf 'Unknown option: %s\n' "$option" >&2; exit 2 ;; esac
done
cd "$(dirname "$0")/.."
base="${DEVELOPMENT_ROOT:-${RUNNER_TEMP:+$RUNNER_TEMP/development}}"
if [[ -z "$base" ]]; then base=/mnt/v/Development; fi
if [[ ! -d "$base" && -z "${RUNNER_TEMP:-}" ]]; then printf 'Approved development storage is unavailable.\n' >&2; exit 1; fi
project="$base/go-oracle-session"; run="$(date -u +%Y%m%d-%H%M%S)-$$"
export TMPDIR="$project/tmp/validate-$run" TEMP="$project/tmp/validate-$run" TMP="$project/tmp/validate-$run"
export GOTMPDIR="$TMPDIR/go-build" GOCACHE="$project/caches/go-build" GOMODCACHE="$project/caches/go-modules"
output="$project/artifacts/runs/validate-$run"
mkdir -p "$TMPDIR" "$GOTMPDIR" "$GOCACHE" "$GOMODCACHE" "$output"
export GOWORK=off GOFLAGS=-mod=readonly GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0
unset GOOS GOARCH
[[ "$(go env GOVERSION)" == go1.27.3 ]]
go_root="$(go env GOROOT)"
format_output="$("$go_root/bin/gofmt" -l .)"
if [[ -n "$format_output" ]]; then printf 'Formatting required:\n%s\n' "$format_output" >&2; exit 1; fi
go test -count=1 ./...
go vet ./...
go mod verify
go build ./...
if $race; then CGO_ENABLED=1 go test -race -count=1 ./...; fi
go test -c -trimpath -buildvcs=false -o "$output/go-oracle-session.test" ./session
go version -m "$output/go-oracle-session.test" > "$output/build-metadata.txt"
go env -json GOVERSION GOOS GOARCH GOFLAGS CGO_ENABLED GOWORK > "$output/build-settings.json"
go list -m -json all > "$output/modules.json"
if $scan; then
  go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
  go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 -mode=binary "$output/go-oracle-session.test"
fi
printf 'Validation passed. Evidence: %s\n' "$output"
