#!/usr/bin/env bash
set -Eeuo pipefail

BASE_REF="${1:-origin/${GITHUB_BASE_REF:-main}}"
HEAD_REF="${2:-HEAD}"

if ! git rev-parse --verify "$BASE_REF^{commit}" >/dev/null 2>&1; then
  echo "Cannot resolve base revision: $BASE_REF" >&2
  exit 1
fi

mapfile -t changed_files < <(git diff --name-only --diff-filter=ACDMR "$BASE_REF...$HEAD_REF")

if ((${#changed_files[@]} == 0)); then
  echo "No added, copied, modified, or renamed files were found in the PR." >&2
  exit 1
fi

declare -A roots=()
for file in "${changed_files[@]}"; do
  if [[ "$file" != */* ]]; then
    echo "Root-level changes are not allowed: $file" >&2
    exit 1
  fi

  root="${file%%/*}"
  if [[ "$root" == .* || "$root" == ".github" ]]; then
    echo "Changes outside the candidate directory are not allowed: $file" >&2
    exit 1
  fi
  roots["$root"]=1
done

if ((${#roots[@]} != 1)); then
  echo "A PR must modify exactly one candidate directory; found: ${!roots[*]}" >&2
  exit 1
fi

candidate="${!roots[@]}"
if [[ ! -d "$candidate" ]]; then
  echo "Candidate directory does not exist: $candidate" >&2
  exit 1
fi

echo "Validating candidate directory: $candidate"

mapfile -t go_mods < <(find "$candidate" -type f -name go.mod \
  -not -path '*/vendor/*' -not -path '*/node_modules/*' | sort)
if ((${#go_mods[@]} == 0)); then
  echo "No go.mod found under $candidate" >&2
  exit 1
fi

for mod in "${go_mods[@]}"; do
  module_dir="${mod%/go.mod}"
  echo "Checking Go module: $module_dir"
  (
    cd "$module_dir"
    go test ./...
    go build ./...
  )
done

mapfile -t package_jsons < <(find "$candidate" -type f -name package.json \
  -not -path '*/node_modules/*' -not -path '*/vendor/*' | sort)
for package_json in "${package_jsons[@]}"; do
  frontend_dir="${package_json%/package.json}"
  echo "Checking frontend project: $frontend_dir"
  (
    cd "$frontend_dir"
    if [[ -f package-lock.json || -f npm-shrinkwrap.json ]]; then
      npm ci
    else
      npm install
    fi

    if node -e 'const p=require("./package.json"); process.exit(p.scripts && p.scripts.build ? 0 : 1)'; then
      npm run build
    else
      echo "No build script in $frontend_dir; dependency installation passed."
    fi
  )
done

echo "Submission validation passed for $candidate"
