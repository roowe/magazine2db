#!/usr/bin/env bash
set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/hehonghui/awesome-english-ebooks.git}"
BRANCH="${BRANCH:-master}"
KEEP="${KEEP:-4}"

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
TARGET_DIR="${TARGET_DIR:-$PROJECT_ROOT/data/awesome-english-ebooks}"

CATEGORIES=(
  "01_economist"
  "05_wired"
)

if ! [[ "$KEEP" =~ ^[1-9][0-9]*$ ]]; then
  echo "KEEP must be a positive integer, got: $KEEP" >&2
  exit 1
fi

mkdir -p "$(dirname "$TARGET_DIR")"

LOCK_DIR="${TARGET_DIR}.lock"
if ! mkdir "$LOCK_DIR" 2>/dev/null; then
  echo "[$(date '+%F %T')] another sync is running, exit."
  exit 0
fi

TMP_FILE="$(mktemp)"
trap 'rm -rf "$LOCK_DIR" "$TMP_FILE"' EXIT

echo "[$(date '+%F %T')] sync start"

if [ -e "$TARGET_DIR" ] && [ ! -d "$TARGET_DIR/.git" ]; then
  echo "target exists but is not a git repository: $TARGET_DIR" >&2
  exit 1
fi

if [ ! -d "$TARGET_DIR/.git" ]; then
  echo "clone repo to $TARGET_DIR"
  git clone \
    --depth=1 \
    --filter=blob:none \
    --sparse \
    --branch "$BRANCH" \
    "$REPO_URL" \
    "$TARGET_DIR"
fi

cd "$TARGET_DIR"

git remote set-url origin "$REPO_URL"
git fetch --depth=1 --prune origin "$BRANCH"

latest_paths_for_category() {
  local category="$1"
  local pattern

  case "$category" in
    "01_economist")
      pattern='^te_[0-9]{4}\.[0-9]{2}\.[0-9]{2}$'
      ;;
    "05_wired")
      pattern='^[0-9]{4}\.[0-9]{2}\.[0-9]{2}$'
      ;;
    *)
      echo "unknown category: $category" >&2
      exit 1
      ;;
  esac

  local names
  names="$(
    git ls-tree -d --name-only "origin/$BRANCH:$category" \
      | grep -E "$pattern" \
      | sort \
      | tail -n "$KEEP" || true
  )"

  if [ -z "$names" ]; then
    echo "no issue dirs found for $category" >&2
    exit 1
  fi

  printf '%s\n' "$names" | sed "s#^#$category/#"
}

{
  for category in "${CATEGORIES[@]}"; do
    latest_paths_for_category "$category"
  done

  if git cat-file -e "origin/$BRANCH:01_economist/fonts" 2>/dev/null; then
    echo "01_economist/fonts"
  fi
} | sort -u > "$TMP_FILE"

echo "keep these paths:"
cat "$TMP_FILE"

git sparse-checkout init --cone >/dev/null 2>&1 || true
git sparse-checkout set --stdin < "$TMP_FILE"
git reset --hard "origin/$BRANCH"
git sparse-checkout reapply

echo
echo "current local dirs:"
for category in "${CATEGORIES[@]}"; do
  echo "== $category =="
  find "$category" -maxdepth 1 -mindepth 1 -type d | sort || true
done

echo
du -h -d 1 . 2>/dev/null || du -h --max-depth=1 .
echo "[$(date '+%F %T')] sync done"
