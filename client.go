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

	"github.com/redis/go-redis/v9"
)

// 确保 Client 实现了 Handler 接口
var _ Handler = (*Client)(nil)

// CacheType 定义支持的缓存类型
type CacheType string

const (
	CacheLRU          CacheType = "lru"
	CacheLRUOptimized CacheType = "lru_optimized"
	CacheRistretto    CacheType = "ristretto"
	CacheRedis        CacheType = "redis"
	CacheExpiring     CacheType = "expiring"
)

// ClientConfig 用于统一配置入口
type ClientConfig struct {
	Type            CacheType
	Capacity        int              // for lru/expiring/ristretto
	CleanupInterval time.Duration    // for expiring cache
	RedisConfig     *redis.Options   // for redis
	RistrettoConfig *RistrettoConfig // for ristretto
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

// ========== Handler接口实现（简化版方法） ==========

// Get 实现Handler.Get
func (c *Client) Get(key []byte) ([]byte, error) {
	return c.ctxCache.Get(context.Background(), key)
}

// GetTTL 实现Handler.GetTTL
func (c *Client) GetTTL(key []byte) (time.Duration, error) {
	return c.ctxCache.GetTTL(context.Background(), key)
}

// Set 实现Handler.Set
func (c *Client) Set(key, value []byte) error {
	return c.ctxCache.Set(context.Background(), key, value)
}

// SetWithTTL 实现Handler.SetWithTTL
func (c *Client) SetWithTTL(key, value []byte, ttl time.Duration) error {
	return c.ctxCache.SetWithTTL(context.Background(), key, value, ttl)
}

// Del 实现Handler.Del
func (c *Client) Del(key []byte) error {
	return c.ctxCache.Del(context.Background(), key)
}

// BatchGet 实现Handler.BatchGet
func (c *Client) BatchGet(keys [][]byte) ([][]byte, []error) {
	return c.ctxCache.BatchGet(context.Background(), keys)
}

// GetOrCompute 实现Handler.GetOrCompute
func (c *Client) GetOrCompute(key []byte, ttl time.Duration, loader func() ([]byte, error)) ([]byte, error) {
	ctxLoader := func(ctx context.Context) ([]byte, error) { return loader() }
	return c.ctxCache.GetOrCompute(context.Background(), key, ttl, ctxLoader)
}

// ========== 带Context的完整版方法 ==========

// GetWithCtx 从缓存中获取值（支持context）
func (c *Client) GetWithCtx(ctx context.Context, key []byte) ([]byte, error) {
	return c.ctxCache.Get(ctx, key)
}

// SetWithCtx 设置缓存值（支持context）
func (c *Client) SetWithCtx(ctx context.Context, key, value []byte) error {
	return c.ctxCache.Set(ctx, key, value)
}

// SetWithTTLAndCtx 设置带TTL的缓存值（支持context）
func (c *Client) SetWithTTLAndCtx(ctx context.Context, key, value []byte, ttl time.Duration) error {
	return c.ctxCache.SetWithTTL(ctx, key, value, ttl)
}

// DelWithCtx 删除缓存键（支持context）
func (c *Client) DelWithCtx(ctx context.Context, key []byte) error {
	return c.ctxCache.Del(ctx, key)
}

// GetTTLWithCtx 获取键的剩余TTL（支持context）
func (c *Client) GetTTLWithCtx(ctx context.Context, key []byte) (time.Duration, error) {
	return c.ctxCache.GetTTL(ctx, key)
}

// GetOrComputeWithCtx 获取或计算缓存值（带去重，支持context）
func (c *Client) GetOrComputeWithCtx(ctx context.Context, key []byte, ttl time.Duration, loader func(context.Context) ([]byte, error)) ([]byte, error) {
	return c.ctxCache.GetOrCompute(ctx, key, ttl, loader)
}

// BatchGetWithCtx 批量获取多个键的值（支持context）
func (c *Client) BatchGetWithCtx(ctx context.Context, keys [][]byte) ([][]byte, []error) {
	select {
	case <-ctx.Done():
		errors := make([]error, len(keys))
		for i := range errors {
			errors[i] = ctx.Err()
		}
		return make([][]byte, len(keys)), errors
	default:
	}

	if c.ctxCache == nil {
		errors := make([]error, len(keys))
		for i := range errors {
			errors[i] = ErrNotInitialized
		}
		return make([][]byte, len(keys)), errors
	}

	return c.ctxCache.BatchGet(ctx, keys)
}

// Stats 实现Handler.Stats（不带context）
func (c *Client) Stats() map[string]interface{} {
	if c.ctxCache == nil {
		return map[string]interface{}{
			"error": ErrNotInitialized.Error(),
		}
	}

	stats := c.ctxCache.Stats()
	// 添加客户端级别的信息
	stats["client_type"] = string(c.config.Type)
	stats["client_capacity"] = c.config.Capacity
	return stats
}

// StatsWithCtx 返回缓存统计信息（支持context）
func (c *Client) StatsWithCtx(ctx context.Context) map[string]interface{} {
	select {
	case <-ctx.Done():
		return map[string]interface{}{
			"error": ctx.Err().Error(),
		}
	default:
	}

	return c.Stats()
}

// Close 实现Handler.Close
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
func NewRedisClient(ctx context.Context, opts *redis.Options) (*Client, error) {
	return NewClient(ctx, &ClientConfig{
		Type:        CacheRedis,
		RedisConfig: opts,
	})
}
