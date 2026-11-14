#!/usr/bin/env bash
set -euo pipefail
race=false; scan=false; native=
while (($#)); do
  case "$1" in
    --race) race=true; shift ;;
    --scan) scan=true; shift ;;
    --native-harness)
      if (($# < 2)) || [[ "$2" != 2 && "$2" != 3 ]]; then printf 'Expected --native-harness 2 or 3\n' >&2; exit 2; fi
      native="$2"; shift 2 ;;
    *) printf 'Unknown option: %s\n' "$1" >&2; exit 2 ;;
  esac
done
cd "$(dirname "$0")/.."
test_package=./session; binary_name=go-oracle-session.test
if [[ -n "$native" ]]; then
  cd integration/oracle
  tags=integration; if [[ "$native" == 2 ]]; then tags=integration,goora2; fi
  test_package=.; binary_name="oracle-native-goora$native.test"
  # Offline validation must not inherit a live qualification profile.
  unset ORACLE_TEST_CONFIG_STDIN
fi
# Conditional arguments also support the system Bash on older macOS hosts.
go_build() {
  local go_subcommand="$1"; shift
  if [[ -n "$native" ]]; then go "$go_subcommand" -tags "$tags" "$@"; else go "$go_subcommand" "$@"; fi
}
base="${DEVELOPMENT_ROOT:-${RUNNER_TEMP:+$RUNNER_TEMP/development}}"
if [[ -z "$base" ]]; then base=/mnt/v/Development; fi
if [[ ! -d "$base" && -z "${RUNNER_TEMP:-}" ]]; then printf 'Approved development storage is unavailable.\n' >&2; exit 1; fi
project="$base/go-oracle-session"; run="$(date -u +%Y%m%d-%H%M%S)-$$"
export TMPDIR="$project/tmp/validate-$run" TEMP="$project/tmp/validate-$run" TMP="$project/tmp/validate-$run"
export GOTMPDIR="$TMPDIR/go-build" GOCACHE="$project/caches/go-build" GOMODCACHE="$project/caches/go-modules"
output="$project/artifacts/runs/validate-$run"
mkdir -p "$TMPDIR" "$GOTMPDIR" "$GOCACHE" "$GOMODCACHE" "$output"
export GOWORK=off GOFLAGS=-mod=readonly GOTOOLCHAIN=go1.27.4 CGO_ENABLED=0
unset GOOS GOARCH
[[ "$(go env GOVERSION)" == go1.27.4 ]]
go_root="$(go env GOROOT)"
format_output="$("$go_root/bin/gofmt" -l .)"
if [[ -n "$format_output" ]]; then printf 'Formatting required:\n%s\n' "$format_output" >&2; exit 1; fi
go_build test -count=1 ./...
go_build vet ./...
go mod verify
go_build build ./...
if $race; then CGO_ENABLED=1 go_build test -race -count=1 ./...; fi
go_build test -c -trimpath -buildvcs=false -o "$output/$binary_name" "$test_package"
go version -m "$output/$binary_name" > "$output/build-metadata.txt"
go env -json GOVERSION GOOS GOARCH GOFLAGS CGO_ENABLED GOWORK > "$output/build-settings.json"
go list -m -json all > "$output/modules.json"
printf 'nativeHarness=%s\nbuildTags=%s\n' "${native:-false}" "${tags:-none}" > "$output/validation-options.txt"
if $scan; then
  if [[ -n "$native" ]]; then
    go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -test "-tags=$tags" ./...
  else
    go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
  fi
  go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -mode=binary "$output/$binary_name"
fi
if [[ -n "$native" ]]; then printf 'Native harness validation is offline; no database qualification was performed.\n'; fi
printf 'Validation passed. Evidence: %s\n' "$output"
