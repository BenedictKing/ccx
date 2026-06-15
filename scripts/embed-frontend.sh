#!/usr/bin/env bash
# 前端构建缓存脚本：基于源码 hash 判断是否需要重新构建
# 支持跨窗口共享缓存

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
FRONTEND_DIR="$ROOT_DIR/frontend"
EMBED_DIR="$ROOT_DIR/backend-go/frontend/dist"
HASH_FILE="$EMBED_DIR/.source-hash"

GREEN='\033[0;32m'
NC='\033[0m'

# 计算前端源码 hash
compute_hash() {
  find "$FRONTEND_DIR/src" "$FRONTEND_DIR/public" \
    "$FRONTEND_DIR/index.html" \
    "$FRONTEND_DIR/vite.config.ts" \
    "$FRONTEND_DIR/tsconfig.json" \
    "$FRONTEND_DIR/tsconfig.app.json" \
    "$FRONTEND_DIR/bun.lock" \
    -type f 2>/dev/null \
    | sort \
    | xargs sha256sum 2>/dev/null \
    | sha256sum \
    | cut -d' ' -f1
}

current_hash=$(compute_hash)

# 检查缓存是否有效
if [ -f "$HASH_FILE" ] && [ -d "$EMBED_DIR/assets" ]; then
  cached_hash=$(cat "$HASH_FILE" 2>/dev/null || echo "")
  if [ "$current_hash" = "$cached_hash" ]; then
    echo -e "${GREEN}✅ 前端未变更，跳过构建${NC}"
    exit 0
  fi
fi

# 需要重新构建
echo -e "${GREEN}📦 构建前端...${NC}"
cd "$FRONTEND_DIR" && bun run build

echo -e "${GREEN}📋 嵌入前端到 Go 后端...${NC}"
rm -rf "$EMBED_DIR"
mkdir -p "$EMBED_DIR"
cp -r "$FRONTEND_DIR/dist/"* "$EMBED_DIR/"

# 写入新 hash
echo "$current_hash" > "$HASH_FILE"
echo -e "${GREEN}✅ 前端构建完成（hash: ${current_hash:0:12}...）${NC}"
