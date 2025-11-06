/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 21:29:40
 * @FilePath: \go-cachex\cache.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"errors"
	"time"
)

var (
	// ErrNotFound 表示在缓存中找不到请求的键
	ErrNotFound = errors.New("key not found in cache")
	// ErrDataWrite 表示写入数据时发生错误
	ErrDataWrite = errors.New("failed to write data to cache")
	// ErrDataRead 表示读取数据时发生错误
	ErrDataRead = errors.New("failed to read data from cache")
	// ErrClosed 表示缓存已关闭
	ErrClosed = errors.New("cache is closed")
	// ErrInvalidKey 表示使用了无效的键
	ErrInvalidKey = errors.New("invalid cache key")
	// ErrInvalidValue 表示使用了无效的值
	ErrInvalidValue = errors.New("invalid cache value")
	// ErrInvalidTTL 表示使用了无效的 TTL 值
	ErrInvalidTTL = errors.New("invalid TTL value")
	// ErrCapacityExceeded 表示超出了缓存容量限制
	ErrCapacityExceeded = errors.New("cache capacity exceeded")
	// ErrNotInitialized 表示缓存未正确初始化
	ErrNotInitialized = errors.New("cache not properly initialized")
	// ErrUnavailable 表示缓存服务暂时不可用（比如 Redis 连接断开）
	ErrUnavailable = errors.New("cache service unavailable")
	// ErrTimeout 表示操作超时
	ErrTimeout = errors.New("cache operation timed out")
)

// Handler 是缓存操作的接口
type Handler interface {
	Set([]byte, []byte) error
	SetWithTTL([]byte, []byte, time.Duration) error
	Get([]byte) ([]byte, error)
	GetTTL([]byte) (time.Duration, error)
	Del([]byte) error
	Close() error
}
