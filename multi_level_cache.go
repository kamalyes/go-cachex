/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-26 01:10:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-10-25 15:08:55
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
	"sync/atomic"
	"time"

	"github.com/kamalyes/go-toolbox/pkg/mathx"
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
	//
	// 原为 sync.RWMutex 保护的普通 int64：每次 Get/Set 都要取写锁记一次计数，
	// 一把全局锁串行化了全部操作，使下层 L1 的分片完全失效（分片再多也排在同一把锁后）。
	// 改为 atomic.Int64 后计数为无锁原子加，与 AdvancedCache.metrics 的既有做法一致。
	// atomic.Int64 同时自带 64 位对齐保证，无需关心字段排布。
	stats struct {
		L1Hits atomic.Int64
		L2Hits atomic.Int64
		Misses atomic.Int64
		L1Sets atomic.Int64
		L2Sets atomic.Int64
	}
}

// NewMultiLevelCache 创建多级缓存
func NewMultiLevelCache[T any](redisClient redis.UniversalClient, config MultiLevelConfig) *MultiLevelCache[T] {
	// 使用 mathx.IfLeZero 设置默认值
	config.L1Size = mathx.IfLeZero(config.L1Size, 1000)
	config.L1TTL = mathx.IfLeZero(config.L1TTL, time.Minute*5)
	config.L2TTL = mathx.IfLeZero(config.L2TTL, time.Hour)
	config.Namespace = mathx.IfNotEmpty(config.Namespace, "mlc") // 空 namespace 默认 "mlc"

	// 创建L1本地缓存
	l1Cache := NewLRUOptimizedHandler(config.L1Size)

	// 创建L2 Redis缓存
	compression := CompressionNone
	if config.EnableCompression {
		compression = CompressionGzip
	}

	// AdvancedCache 已支持 UniversalClient，无需类型断言
	l2CacheConfig := AdvancedCacheConfig{
		DefaultTTL:  config.L2TTL,
		Namespace:   config.Namespace,
		Compression: compression,
	}
	l2Cache := NewAdvancedCache[T](redisClient, l2CacheConfig)

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
//
// 各计数独立原子读，不再持锁快照：返回值是各项的近似同期视图而非严格一致快照
// （并发写入下 l1_hits 与 misses 可能相差一次计数）。统计用途不需要线性一致性，
// 而旧实现为此在每次 Get/Set 上加了一把全局锁，代价远超收益。
func (m *MultiLevelCache[T]) GetStats() map[string]int64 {
	l1Hits := m.stats.L1Hits.Load()
	l2Hits := m.stats.L2Hits.Load()
	misses := m.stats.Misses.Load()

	return map[string]int64{
		"l1_hits":  l1Hits,
		"l2_hits":  l2Hits,
		"misses":   misses,
		"l1_sets":  m.stats.L1Sets.Load(),
		"l2_sets":  m.stats.L2Sets.Load(),
		"hit_rate": hitRateOf(l1Hits, l2Hits, misses),
	}
}

// calculateHitRate 计算命中率(百分比)
func (m *MultiLevelCache[T]) calculateHitRate() int64 {
	return hitRateOf(m.stats.L1Hits.Load(), m.stats.L2Hits.Load(), m.stats.Misses.Load())
}

// hitRateOf 由已读取的计数计算命中率百分比
// 独立成函数，使 GetStats 能复用同一批 Load 结果，避免同一次调用内重复读取出现自相矛盾的比率
func hitRateOf(l1Hits, l2Hits, misses int64) int64 {
	total := l1Hits + l2Hits + misses
	if total == 0 {
		return 0
	}
	return ((l1Hits + l2Hits) * 100) / total
}

// 统计记录方法（无锁原子加，位于每次 Get/Set 的热路径）
func (m *MultiLevelCache[T]) recordL1Hit() {
	m.stats.L1Hits.Add(1)
}

func (m *MultiLevelCache[T]) recordL2Hit() {
	m.stats.L2Hits.Add(1)
}

func (m *MultiLevelCache[T]) recordMiss() {
	m.stats.Misses.Add(1)
}

func (m *MultiLevelCache[T]) recordL1Set() {
	m.stats.L1Sets.Add(1)
}

func (m *MultiLevelCache[T]) recordL2Set() {
	m.stats.L2Sets.Add(1)
}

// Clear 清空所有缓存
func (m *MultiLevelCache[T]) Clear(ctx context.Context) error {
	// 使用 LRUOptimizedHandler.Clear 清空 L1 缓存
	// 不再替换整个 handler 实例，避免与并发 Get/Set 的数据竞争
	m.l1Cache.Clear()

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
