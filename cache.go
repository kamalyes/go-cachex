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

// Handler 是缓存操作的基础接口
// 提供了最基本的缓存操作方法，包括增删改查和统计功能
// 这是一个同步接口，不支持context控制
type Handler interface {
	// Set 设置缓存键值对
	// key: 缓存键，不能为空
	// value: 缓存值，可以为任意字节数组
	// 返回值: 设置成功返回nil，失败返回相应错误
	Set([]byte, []byte) error
	
	// SetWithTTL 设置带有过期时间的缓存键值对
	// key: 缓存键，不能为空
	// value: 缓存值，可以为任意字节数组
	// ttl: 过期时间，必须为正数
	// 返回值: 设置成功返回nil，失败返回相应错误
	SetWithTTL([]byte, []byte, time.Duration) error
	
	// Get 根据键获取缓存值
	// key: 要查询的缓存键
	// 返回值: 找到返回值和nil错误，未找到返回nil和ErrNotFound错误
	Get([]byte) ([]byte, error)
	
	// GetTTL 获取指定键的剩余过期时间
	// key: 要查询的缓存键
	// 返回值: 剩余过期时间和错误信息，如果键不存在返回0和ErrNotFound
	GetTTL([]byte) (time.Duration, error)
	
	// Del 删除指定的缓存键
	// key: 要删除的缓存键
	// 返回值: 删除成功返回nil，失败返回相应错误
	Del([]byte) error
	
	// BatchGet 批量获取多个键的值
	// keys: 要查询的缓存键数组
	// 返回值: 对应的值数组和错误数组，索引一一对应
	BatchGet([][]byte) ([][]byte, []error)
	
	// Stats 获取缓存统计信息
	// 返回值: 包含缓存统计数据的map，如命中率、大小、元素数量等
	Stats() map[string]interface{}
	
	// Close 关闭缓存实例并释放资源
	// 返回值: 关闭成功返回nil，失败返回相应错误
	Close() error
}

// ContextHandler 是支持 context 的缓存操作接口
// 继承了Handler的所有功能，并在每个操作中添加了context支持
// context可以用于超时控制、取消操作和传递请求相关的元数据
type ContextHandler interface {
	// Set 设置缓存键值对（支持context）
	// ctx: 上下文，用于控制超时和取消操作
	// key: 缓存键，不能为空
	// value: 缓存值，可以为任意字节数组
	// 返回值: 设置成功返回nil，失败返回相应错误
	Set(ctx context.Context, key, value []byte) error
	
	// SetWithTTL 设置带有过期时间的缓存键值对（支持context）
	// ctx: 上下文，用于控制超时和取消操作
	// key: 缓存键，不能为空
	// value: 缓存值，可以为任意字节数组
	// ttl: 过期时间，必须为正数
	// 返回值: 设置成功返回nil，失败返回相应错误
	SetWithTTL(ctx context.Context, key, value []byte, ttl time.Duration) error
	
	// Get 根据键获取缓存值（支持context）
	// ctx: 上下文，用于控制超时和取消操作
	// key: 要查询的缓存键
	// 返回值: 找到返回值和nil错误，未找到返回nil和ErrNotFound错误
	Get(ctx context.Context, key []byte) ([]byte, error)
	
	// GetTTL 获取指定键的剩余过期时间（支持context）
	// ctx: 上下文，用于控制超时和取消操作
	// key: 要查询的缓存键
	// 返回值: 剩余过期时间和错误信息，如果键不存在返回0和ErrNotFound
	GetTTL(ctx context.Context, key []byte) (time.Duration, error)
	
	// Del 删除指定的缓存键（支持context）
	// ctx: 上下文，用于控制超时和取消操作
	// key: 要删除的缓存键
	// 返回值: 删除成功返回nil，失败返回相应错误
	Del(ctx context.Context, key []byte) error
	
	// GetOrCompute 获取缓存值，如果不存在则通过loader函数计算并存储
	// 这是一个常用的缓存模式，可以避免缓存穿透问题
	// ctx: 上下文，用于控制超时和取消操作
	// key: 缓存键
	// ttl: 计算出的值的过期时间
	// loader: 当缓存不存在时用于计算值的函数
	// 返回值: 缓存值和错误信息
	GetOrCompute(ctx context.Context, key []byte, ttl time.Duration, loader func(context.Context) ([]byte, error)) ([]byte, error)
	
	// BatchGet 批量获取多个键的值（支持context）
	// ctx: 上下文，用于控制超时和取消操作
	// keys: 要查询的缓存键数组
	// 返回值: 对应的值数组和错误数组，索引一一对应
	BatchGet(ctx context.Context, keys [][]byte) ([][]byte, []error)
	
	// Stats 获取缓存统计信息（支持context）
	// ctx: 上下文，用于控制超时和取消操作
	// 返回值: 包含缓存统计数据的map，如命中率、大小、元素数量等
	Stats(ctx context.Context) map[string]interface{}
	
	// Close 关闭缓存实例并释放资源
	// 注意：Close操作通常不需要context，因为它是清理操作
	// 返回值: 关闭成功返回nil，失败返回相应错误
	Close() error
}
