#!/usr/bin/env bash
# 编译 music-dl 并安装到全局目录。
#
# 用法：
#   ./install.sh                     # 安装到 ~/.local/bin/music-dl
#   INSTALL_DIR=/usr/local/bin ./install.sh
#   BACKUP=1 ./install.sh            # 覆盖前把旧版本备份为 music-dl.bak
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
BIN_NAME="music-dl"
TARGET="$INSTALL_DIR/$BIN_NAME"

cd "$(dirname "$0")"

if ! command -v go >/dev/null 2>&1; then
    echo "未找到 go，请先安装 Go：https://go.dev/dl/" >&2
    exit 1
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "编译 $BIN_NAME ($(git rev-parse --short HEAD 2>/dev/null || echo unknown))..."
go build -trimpath -ldflags="-s -w" -o "$TMP_DIR/$BIN_NAME" ./cmd/music-dl

mkdir -p "$INSTALL_DIR"
if [[ "${BACKUP:-0}" == "1" && -f "$TARGET" ]]; then
    cp -p "$TARGET" "$TARGET.bak"
    echo "已备份旧版本到 $TARGET.bak"
fi
# 先在目标目录写临时文件再 rename 替换：替换是原子的，正在运行的旧进程不受影响，
# 也避免 macOS 上原地覆盖已签名二进制导致新进程被系统杀掉。
install -m 755 "$TMP_DIR/$BIN_NAME" "$INSTALL_DIR/.$BIN_NAME.tmp"
mv -f "$INSTALL_DIR/.$BIN_NAME.tmp" "$TARGET"

echo "已安装到 $TARGET"
case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *) echo "提示：$INSTALL_DIR 不在 PATH 中，请把它加入 PATH。" ;;
esac
"$TARGET" --version
