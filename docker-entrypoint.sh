#!/bin/sh
# noblack 容器入口脚本。
#   1. 首次启动时，若持久化目录里没有词库，使用镜像内置默认词库初始化。
#   2. 检查词库与统计目录是否可写。
#   3. 把 NB_* 环境变量翻译成命令行参数，并以 root 运行服务。
set -e

words_dir="$(dirname "$NB_WORDS")"
stats_dir=""
if [ -n "$NB_STATS" ]; then
  stats_dir="$(dirname "$NB_STATS")"
fi

mkdir -p "$words_dir"
if [ -n "$stats_dir" ]; then
  mkdir -p "$stats_dir"
fi

# 初始化数据卷里的词库，仅在文件不存在时复制，避免覆盖用户数据。
if [ ! -f "$NB_WORDS" ]; then
  echo "[entrypoint] $NB_WORDS 不存在，使用内置默认词库初始化"
  cp /app/words.default.json "$NB_WORDS"
fi

# 应用通过“同目录临时文件 + 重命名”原子落盘，因此目录本身必须可写。
check_write_dir() {
  dir="$1"
  test_file="$dir/.noblack-write-test-$$"
  if ! (: > "$test_file" && rm -f "$test_file"); then
    echo "[entrypoint] 目录不可写: $dir，请检查绑定挂载目录权限" >&2
    exit 1
  fi
}
check_write_dir "$words_dir"
if [ -n "$stats_dir" ] && [ "$stats_dir" != "$words_dir" ]; then
  check_write_dir "$stats_dir"
fi

set -- -addr "$NB_ADDR" -words "$NB_WORDS" -watch="$NB_WATCH"
if [ -n "$NB_STATS" ]; then
  set -- "$@" -stats-file "$NB_STATS"
fi
if [ -n "$NB_TOKEN" ]; then
  set -- "$@" -token "$NB_TOKEN"
fi
if [ "$NB_CI" = "true" ]; then
  set -- "$@" -ci
fi

echo "[entrypoint] 以 root 启动 noblack，监听地址: $NB_ADDR"
exec /app/noblack "$@"
