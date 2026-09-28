#!/usr/bin/env bash
set -Eeuo pipefail

BASE_REF="${1:-origin/${GITHUB_BASE_REF:-main}}"
HEAD_REF="${2:-HEAD}"

mapfile -t changed_files < <(
  git diff --name-only --diff-filter=ACDMR "$BASE_REF...$HEAD_REF"
)

if ((${#changed_files[@]} == 0)); then
  echo "提交中没有文件变更。"
  exit 1
fi

declare -A candidate_dirs=()

for file in "${changed_files[@]}"; do
  # 不允许修改仓库根目录文件
  if [[ "$file" != */* ]]; then
    echo "不允许修改仓库根目录文件：$file"
    exit 1
  fi

  candidate_dir="${file%%/*}"

  # 不允许修改 .github、.idea 等管理目录
  if [[ "$candidate_dir" == .* ]]; then
    echo "不允许修改仓库管理目录：$file"
    exit 1
  fi

  candidate_dirs["$candidate_dir"]=1
done

# 一个 PR 只能修改一个新生目录
if ((${#candidate_dirs[@]} != 1)); then
  echo "一个 PR 只能提交一个新生目录。"
  echo "本次修改涉及：${!candidate_dirs[*]}"
  exit 1
fi

candidate_dir="${!candidate_dirs[@]}"

# 防止只删除整个目录
if [[ ! -d "$candidate_dir" ]]; then
  echo "提交目录不存在或已被删除：$candidate_dir"
  exit 1
fi

echo "目录结构检查通过：$candidate_dir"