#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
TARGET_DIR="${TARGET_DIR:-$PROJECT_ROOT/data/awesome-english-ebooks}"
BINARY="${MAGAZINES2DB_BIN:-$PROJECT_ROOT/magazines2db}"

if [ ! -x "$BINARY" ]; then
  echo "magazines2db binary is missing or not executable: $BINARY" >&2
  echo "build it first: go build -o magazines2db ." >&2
  exit 1
fi

LOCK_DIR="$PROJECT_ROOT/.daily.lock"
if ! mkdir "$LOCK_DIR" 2>/dev/null; then
  echo "[$(date '+%F %T')] another daily job is running, exit."
  exit 0
fi
trap 'rm -rf "$LOCK_DIR"' EXIT

cd "$PROJECT_ROOT"
echo "[$(date '+%F %T')] daily job start"

"$SCRIPT_DIR/sync_magazines.sh"

shopt -s nullglob
issue_dirs=(
  "$TARGET_DIR/01_economist"/te_[0-9][0-9][0-9][0-9].[0-9][0-9].[0-9][0-9]
  "$TARGET_DIR/05_wired"/[0-9][0-9][0-9][0-9].[0-9][0-9].[0-9][0-9]
)

if [ "${#issue_dirs[@]}" -eq 0 ]; then
  echo "no magazine issue directories found under $TARGET_DIR" >&2
  exit 1
fi

failed=0
for issue_dir in "${issue_dirs[@]}"; do
  if ! "$BINARY" ingest "$issue_dir"; then
    failed=1
  fi
done

if ! "$BINARY" summarize; then
  failed=1
fi

if [ "$failed" -ne 0 ]; then
  echo "[$(date '+%F %T')] daily job completed with errors" >&2
  exit 1
fi

echo "[$(date '+%F %T')] daily job done"
