#!/usr/bin/env bash
# Run web unit tests. Pass --coverage to write reports under ./coverage/
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

COVERAGE=0
ARGS=()
for arg in "$@"; do
  case "$arg" in
    --coverage|-cover) COVERAGE=1 ;;
    *) ARGS+=("$arg") ;;
  esac
done

PKGS=$(go list ./... | grep -v '/cmd/')

if [[ "$COVERAGE" -eq 1 ]]; then
  mkdir -p coverage
  echo "→ go test with coverage → coverage/"
  go test -count=1 -race -covermode=atomic -coverprofile=coverage/coverage.out ${ARGS[@]+"${ARGS[@]}"} $PKGS
  GEN="$ROOT/scripts/gen-coverage-html.py"
  if [[ ! -f "$GEN" ]]; then
    GEN="$ROOT/../scripts/gen-coverage-html.py"
  fi
  python3 "$GEN" \
    --profile coverage/coverage.out \
    --out coverage \
    --title "alfred-identity-backend — web" \
    --module github.com/alfred-identity/web
else
  echo "→ go test"
  go test -count=1 -race ${ARGS[@]+"${ARGS[@]}"} $PKGS
fi
