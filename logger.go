/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-13 13:15:19
 * @FilePath: \go-cachex\logger.go
 * @Description: go-cachex 日志接口，直接复用 go-logger
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"time"

	"github.com/kamalyes/go-logger"
)

// NewCachexLogger 创建新的Cachex日志器，基于 go-logger
func NewCachexLogger(config *logger.LogConfig) logger.ILogger {
	return logger.NewLogger(config)
}

// NewDefaultCachexLogger 创建默认配置的Cachex日志器
func NewDefaultCachexLogger() logger.ILogger {
	config := logger.DefaultConfig().
		WithLevel(logger.INFO).
		WithPrefix("[CACHEX] ").
		WithShowCaller(false).
		WithColorful(true).
		WithTimeFormat(time.DateTime)

	return logger.NewLogger(config)
}
