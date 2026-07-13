#!/bin/sh
# noblack 容器入口脚本。
#   1. 首次启动时, 若持久化目录里没有词库, 用镜像内置的默认词库初始化 (幂等)。
#   2. 把 NB_* 环境变量翻译成命令行参数。
#   3. exec 启动二进制, 使其成为 PID 1, 正确接收 SIGINT/SIGTERM 实现优雅关闭。
set -e

# 1. 初始化数据卷里的词库 (仅当不存在时, 避免覆盖用户数据)。
if [ ! -f "$NB_WORDS" ]; then
  echo "[entrypoint] $NB_WORDS 不存在, 用内置默认词库初始化"
  mkdir -p "$(dirname "$NB_WORDS")"
  cp /app/words.default.json "$NB_WORDS"
fi

# 2. 组装参数。
set -- -addr "$NB_ADDR" -words "$NB_WORDS" -watch="$NB_WATCH"

# 统计持久化 (NB_STATS 非空时开启)。
if [ -n "$NB_STATS" ]; then
  set -- "$@" -stats-file "$NB_STATS"
fi
# 写操作鉴权 (NB_TOKEN 非空时开启)。
if [ -n "$NB_TOKEN" ]; then
  set -- "$@" -token "$NB_TOKEN"
fi
# 大小写不敏感。
if [ "$NB_CI" = "true" ]; then
  set -- "$@" -ci
fi

echo "[entrypoint] 启动: /app/noblack $*"
# 3. exec 让二进制接管 PID 1。
exec /app/noblack "$@"
