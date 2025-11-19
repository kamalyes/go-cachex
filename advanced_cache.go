/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-19 00:00:00
 * @FilePath: \go-cachex\advanced_cache.go
 * @Description: 高级缓存包装器，支持压缩和泛型，集成队列、热key和分布式锁功能
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// CompressionType 压缩类型
type CompressionType string

const (
	CompressionNone CompressionType = "none"
	CompressionGzip CompressionType = "gzip"
)

// AdvancedCacheConfig 高级缓存配置
type AdvancedCacheConfig struct {
	Compression      CompressionType // 压缩类型
	MinSizeForCompress int            // 启用压缩的最小大小（字节）
	DefaultTTL       time.Duration   // 默认TTL
	Namespace        string          // 命名空间
	EnableMetrics    bool            // 是否启用指标统计
}

// CacheMetrics 缓存指标
type CacheMetrics struct {
	Hits           int64 `json:"hits"`
	Misses         int64 `json:"misses"`
	Sets           int64 `json:"sets"`
	Deletes        int64 `json:"deletes"`
	Compressions   int64 `json:"compressions"`
	Decompressions int64 `json:"decompressions"`
	TotalSize      int64 `json:"total_size"`
}

// AdvancedCache 高级缓存包装器
type AdvancedCache[T any] struct {
	client    *redis.Client
	config    AdvancedCacheConfig
	metrics   *CacheMetrics
	queue     *QueueHandler
	hotkey    *HotKeyManager
	lockMgr   *LockManager
}

// NewAdvancedCache 创建高级缓存
func NewAdvancedCache[T any](client *redis.Client, config AdvancedCacheConfig) *AdvancedCache[T] {
	if config.MinSizeForCompress == 0 {
		config.MinSizeForCompress = 1024 // 1KB
	}
	if config.DefaultTTL == 0 {
		config.DefaultTTL = time.Hour
	}
	if config.Namespace == "" {
		config.Namespace = "cache"
	}

	cache := &AdvancedCache[T]{
		client:  client,
		config:  config,
		metrics: &CacheMetrics{},
	}

	// 初始化队列
	queueConfig := QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second * 5,
		BatchSize:       10,
		LockTimeout:     time.Minute * 5,
		CleanupInterval: time.Minute * 10,
	}
	cache.queue = NewQueueHandler(client, config.Namespace, queueConfig)

	// 初始化热key管理器
	hotkeyConfig := HotKeyConfig{
		DefaultTTL:        time.Hour,
		RefreshInterval:   time.Minute * 10,
		EnableAutoRefresh: true,
		Namespace:         config.Namespace,
	}
	cache.hotkey = NewHotKeyManager(client, hotkeyConfig)

	// 初始化锁管理器
	lockConfig := LockConfig{
		TTL:              time.Minute * 5,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       10,
		Namespace:        config.Namespace,
		EnableWatchdog:   true,
		WatchdogInterval: time.Minute,
	}
	cache.lockMgr = NewLockManager(client, lockConfig)

	return cache
}

// getKey 获取完整的键名
func (c *AdvancedCache[T]) getKey(key string) string {
	return fmt.Sprintf("%s:%s", c.config.Namespace, key)
}

// compress 压缩数据
func (c *AdvancedCache[T]) compress(data []byte) ([]byte, error) {
	if c.config.Compression == CompressionNone || len(data) < c.config.MinSizeForCompress {
		return data, nil
	}

	var buf strings.Builder
	writer := gzip.NewWriter(&buf)
	
	if _, err := writer.Write(data); err != nil {
		writer.Close()
		return nil, err
	}
	
	if err := writer.Close(); err != nil {
		return nil, err
	}

	if c.config.EnableMetrics {
		c.metrics.Compressions++
	}

	return []byte(buf.String()), nil
}

// decompress 解压缩数据
func (c *AdvancedCache[T]) decompress(data []byte) ([]byte, error) {
	if c.config.Compression == CompressionNone {
		return data, nil
	}

	reader, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		// 可能是未压缩的数据
		return data, nil
	}
	defer reader.Close()

	result, err := io.ReadAll(reader)
	if err != nil {
		// 如果解压缩失败，返回原始数据
		return data, nil
	}

	if c.config.EnableMetrics {
		c.metrics.Decompressions++
	}

	return result, nil
}

// Set 设置缓存
func (c *AdvancedCache[T]) Set(ctx context.Context, key string, value T, ttl ...time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	// 压缩数据
	compressedData, err := c.compress(data)
	if err != nil {
		return fmt.Errorf("failed to compress data: %w", err)
	}

	// 设置TTL
	cacheTTL := c.config.DefaultTTL
	if len(ttl) > 0 {
		cacheTTL = ttl[0]
	}

	fullKey := c.getKey(key)
	err = c.client.SetEx(ctx, fullKey, compressedData, cacheTTL).Err()
	if err != nil {
		return err
	}

	if c.config.EnableMetrics {
		c.metrics.Sets++
		c.metrics.TotalSize += int64(len(compressedData))
	}

	return nil
}

// Get 获取缓存
func (c *AdvancedCache[T]) Get(ctx context.Context, key string) (T, bool, error) {
	var zero T
	
	fullKey := c.getKey(key)
	data, err := c.client.Get(ctx, fullKey).Bytes()
	if err != nil {
		if err == redis.Nil {
			if c.config.EnableMetrics {
				c.metrics.Misses++
			}
			return zero, false, nil
		}
		return zero, false, err
	}

	// 解压缩数据
	decompressedData, err := c.decompress(data)
	if err != nil {
		return zero, false, fmt.Errorf("failed to decompress data: %w", err)
	}

	var value T
	if err := json.Unmarshal(decompressedData, &value); err != nil {
		return zero, false, fmt.Errorf("failed to unmarshal value: %w", err)
	}

	if c.config.EnableMetrics {
		c.metrics.Hits++
	}

	return value, true, nil
}

// GetOrSet 获取或设置缓存（如果不存在则设置）
func (c *AdvancedCache[T]) GetOrSet(ctx context.Context, key string, fn func() (T, error), ttl ...time.Duration) (T, error) {
	// 先尝试获取
	if value, exists, err := c.Get(ctx, key); err == nil && exists {
		return value, nil
	}

	// 使用分布式锁防止缓存击穿
	lockKey := fmt.Sprintf("getorset:%s", key)
	lock := c.lockMgr.GetLock(lockKey)
	
	if err := lock.Lock(ctx); err != nil {
		// 如果获取锁失败，直接执行函数
		return fn()
	}
	defer lock.Unlock(ctx)

	// 再次检查缓存（可能在等待锁的过程中其他协程已设置）
	if value, exists, err := c.Get(ctx, key); err == nil && exists {
		return value, nil
	}

	// 执行函数获取值
	value, err := fn()
	if err != nil {
		return value, err
	}

	// 设置缓存
	if setErr := c.Set(ctx, key, value, ttl...); setErr != nil {
		// 记录错误但不影响返回结果
		fmt.Printf("failed to set cache: %v\n", setErr)
	}

	return value, nil
}

// Delete 删除缓存
func (c *AdvancedCache[T]) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}

	fullKeys := make([]string, len(keys))
	for i, key := range keys {
		fullKeys[i] = c.getKey(key)
	}

	err := c.client.Del(ctx, fullKeys...).Err()
	if err != nil {
		return err
	}

	if c.config.EnableMetrics {
		c.metrics.Deletes += int64(len(keys))
	}

	return nil
}

// Exists 检查键是否存在
func (c *AdvancedCache[T]) Exists(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}

	fullKeys := make([]string, len(keys))
	for i, key := range keys {
		fullKeys[i] = c.getKey(key)
	}

	return c.client.Exists(ctx, fullKeys...).Result()
}

// TTL 获取键的TTL
func (c *AdvancedCache[T]) TTL(ctx context.Context, key string) (time.Duration, error) {
	fullKey := c.getKey(key)
	return c.client.TTL(ctx, fullKey).Result()
}

// Expire 设置键的过期时间
func (c *AdvancedCache[T]) Expire(ctx context.Context, key string, ttl time.Duration) error {
	fullKey := c.getKey(key)
	return c.client.Expire(ctx, fullKey, ttl).Err()
}

// Keys 获取匹配模式的所有键
func (c *AdvancedCache[T]) Keys(ctx context.Context, pattern string) ([]string, error) {
	fullPattern := c.getKey(pattern)
	keys, err := c.client.Keys(ctx, fullPattern).Result()
	if err != nil {
		return nil, err
	}

	// 移除命名空间前缀
	prefix := c.config.Namespace + ":"
	result := make([]string, len(keys))
	for i, key := range keys {
		result[i] = strings.TrimPrefix(key, prefix)
	}

	return result, nil
}

// Clear 清空指定模式的所有缓存
func (c *AdvancedCache[T]) Clear(ctx context.Context, pattern string) error {
	keys, err := c.Keys(ctx, pattern)
	if err != nil {
		return err
	}

	if len(keys) == 0 {
		return nil
	}

	return c.Delete(ctx, keys...)
}

// GetQueue 获取队列处理器
func (c *AdvancedCache[T]) GetQueue() *QueueHandler {
	return c.queue
}

// GetHotKeyManager 获取热key管理器
func (c *AdvancedCache[T]) GetHotKeyManager() *HotKeyManager {
	return c.hotkey
}

// GetLockManager 获取锁管理器
func (c *AdvancedCache[T]) GetLockManager() *LockManager {
	return c.lockMgr
}

// GetMetrics 获取缓存指标
func (c *AdvancedCache[T]) GetMetrics() *CacheMetrics {
	if !c.config.EnableMetrics {
		return nil
	}
	return c.metrics
}

// ResetMetrics 重置缓存指标
func (c *AdvancedCache[T]) ResetMetrics() {
	if c.config.EnableMetrics {
		c.metrics = &CacheMetrics{}
	}
}

// Ping 测试连接
func (c *AdvancedCache[T]) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// Close 关闭缓存
func (c *AdvancedCache[T]) Close() error {
	// 停止热key自动刷新
	c.hotkey.StopAll()
	
	// 释放所有锁
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	
	c.lockMgr.ReleaseAllLocks(ctx)
	
	return c.client.Close()
}

// CacheWrapper 缓存包装器函数类型
type CacheWrapperFunc[T any] func(ctx context.Context) (T, error)

// Wrap 包装函数以添加缓存功能
func (c *AdvancedCache[T]) Wrap(key string, fn CacheWrapperFunc[T], ttl ...time.Duration) CacheWrapperFunc[T] {
	return func(ctx context.Context) (T, error) {
		return c.GetOrSet(ctx, key, func() (T, error) { return fn(ctx) }, ttl...)
	}
}

// BatchGet 批量获取
func (c *AdvancedCache[T]) BatchGet(ctx context.Context, keys []string) (map[string]T, error) {
	if len(keys) == 0 {
		return make(map[string]T), nil
	}

	fullKeys := make([]string, len(keys))
	for i, key := range keys {
		fullKeys[i] = c.getKey(key)
	}

	// 使用Pipeline批量获取
	pipe := c.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(fullKeys))
	
	for i, fullKey := range fullKeys {
		cmds[i] = pipe.Get(ctx, fullKey)
	}

	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, err
	}

	result := make(map[string]T)
	
	for i, cmd := range cmds {
		data, err := cmd.Bytes()
		if err != nil {
			if err == redis.Nil {
				continue // 跳过不存在的键
			}
			return nil, err
		}

		// 解压缩和反序列化
		decompressedData, err := c.decompress(data)
		if err != nil {
			continue
		}

		var value T
		if err := json.Unmarshal(decompressedData, &value); err != nil {
			continue
		}

		result[keys[i]] = value
	}

	return result, nil
}

// BatchSet 批量设置
func (c *AdvancedCache[T]) BatchSet(ctx context.Context, items map[string]T, ttl ...time.Duration) error {
	if len(items) == 0 {
		return nil
	}

	cacheTTL := c.config.DefaultTTL
	if len(ttl) > 0 {
		cacheTTL = ttl[0]
	}

	pipe := c.client.Pipeline()
	
	for key, value := range items {
		data, err := json.Marshal(value)
		if err != nil {
			continue
		}

		compressedData, err := c.compress(data)
		if err != nil {
			continue
		}

		fullKey := c.getKey(key)
		pipe.SetEx(ctx, fullKey, compressedData, cacheTTL)
	}

	_, err := pipe.Exec(ctx)
	return err
}