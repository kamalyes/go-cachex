/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 23:13:01
 * @FilePath: \go-cachex\cache.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound 表示在缓存中找不到请求的键
	// 当尝试获取一个不存在的缓存项时返回此错误
	ErrNotFound = errors.New("key not found in cache")

	// ErrDataWrite 表示写入数据时发生错误
	// 当向缓存存储数据失败时返回此错误
	ErrDataWrite = errors.New("failed to write data to cache")

	// ErrDataRead 表示读取数据时发生错误
	// 当从缓存读取数据失败时返回此错误
	ErrDataRead = errors.New("failed to read data from cache")

	// ErrClosed 表示缓存已关闭
	// 当尝试在已关闭的缓存实例上执行操作时返回此错误
	ErrClosed = errors.New("cache is closed")

	// ErrInvalidKey 表示使用了无效的键
	// 当传入的缓存键为空或格式不正确时返回此错误
	ErrInvalidKey = errors.New("invalid cache key")

	// ErrInvalidValue 表示使用了无效的值
	// 当传入的缓存值为nil或格式不正确时返回此错误
	ErrInvalidValue = errors.New("invalid cache value")

	// ErrInvalidTTL 表示使用了无效的 TTL 值
	// 当传入的过期时间为负数或其他无效值时返回此错误
	ErrInvalidTTL = errors.New("invalid TTL value")

	// ErrCapacityExceeded 表示超出了缓存容量限制
	// 当缓存已达到最大容量限制无法存储更多数据时返回此错误
	ErrCapacityExceeded = errors.New("cache capacity exceeded")

	// ErrNotInitialized 表示缓存未正确初始化
	// 当尝试使用未初始化的缓存实例时返回此错误
	ErrNotInitialized = errors.New("cache not properly initialized")

	// ErrUnavailable 表示缓存服务暂时不可用（比如 Redis 连接断开）
	// 当底层存储服务（如Redis、Memcached等）连接失败或不可达时返回此错误
	ErrUnavailable = errors.New("cache service unavailable")

	// ErrTimeout 表示操作超时
	// 当缓存操作执行时间超过设定的超时时间时返回此错误
	ErrTimeout = errors.New("cache operation timed out")
)

// Handler 是缓存操作的统一接口
// 同时提供简化版（不带context）和完整版（带context）两种调用方式
// 简化版方法：Set, Get, Del 等 - 适用于简单场景
// 完整版方法：SetWithCtx, GetWithCtx, DelWithCtx 等 - 支持超时控制和取消操作
type Handler interface {
	// ========== 简化版方法（不带context） ==========

	// Set 设置缓存键值对
	Set(key, value []byte) error

	// SetWithTTL 设置带有过期时间的缓存键值对
	SetWithTTL(key, value []byte, ttl time.Duration) error

	// Get 根据键获取缓存值
	Get(key []byte) ([]byte, error)

	// GetTTL 获取指定键的剩余过期时间
	GetTTL(key []byte) (time.Duration, error)

	// Del 删除指定的缓存键
	Del(key []byte) error

	// BatchGet 批量获取多个键的值
	BatchGet(keys [][]byte) ([][]byte, []error)

	// GetOrCompute 获取缓存值，如果不存在则通过loader函数计算并存储
	GetOrCompute(key []byte, ttl time.Duration, loader func() ([]byte, error)) ([]byte, error)

	// ========== 完整版方法（带context） ==========

	// SetWithCtx 设置缓存键值对
	SetWithCtx(ctx context.Context, key, value []byte) error

	// SetWithTTLAndCtx 设置带有过期时间的缓存键值对
	SetWithTTLAndCtx(ctx context.Context, key, value []byte, ttl time.Duration) error

	// GetWithCtx 根据键获取缓存值
	GetWithCtx(ctx context.Context, key []byte) ([]byte, error)

	// GetTTLWithCtx 获取指定键的剩余过期时间
	GetTTLWithCtx(ctx context.Context, key []byte) (time.Duration, error)

	// DelWithCtx 删除指定的缓存键
	DelWithCtx(ctx context.Context, key []byte) error

	// BatchGetWithCtx 批量获取多个键的值
	BatchGetWithCtx(ctx context.Context, keys [][]byte) ([][]byte, []error)

	// GetOrComputeWithCtx 获取缓存值，如果不存在则通过loader函数计算并存储
	GetOrComputeWithCtx(ctx context.Context, key []byte, ttl time.Duration, loader func(context.Context) ([]byte, error)) ([]byte, error)

	// ========== 通用方法 ==========

	// Stats 获取缓存统计信息
	Stats() map[string]interface{}

	// Close 关闭缓存实例并释放资源
	Close() error
}
