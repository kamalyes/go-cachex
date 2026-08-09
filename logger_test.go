/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-08-09 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-08-09 00:00:00
 * @FilePath: \go-cachex\logger_test.go
 * @Description: 默认 Cachex 日志器测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"testing"

	"github.com/kamalyes/go-logger"
	"github.com/stretchr/testify/assert"
)

func TestLogger_NewDefaultCachexLogger(t *testing.T) {
	l := NewDefaultCachexLogger()
	assert.NotNil(t, l)

	// 验证返回值实现了 logger.ILogger 接口
	var _ logger.ILogger = l

	// 验证各日志方法可正常调用（不影响测试结果）
	l.InfoMsg("测试日志消息")
	l.DebugMsg("调试消息")
	l.WarnMsg("警告消息")
}
