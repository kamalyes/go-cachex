/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 21:56:05
 * @FilePath: \go-cachex\ristretto.go
 * @Description: Ristretto 适配器（RistrettoHandler）
 *
 * 对 dgraph-io/ristretto 的薄包装，满足仓库中的 `Handler` 接口Ristretto
 * 提供高性能的本地缓存，支持基于成本的驱逐与统计指标使用此适配器
 * 可在进程内获得生产级缓存表现，但需要在项目中引入 ristretto 依赖
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"time"

	ristretto "github.com/dgraph-io/ristretto/v2"
)

// RistrettoHandler 是 Ristretto 缓存的实现
type RistrettoHandler struct {
	cache *ristretto.Cache[[]byte, []byte]
}

// Config 用于定制 Ristretto 缓存的配置
type RistrettoConfig struct {
	NumCounters                  int64 // 计数器数量，用于跟踪访问频率
	MaxCost                      int64 // 最大缓存成本
	BufferItems                  int64 // Get 缓存的大小
	Metrics                      bool  // 是否启用缓存统计
	OnEvict                      func(item *ristretto.Item[[]byte]) // 每次驱逐时调用
	OnReject                     func(item *ristretto.Item[[]byte]) // 每次拒绝时调用
	OnExit                       func(val []byte) // 从缓存中移除值时调用
	ShouldUpdate                 func(cur, prev []byte) bool // 更新时的检查函数
	KeyToHash                    func(key []byte) (uint64, uint64) // 自定义键哈希算法
	Cost                         func(value []byte) int64 // 计算值的成本
	IgnoreInternalCost           bool // 是否忽略内部存储的成本
	TtlTickerDurationInSec       int64 // TTL 过期时清理键的时间间隔 (为0时=bucketDurationSecs)
}

// NewDefaultRistrettoConfig 创建一个新的ristretto配置实例
func NewDefaultRistrettoConfig() *RistrettoConfig {
	return &RistrettoConfig{
		NumCounters: 1e7,     // 用于跟踪访问频率的键数量（1000万）
		MaxCost:     1 << 30, // 缓存的最大成本（1GB）
		BufferItems: 64,      // 每次 Get 请求的键数量
		Metrics:                false, // 不启用统计
		OnEvict:                nil,  // 不指定驱逐时的回调
		OnReject:               nil,  // 不指定拒绝时的回调
		OnExit:                 nil,  // 不指定移除值时的回调
		ShouldUpdate:           nil,  // 不指定更新检查函数
		KeyToHash:              nil,  // 使用默认哈希函数
		Cost:                   nil,  // 不指定成本计算函数
		IgnoreInternalCost:     false, // 不忽略内部成本
		TtlTickerDurationInSec: 1,   // TTL 过期时间间隔（1秒）
	}
}

// SetNumCounters 设置计数器数量
func (c *RistrettoConfig) SetNumCounters(numCounters int64) *RistrettoConfig {
	c.NumCounters = numCounters
	return c
}

// SetMaxCost 设置最大缓存成本
func (c *RistrettoConfig) SetMaxCost(maxCost int64) *RistrettoConfig {
	c.MaxCost = maxCost
	return c
}

// SetBufferItems 设置 Get 缓存的大小
func (c *RistrettoConfig) SetBufferItems(bufferItems int64) *RistrettoConfig {
	c.BufferItems = bufferItems
	return c
}

// EnableMetrics 启用缓存统计
func (c *RistrettoConfig) EnableMetrics() *RistrettoConfig {
	c.Metrics = true
	return c
}

// SetOnEvict 设置驱逐时的回调函数
func (c *RistrettoConfig) SetOnEvict(fn func(item *ristretto.Item[[]byte])) *RistrettoConfig {
	c.OnEvict = fn
	return c
}

// SetOnReject 设置拒绝时的回调函数
func (c *RistrettoConfig) SetOnReject(fn func(item *ristretto.Item[[]byte])) *RistrettoConfig {
	c.OnReject = fn
	return c
}

// SetOnExit 设置从缓存中移除值时的回调函数
func (c *RistrettoConfig) SetOnExit(fn func(val []byte)) *RistrettoConfig {
	c.OnExit = fn
	return c
}

// SetShouldUpdate 设置更新时的检查函数
func (c *RistrettoConfig) SetShouldUpdate(fn func(cur, prev []byte) bool) *RistrettoConfig {
	c.ShouldUpdate = fn
	return c
}

// SetKeyToHash 设置自定义键哈希算法
func (c *RistrettoConfig) SetKeyToHash(fn func(key []byte) (uint64, uint64)) *RistrettoConfig {
	c.KeyToHash = fn
	return c
}

// SetCost 设置计算值成本的函数
func (c *RistrettoConfig) SetCost(fn func(value []byte) int64) *RistrettoConfig {
	c.Cost = fn
	return c
}

// SetIgnoreInternalCost 设置是否忽略内部存储的成本
func (c *RistrettoConfig) SetIgnoreInternalCost(ignore bool) *RistrettoConfig {
	c.IgnoreInternalCost = ignore
	return c
}

// SetTtlTickerDurationInSec 设置 TTL 过期时间间隔
func (c *RistrettoConfig) SetTtlTickerDurationInSec(duration int64) *RistrettoConfig {
	c.TtlTickerDurationInSec = duration
	return c
}

// createCache 创建 Ristretto 缓存
func createCache(config *RistrettoConfig) (*ristretto.Cache[[]byte, []byte], error) {
	return ristretto.NewCache(&ristretto.Config[[]byte, []byte]{
		NumCounters:               config.NumCounters,
		MaxCost:                   config.MaxCost,
		BufferItems:               config.BufferItems,
		Metrics:                   config.Metrics,
		OnEvict:                   config.OnEvict,
		OnReject:                  config.OnReject,
		OnExit:                    config.OnExit,
		ShouldUpdate:              config.ShouldUpdate,
		KeyToHash:                 config.KeyToHash,
		Cost:                      config.Cost,
		IgnoreInternalCost:        config.IgnoreInternalCost,
		TtlTickerDurationInSec:    config.TtlTickerDurationInSec,
	})
}

// NewDefaultRistrettoHandler 创建默认的 RistrettoHandler
func NewDefaultRistrettoHandler() (*RistrettoHandler, error) {
	// 使用默认配置
	config := NewDefaultRistrettoConfig()
	cache, err := createCache(config)
	if err != nil {
		return nil, err
	}
	return &RistrettoHandler{cache: cache}, nil
}

// NewRistrettoHandler 创建新的 RistrettoHandler，支持自定义配置
func NewRistrettoHandler(config *RistrettoConfig) (*RistrettoHandler, error) {
	if config == nil {
		return NewDefaultRistrettoHandler()
	}

	cache, err := createCache(config)
	if err != nil {
		return nil, err
	}

	return &RistrettoHandler{cache: cache}, nil
}

// Get 实现 Handler 接口的 Get 方法
func (h *RistrettoHandler) Get(k []byte) ([]byte, error) {
	if err := ValidateBasicOp(k, h.cache != nil, false); err != nil {
		return nil, err
	}

	v, ok := h.cache.Get(k)
	if !ok {
		return nil, ErrNotFound
	}
	return v, nil
}

// GetTTL 实现 Handler 接口的 GetTTL 方法
func (h *RistrettoHandler) GetTTL(k []byte) (time.Duration, error) {
	if err := ValidateBasicOp(k, h.cache != nil, false); err != nil {
		return 0, err
	}

	v, ok := h.cache.GetTTL(k)
	if !ok {
		return 0, ErrNotFound
	}
	return v, nil
}

// Set 实现 Handler 接口的 Set 方法
func (h *RistrettoHandler) Set(k, v []byte) error {
	if err := ValidateWriteOp(k, v, h.cache != nil, false); err != nil {
		return err
	}

	if ok := h.cache.Set(k, v, 1); !ok {
		return ErrCapacityExceeded
	}
	h.cache.Wait()
	return nil
}

// SetWithTTL 实现 Handler 接口的 SetWithTTL 方法
func (h *RistrettoHandler) SetWithTTL(k, v []byte, ttl time.Duration) error {
	if err := ValidateWriteWithTTLOp(k, v, ttl, h.cache != nil, false); err != nil {
		return err
	}

	var ok bool
	if ttl == -1 {
		ok = h.cache.SetWithTTL(k, v, 1, 0*time.Second)
	} else {
		ok = h.cache.SetWithTTL(k, v, 1, ttl)
	}
	if !ok {
		return ErrCapacityExceeded
	}
	h.cache.Wait()
	return nil
}

// Del 实现 Handler 接口的 Del 方法
func (h *RistrettoHandler) Del(k []byte) error {
	if err := ValidateBasicOp(k, h.cache != nil, false); err != nil {
		return err
	}

	h.cache.Del(k)
	h.cache.Wait()
	return nil
}

// Close 实现 Handler 接口的 Close 方法
func (h *RistrettoHandler) Close() error {
	if h.cache == nil {
		return ErrNotInitialized
	}

	h.cache.Close()
	h.cache = nil
	return nil
}
