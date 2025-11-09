/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 19:15:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 19:15:00
 * @FilePath: \go-cachex\client.go
 * @Description: 缓存客户端统一入口
 *
 * 提供统一的缓存客户端接口，支持多种底层缓存实现（LRU、Ristretto、Redis、Expiring）
 * 主要功能包括：
 *  - 统一的配置结构 `ClientConfig`，支持不同缓存类型的配置参数
 *  - 支持 context 的完整 API：Get、Set、SetWithTTL、Del、GetOrCompute
 *  - 便利构造函数：NewLRUClient、NewRistrettoClient、NewRedisClient、NewExpiringClient
 *  - 自动错误处理和参数验证
 *  - 优雅的资源管理（Close）
 *
 * 使用注意：
 *  - 所有操作都支持 context 传入，可以实现超时控制和取消操作
 *  - Client 内部封装了 CtxCache，提供并发去重和 singleflight 支持
 *  - 不同缓存类型有不同的配置要求，使用前请确保配置正确
 *  - 使用完毕后应调用 Close() 方法释放资源
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"time"
)

// 确保 Client 实现了 ContextHandler 接口
var _ ContextHandler = (*Client)(nil)

// CacheType 定义支持的缓存类型
type CacheType string

const (
	CacheLRU           CacheType = "lru"
	CacheLRUOptimized  CacheType = "lru_optimized"
	CacheRistretto     CacheType = "ristretto"
	CacheRedis         CacheType = "redis"
	CacheExpiring      CacheType = "expiring"
)

// ClientConfig 用于统一配置入口
type ClientConfig struct {
	Type             CacheType
	Capacity         int                 // for lru/expiring/ristretto
	CleanupInterval  time.Duration       // for expiring cache
	RedisConfig      *RedisConfig        // for redis
	RistrettoConfig  *RistrettoConfig    // for ristretto
}

// Client 是支持 context 的缓存客户端
type Client struct {
	ctxCache *CtxCache
	config   *ClientConfig
}

// NewClient 根据配置创建带 context 能力的缓存客户端
func NewClient(ctx context.Context, cfg *ClientConfig) (*Client, error) {
	if cfg == nil {
		return nil, ErrInvalidValue
	}

	var h Handler
	var err error

	switch cfg.Type {
	case CacheLRU:
		if cfg.Capacity <= 0 {
			cfg.Capacity = 128
		}
		h = NewLRUHandler(cfg.Capacity)
	case CacheLRUOptimized:
		if cfg.Capacity <= 0 {
			cfg.Capacity = 128
		}
		h = NewLRUOptimizedHandler(cfg.Capacity)
	case CacheExpiring:
		interval := cfg.CleanupInterval
		if interval <= 0 {
			interval = time.Minute
		}
		h = NewExpiringHandler(interval)
	case CacheRistretto:
		h, err = NewRistrettoHandler(cfg.RistrettoConfig)
		if err != nil {
			return nil, err
		}
	case CacheRedis:
		if cfg.RedisConfig == nil {
			return nil, ErrInvalidValue
		}
		h, err = NewRedisHandler(cfg.RedisConfig)
		if err != nil {
			return nil, err
		}
	default:
		h = NewLRUHandler(128)
	}

	client := &Client{
		ctxCache: NewCtxCache(h),
		config:   cfg,
	}

	return client, nil
}

// Get 从缓存中获取值
func (c *Client) Get(ctx context.Context, key []byte) ([]byte, error) {
	return c.ctxCache.Get(ctx, key)
}

// Set 设置缓存值
func (c *Client) Set(ctx context.Context, key, value []byte) error {
	return c.ctxCache.Set(ctx, key, value)
}

// SetWithTTL 设置带TTL的缓存值
func (c *Client) SetWithTTL(ctx context.Context, key, value []byte, ttl time.Duration) error {
	return c.ctxCache.SetWithTTL(ctx, key, value, ttl)
}

// Del 删除缓存键
func (c *Client) Del(ctx context.Context, key []byte) error {
	return c.ctxCache.Del(ctx, key)
}

// GetTTL 获取键的剩余TTL
func (c *Client) GetTTL(ctx context.Context, key []byte) (time.Duration, error) {
	if err := ValidateBasicOp(key, c.ctxCache != nil && c.ctxCache.handler != nil, false); err != nil {
		return 0, err
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
		return c.ctxCache.handler.GetTTL(key)
	}
}

// GetOrCompute 获取或计算缓存值（带去重）
func (c *Client) GetOrCompute(ctx context.Context, key []byte, ttl time.Duration, loader func(context.Context) ([]byte, error)) ([]byte, error) {
	return c.ctxCache.GetOrCompute(ctx, key, ttl, loader)
}

// Close 关闭客户端
func (c *Client) Close() error {
	if c.ctxCache != nil {
		return c.ctxCache.Close()
	}
	return nil
}

// GetConfig 返回客户端配置（只读）
func (c *Client) GetConfig() ClientConfig {
	return *c.config
}

// WithContext 在上下文中嵌入当前客户端
func (c *Client) WithContext(ctx context.Context) context.Context {
	return WithCache(ctx, c.ctxCache)
}

// NewLRUClient 创建 LRU 缓存客户端的便利函数
func NewLRUClient(ctx context.Context, capacity int) (*Client, error) {
	return NewClient(ctx, &ClientConfig{
		Type:     CacheLRU,
		Capacity: capacity,
	})
}

// NewLRUOptimizedClient 创建优化版本 LRU 缓存客户端的便利函数
func NewLRUOptimizedClient(ctx context.Context, capacity int) (*Client, error) {
	return NewClient(ctx, &ClientConfig{
		Type:     CacheLRUOptimized,
		Capacity: capacity,
	})
}

// NewExpiringClient 创建过期缓存客户端的便利函数
func NewExpiringClient(ctx context.Context, cleanupInterval time.Duration) (*Client, error) {
	return NewClient(ctx, &ClientConfig{
		Type:            CacheExpiring,
		CleanupInterval: cleanupInterval,
	})
}

// NewRistrettoClient 创建 Ristretto 缓存客户端的便利函数
func NewRistrettoClient(ctx context.Context, config *RistrettoConfig) (*Client, error) {
	return NewClient(ctx, &ClientConfig{
		Type:            CacheRistretto,
		RistrettoConfig: config,
	})
}

// NewRedisClient 创建 Redis 缓存客户端的便利函数
func NewRedisClient(ctx context.Context, config *RedisConfig) (*Client, error) {
	return NewClient(ctx, &ClientConfig{
		Type:        CacheRedis,
		RedisConfig: config,
	})
}