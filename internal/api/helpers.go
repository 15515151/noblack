package api

import (
	"strconv"
	"strings"
)

// trimPrefixPath 去掉 URL 路径前缀, 返回剩余部分 (已去空白)。
func trimPrefixPath(path, prefix string) string {
	return strings.TrimSpace(strings.TrimPrefix(path, prefix))
}

// parsePositiveInt 解析正整数, 失败或非正数返回错误。
func parsePositiveInt(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, strconv.ErrRange
	}
	return n, nil
}
