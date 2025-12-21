/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-26 01:10:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-26 01:10:00
 * @FilePath: \go-cachex\multi_level_cache.go
 * @Description: 多级缓存 - 本地缓存+Redis组合(生产级别)
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */

package cachex

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// MultiLevelConfig 多级缓存配置
type MultiLevelConfig struct {
	Namespace         string
	L1Size            int           // L1本地缓存大小
	L1TTL             time.Duration // L1缓存过期时间
	L2TTL             time.Duration // L2 Redis缓存过期时间
	EnableCompression bool          // 是否启用压缩
	SyncInterval      time.Duration // 同步间隔
}

// MultiLevelCache 多级缓存(生产级别)
type MultiLevelCache[T any] struct {
	config      MultiLevelConfig
	l1Cache     *LRUOptimizedHandler // 本地LRU缓存
	l2Cache     *AdvancedCache[T]    // Redis缓存
	redisClient redis.UniversalClient

	// 统计信息
	stats struct {
		sync.RWMutex
		L1Hits int64
		L2Hits int64
		Misses int64
		L1Sets int64
		L2Sets int64
	}
}

// NewMultiLevelCache 创建多级缓存
func NewMultiLevelCache[T any](redisClient redis.UniversalClient, config MultiLevelConfig) *MultiLevelCache[T] {
	if config.L1Size == 0 {
		config.L1Size = 1000
	}
	if config.L1TTL == 0 {
		config.L1TTL = time.Minute * 5
	}
	if config.L2TTL == 0 {
		config.L2TTL = time.Hour
	}

	// 创建L1本地缓存
	l1Cache := NewLRUOptimizedHandler(config.L1Size)

	// 创建L2 Redis缓存
	compression := CompressionNone
	if config.EnableCompression {
		compression = CompressionGzip
	}

	// 类型断言为*redis.Client (AdvancedCache需要)
	client, ok := redisClient.(*redis.Client)
	if !ok {
		panic("MultiLevelCache requires *redis.Client for AdvancedCache, cluster mode not supported yet")
	}

	l2CacheConfig := AdvancedCacheConfig{
		DefaultTTL:  config.L2TTL,
		Namespace:   config.Namespace,
		Compression: compression,
	}
	l2Cache := NewAdvancedCache[T](client, l2CacheConfig)

	mlc := &MultiLevelCache[T]{
		config:      config,
		l1Cache:     l1Cache,
		l2Cache:     l2Cache,
		redisClient: redisClient,
	}

	return mlc
}

// Get 获取缓存(L1 → L2 → 加载器)
func (m *MultiLevelCache[T]) Get(ctx context.Context, key string, loader func() (T, error)) (T, error) {
	var zero T
	keyBytes := []byte(key)

	// 1. 尝试L1缓存
	if val, err := m.l1Cache.Get(keyBytes); err == nil {
		m.recordL1Hit()
		var result T
		if err := json.Unmarshal(val, &result); err == nil {
			return result, nil
		}
	}

	// 2. 尝试L2缓存
	val, exists, err := m.l2Cache.Get(ctx, key)
	if err == nil && exists {
		m.recordL2Hit()
		// 回填到L1
		if data, err := json.Marshal(val); err == nil {
			m.l1Cache.SetWithTTL(keyBytes, data, m.config.L1TTL)
		}
		return val, nil
	}

	// 3. 缓存未命中,使用加载器
	m.recordMiss()
	if loader == nil {
		return zero, fmt.Errorf("cache miss and no loader provided")
	}

	data, err := loader()
	if err != nil {
		return zero, err
	}

	// 4. 设置到L1和L2
	m.Set(ctx, key, data)

	return data, nil
}

// Set 设置缓存到L1和L2
func (m *MultiLevelCache[T]) Set(ctx context.Context, key string, value T) error {
	keyBytes := []byte(key)

	// 设置L1
	if data, err := json.Marshal(value); err == nil {
		m.l1Cache.SetWithTTL(keyBytes, data, m.config.L1TTL)
		m.recordL1Set()
	}

	// 设置L2
	err := m.l2Cache.Set(ctx, key, value, m.config.L2TTL)
	if err == nil {
		m.recordL2Set()
	}

	return err
}

// Delete 删除缓存
func (m *MultiLevelCache[T]) Delete(ctx context.Context, key string) error {
	keyBytes := []byte(key)

	// 删除L1
	m.l1Cache.Del(keyBytes)

	// 删除L2
	return m.l2Cache.Delete(ctx, key)
}

// InvalidateL1 使L1缓存失效
func (m *MultiLevelCache[T]) InvalidateL1(key string) {
	m.l1Cache.Del([]byte(key))
}

// GetStats 获取统计信息
func (m *MultiLevelCache[T]) GetStats() map[string]int64 {
	m.stats.RLock()
	defer m.stats.RUnlock()

	return map[string]int64{
		"l1_hits":  m.stats.L1Hits,
		"l2_hits":  m.stats.L2Hits,
		"misses":   m.stats.Misses,
		"l1_sets":  m.stats.L1Sets,
		"l2_sets":  m.stats.L2Sets,
		"hit_rate": m.calculateHitRate(),
	}
}

// calculateHitRate 计算命中率(百分比)
func (m *MultiLevelCache[T]) calculateHitRate() int64 {
	total := m.stats.L1Hits + m.stats.L2Hits + m.stats.Misses
	if total == 0 {
		return 0
	}
	hits := m.stats.L1Hits + m.stats.L2Hits
	return (hits * 100) / total
}

// 统计记录方法
func (m *MultiLevelCache[T]) recordL1Hit() {
	m.stats.Lock()
	m.stats.L1Hits++
	m.stats.Unlock()
}

func (m *MultiLevelCache[T]) recordL2Hit() {
	m.stats.Lock()
	m.stats.L2Hits++
	m.stats.Unlock()
}

func (m *MultiLevelCache[T]) recordMiss() {
	m.stats.Lock()
	m.stats.Misses++
	m.stats.Unlock()
}

func (m *MultiLevelCache[T]) recordL1Set() {
	m.stats.Lock()
	m.stats.L1Sets++
	m.stats.Unlock()
}

func (m *MultiLevelCache[T]) recordL2Set() {
	m.stats.Lock()
	m.stats.L2Sets++
	m.stats.Unlock()
}

// Clear 清空所有缓存
func (m *MultiLevelCache[T]) Clear(ctx context.Context) error {
	// L1没有Clear方法,手动实现
	// LRUOptimizedHandler没有导出Clear,我们需要重新创建
	m.l1Cache = NewLRUOptimizedHandler(m.config.L1Size)

	// L2清空所有匹配命名空间的key
	return m.l2Cache.Clear(ctx, m.config.Namespace+"*")
}

// CachePattern 缓存模式封装(生产级别)
type CachePattern[T any] struct {
	cache *MultiLevelCache[T]
	mu    sync.RWMutex
}

// NewCachePattern 创建缓存模式
func NewCachePattern[T any](cache *MultiLevelCache[T]) *CachePattern[T] {
	return &CachePattern[T]{cache: cache}
}

// CacheAside 旁路缓存模式(Cache-Aside)
// 读:先读缓存,未命中则读DB并写入缓存
// 写:先写DB,再删除缓存(延迟双删)
func (p *CachePattern[T]) CacheAside(
	ctx context.Context,
	key string,
	dbLoader func() (T, error),
	dbWriter func(T) error,
) *CacheAsideOperation[T] {
	return &CacheAsideOperation[T]{
		ctx:      ctx,
		key:      key,
		cache:    p.cache,
		dbLoader: dbLoader,
		dbWriter: dbWriter,
	}
}

// CacheAsideOperation 旁路缓存操作
type CacheAsideOperation[T any] struct {
	ctx      context.Context
	key      string
	cache    *MultiLevelCache[T]
	dbLoader func() (T, error)
	dbWriter func(T) error
}

// Read 读取数据
func (op *CacheAsideOperation[T]) Read() (T, error) {
	return op.cache.Get(op.ctx, op.key, op.dbLoader)
}

// Write 写入数据(延迟双删模式)
func (op *CacheAsideOperation[T]) Write(value T) error {
	// 先写DB
	if err := op.dbWriter(value); err != nil {
		return err
	}

	// 立即删除缓存
	op.cache.Delete(op.ctx, op.key)

	// 延迟再删一次(解决并发读写问题)
	go func() {
		time.Sleep(time.Millisecond * 500)
		op.cache.Delete(context.Background(), op.key)
	}()

	return nil
}

// ReadThrough 穿透读缓存模式
func (p *CachePattern[T]) ReadThrough(ctx context.Context, key string, dbLoader func() (T, error)) (T, error) {
	return p.cache.Get(ctx, key, dbLoader)
}

// WriteThrough 穿透写缓存模式
func (p *CachePattern[T]) WriteThrough(ctx context.Context, key string, value T, dbWriter func(T) error) error {
	// 先写DB
	if err := dbWriter(value); err != nil {
		return err
	}

	// 再更新缓存
	return p.cache.Set(ctx, key, value)
}

// WriteBehind 异步写缓存模式(Write-Behind/Write-Back)
func (p *CachePattern[T]) WriteBehind(ctx context.Context, key string, value T, dbWriter func(T) error) error {
	// 立即写缓存
	if err := p.cache.Set(ctx, key, value); err != nil {
		return err
	}

	// 异步写DB
	go func() {
		if err := dbWriter(value); err != nil {
			// 写DB失败,删除缓存保持一致性
			p.cache.Delete(context.Background(), key)
		}
	}()

	return nil
}
