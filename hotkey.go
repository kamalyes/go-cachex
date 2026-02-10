/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-19 00:00:00
 * @FilePath: \go-cachex\hotkey.go
 * @Description: 热key缓存实现，提供灵活的数据字典缓存功能
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

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/mathx"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
	"github.com/redis/go-redis/v9"
)

// HotKeyConfig 热key配置
type HotKeyConfig struct {
	DefaultTTL        time.Duration  // 默认TTL
	RefreshInterval   time.Duration  // 刷新间隔
	EnableAutoRefresh bool           // 是否启用自动刷新
	Namespace         string         // 命名空间
	Logger            logger.ILogger // 日志记录器（可选，不设置则使用 NoOpLogger）
	MaxLocalCacheSize int            // 本地缓存最大条目数，防止无界增长，默认 10000
}

// DataLoader 数据加载器接口
type DataLoader[K comparable, V any] interface {
	Load(ctx context.Context) (map[K]V, error)
}

// SQLDataLoader SQL数据加载器
type SQLDataLoader[K comparable, V any] struct {
	QueryFunc func(ctx context.Context) (map[K]V, error)
}

func (s *SQLDataLoader[K, V]) Load(ctx context.Context) (map[K]V, error) {
	return s.QueryFunc(ctx)
}

// HotKeyCache 热key缓存
type HotKeyCache[K comparable, V any] struct {
	client          *redis.Client
	config          HotKeyConfig
	loader          DataLoader[K, V]
	keyName         string
	mu              sync.RWMutex
	localCache      map[K]V
	lastRefreshTime time.Time
	stopChan        chan struct{}
	once            sync.Once
	logger          logger.ILogger
	// 防止本地缓存无界增长
	accessOrder   []K          // 访问顺序，用于 LRU 驱逐
	accessOrderMu sync.Mutex   // 保护 accessOrder
	cleanupTicker *time.Ticker // 定期清理过期数据
}

// NewHotKeyCache 创建热key缓存
func NewHotKeyCache[K comparable, V any](
	client *redis.Client,
	keyName string,
	loader DataLoader[K, V],
	config HotKeyConfig,
) *HotKeyCache[K, V] {
	config.DefaultTTL = mathx.IfNotZero(config.DefaultTTL, time.Hour)
	config.RefreshInterval = mathx.IfNotZero(config.RefreshInterval, time.Minute*10)
	config.Namespace = mathx.IfNotEmpty(config.Namespace, "hotkey")
	// 防止本地缓存无界增长：设置最大条目数
	config.MaxLocalCacheSize = mathx.IF(config.MaxLocalCacheSize > 0, config.MaxLocalCacheSize, 10000)

	cache := &HotKeyCache[K, V]{
		client:        client,
		config:        config,
		loader:        loader,
		keyName:       keyName,
		localCache:    make(map[K]V),
		accessOrder:   make([]K, 0),
		stopChan:      make(chan struct{}),
		logger:        mathx.IfEmpty(config.Logger, NewDefaultCachexLogger()),
		cleanupTicker: time.NewTicker(time.Minute), // 每分钟清理一次
	}

	// 启动自动刷新
	if config.EnableAutoRefresh {
		syncx.Go().
			OnPanic(func(r interface{}) {
				cache.logger.Errorf("Panic in autoRefresh: %v", r)
			}).
			Exec(cache.autoRefresh)
	}

	// 启动定期清理
	syncx.Go().
		OnPanic(func(r interface{}) {
			cache.logger.Errorf("Panic in cleanup: %v", r)
		}).
		Exec(cache.cleanupExpired)

	return cache
}

// getRedisKey 获取Redis键名
func (h *HotKeyCache[K, V]) getRedisKey() string {
	return fmt.Sprintf("%s:%s", h.config.Namespace, h.keyName)
}

// Get 获取单个值
func (h *HotKeyCache[K, V]) Get(ctx context.Context, key K) (V, bool, error) {
	var zero V

	// 先尝试从本地缓存获取
	h.mu.RLock()
	if value, exists := h.localCache[key]; exists {
		h.mu.RUnlock()
		return value, true, nil
	}
	h.mu.RUnlock()

	// 从Redis获取整个映射
	data, err := h.LoadAll(ctx)
	if err != nil {
		return zero, false, err
	}

	value, exists := data[key]
	return value, exists, nil
}

// GetAll 获取所有值
func (h *HotKeyCache[K, V]) GetAll(ctx context.Context) (map[K]V, error) {
	// 先尝试从本地缓存获取
	h.mu.RLock()
	if time.Since(h.lastRefreshTime) < h.config.RefreshInterval {
		// 如果在刷新间隔内，直接返回本地缓存（可能为空）
		result := make(map[K]V, len(h.localCache))
		for k, v := range h.localCache {
			result[k] = v
		}
		h.mu.RUnlock()
		return result, nil
	}
	h.mu.RUnlock()

	// 从Redis加载
	return h.LoadAll(ctx)
}

// LoadAll 从Redis或数据源加载所有数据
func (h *HotKeyCache[K, V]) LoadAll(ctx context.Context) (map[K]V, error) {
	redisKey := h.getRedisKey()

	// 先尝试从Redis获取
	cachedData, err := h.client.Get(ctx, redisKey).Result()
	if err == nil && cachedData != "" {
		var data map[K]V
		if err := json.Unmarshal([]byte(cachedData), &data); err == nil {
			// 更新本地缓存
			h.mu.Lock()
			h.localCache = make(map[K]V, len(data))
			for k, v := range data {
				h.localCache[k] = v
			}
			h.lastRefreshTime = time.Now()
			h.mu.Unlock()

			return data, nil
		}
	}

	// Redis中没有数据，从数据源加载
	data, err := h.loader.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load data from source: %w", err)
	}

	// 保存到Redis
	if len(data) > 0 {
		jsonData, err := json.Marshal(data)
		if err == nil {
			h.client.SetEx(ctx, redisKey, string(jsonData), h.config.DefaultTTL)
		}

		// 更新本地缓存
		h.mu.Lock()
		h.localCache = make(map[K]V, len(data))
		for k, v := range data {
			h.localCache[k] = v
		}
		h.lastRefreshTime = time.Now()
		h.mu.Unlock()
	}

	return data, nil
}

// Set 设置单个值
func (h *HotKeyCache[K, V]) Set(ctx context.Context, key K, value V) error {
	// 更新本地缓存
	h.mu.Lock()
	h.localCache[key] = value
	h.mu.Unlock()

	// 重新保存到Redis
	return h.SaveToRedis(ctx)
}

// SetAll 设置所有值
func (h *HotKeyCache[K, V]) SetAll(ctx context.Context, data map[K]V) error {
	// 更新本地缓存
	h.mu.Lock()
	h.localCache = make(map[K]V, len(data))
	for k, v := range data {
		h.localCache[k] = v
	}
	h.lastRefreshTime = time.Now()
	h.mu.Unlock()

	// 保存到Redis
	return h.SaveToRedis(ctx)
}

// Delete 删除单个键
func (h *HotKeyCache[K, V]) Delete(ctx context.Context, key K) error {
	// 从本地缓存删除
	h.mu.Lock()
	delete(h.localCache, key)
	h.mu.Unlock()

	// 重新保存到Redis
	return h.SaveToRedis(ctx)
}

// SaveToRedis 保存到Redis
func (h *HotKeyCache[K, V]) SaveToRedis(ctx context.Context) error {
	h.mu.RLock()
	data := make(map[K]V, len(h.localCache))
	for k, v := range h.localCache {
		data[k] = v
	}
	h.mu.RUnlock()

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	redisKey := h.getRedisKey()
	return h.client.SetEx(ctx, redisKey, string(jsonData), h.config.DefaultTTL).Err()
}

// Refresh 手动刷新缓存
func (h *HotKeyCache[K, V]) Refresh(ctx context.Context) error {
	data, err := h.loader.Load(ctx)
	if err != nil {
		return fmt.Errorf("failed to refresh data: %w", err)
	}

	return h.SetAll(ctx, data)
}

// Clear 清空缓存
func (h *HotKeyCache[K, V]) Clear(ctx context.Context) error {
	// 清空本地缓存并设置刷新时间为当前时间
	// 这样避免GetAll重新加载数据
	h.mu.Lock()
	h.localCache = make(map[K]V)
	h.lastRefreshTime = time.Now()
	h.mu.Unlock()

	// 删除Redis缓存
	redisKey := h.getRedisKey()
	return h.client.Del(ctx, redisKey).Err()
}

// Exists 检查键是否存在
func (h *HotKeyCache[K, V]) Exists(ctx context.Context, key K) (bool, error) {
	// 确保数据已加载
	_, err := h.GetAll(ctx)
	if err != nil {
		return false, err
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	_, exists := h.localCache[key]
	return exists, nil
}

// Keys 获取所有键
func (h *HotKeyCache[K, V]) Keys(ctx context.Context) ([]K, error) {
	data, err := h.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	keys := make([]K, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}

	return keys, nil
}

// Size 获取缓存大小（直接从本地缓存计算，避免触发加载）
func (h *HotKeyCache[K, V]) Size(ctx context.Context) (int, error) {
	// 确保数据已加载
	_, err := h.GetAll(ctx)
	if err != nil {
		return 0, err
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.localCache), nil
}

// autoRefresh 自动刷新
func (h *HotKeyCache[K, V]) autoRefresh() {
	ticker := time.NewTicker(h.config.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
			if err := h.Refresh(ctx); err != nil {
				// 记录错误日志，但不停止刷新
				h.logger.Warnf("HotKeyCache auto refresh failed: %v", err)
			}
			cancel()
		case <-h.stopChan:
			return
		}
	}
}

// Stop 停止自动刷新和清理
func (h *HotKeyCache[K, V]) Stop() {
	h.once.Do(func() {
		// 停止自动刷新和清理
		close(h.stopChan)
		if h.cleanupTicker != nil {
			h.cleanupTicker.Stop()
		}
	})
}

// cleanupExpired 定期清理过期数据，防止本地缓存无界增长
func (h *HotKeyCache[K, V]) cleanupExpired() {
	for {
		select {
		case <-h.stopChan:
			return
		case <-h.cleanupTicker.C:
			h.mu.Lock()
			cacheSize := len(h.localCache)

			// 如果超过最大限制，执行 LRU 驱逐
			if cacheSize > h.config.MaxLocalCacheSize {
				// 计算需要驱逐的数量（驱逐 20% 的数据）
				toEvict := cacheSize / 5
				if toEvict < 1 {
					toEvict = 1
				}

				// 从访问顺序列表中移除最旧的条目
				h.accessOrderMu.Lock()
				for i := 0; i < toEvict && len(h.accessOrder) > 0; i++ {
					key := h.accessOrder[0]
					h.accessOrder = h.accessOrder[1:]
					delete(h.localCache, key)
				}
				h.accessOrderMu.Unlock()

				h.logger.Warnf("HotKeyCache %s evicted %d entries (size: %d -> %d)",
					h.keyName, toEvict, cacheSize, len(h.localCache))
			}
			h.mu.Unlock()
		}
	}
}

// GetStats 获取统计信息
type HotKeyStats struct {
	KeyName         string    `json:"key_name"`
	LocalCacheSize  int       `json:"local_cache_size"`
	LastRefreshTime time.Time `json:"last_refresh_time"`
	TTL             int64     `json:"ttl_seconds"`
}

func (h *HotKeyCache[K, V]) GetStats(ctx context.Context) (*HotKeyStats, error) {
	h.mu.RLock()
	size := len(h.localCache)
	lastRefresh := h.lastRefreshTime
	h.mu.RUnlock()

	// 获取Redis TTL
	redisKey := h.getRedisKey()
	ttl, err := h.client.TTL(ctx, redisKey).Result()
	if err != nil {
		ttl = 0
	}

	return &HotKeyStats{
		KeyName:         h.keyName,
		LocalCacheSize:  size,
		LastRefreshTime: lastRefresh,
		TTL:             int64(ttl.Seconds()),
	}, nil
}

// HotKeyManager 热key管理器
type HotKeyManager struct {
	client *redis.Client
	config HotKeyConfig
	caches map[string]interface{} // 存储不同类型的缓存
	mu     sync.RWMutex
}

// NewHotKeyManager 创建热key管理器
func NewHotKeyManager(redisClient redis.UniversalClient, config ...HotKeyConfig) *HotKeyManager {
	// 类型断言为*redis.Client
	client, ok := redisClient.(*redis.Client)
	if !ok {
		panic("HotKeyManager requires *redis.Client, cluster mode not supported yet")
	}

	cfg := HotKeyConfig{
		DefaultTTL:        time.Hour,
		RefreshInterval:   time.Minute * 5,
		EnableAutoRefresh: true,
		Namespace:         "hotkey",
	}
	if len(config) > 0 {
		cfg = config[0]
	}

	return &HotKeyManager{
		client: client,
		config: cfg,
		caches: make(map[string]interface{}),
	}
}

// RegisterCache 注册缓存
func (m *HotKeyManager) RegisterCache(name string, cache interface{}) {
	m.mu.Lock()
	m.caches[name] = cache
	m.mu.Unlock()
}

// GetCache 获取缓存
func (m *HotKeyManager) GetCache(name string) (interface{}, bool) {
	m.mu.RLock()
	cache, exists := m.caches[name]
	m.mu.RUnlock()
	return cache, exists
}

// RefreshAll 刷新所有缓存
func (m *HotKeyManager) RefreshAll(ctx context.Context) error {
	m.mu.RLock()
	caches := make(map[string]interface{}, len(m.caches))
	for k, v := range m.caches {
		caches[k] = v
	}
	m.mu.RUnlock()

	for name, cache := range caches {
		if refresher, ok := cache.(interface{ Refresh(context.Context) error }); ok {
			if err := refresher.Refresh(ctx); err != nil {
				return fmt.Errorf("failed to refresh cache %s: %w", name, err)
			}
		}
	}

	return nil
}

// Register 注册热key缓存
func (m *HotKeyManager) Register(name string, cache interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caches[name] = cache
}

// Get 获取热key缓存
func (m *HotKeyManager) Get(name string) (interface{}, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cache, exists := m.caches[name]
	return cache, exists
}

// StopAll 停止所有自动刷新
func (m *HotKeyManager) StopAll() {
	m.mu.RLock()
	caches := make(map[string]interface{}, len(m.caches))
	for k, v := range m.caches {
		caches[k] = v
	}
	m.mu.RUnlock()

	for _, cache := range caches {
		if stopper, ok := cache.(interface{ Stop() }); ok {
			stopper.Stop()
		}
	}
}

// GetAllStats 获取所有缓存统计
func (m *HotKeyManager) GetAllStats(ctx context.Context) (map[string]*HotKeyStats, error) {
	m.mu.RLock()
	caches := make(map[string]interface{}, len(m.caches))
	for k, v := range m.caches {
		caches[k] = v
	}
	m.mu.RUnlock()

	stats := make(map[string]*HotKeyStats)

	for name, cache := range caches {
		if statsGetter, ok := cache.(interface {
			GetStats(context.Context) (*HotKeyStats, error)
		}); ok {
			stat, err := statsGetter.GetStats(ctx)
			if err == nil {
				stats[name] = stat
			}
		}
	}

	return stats, nil
}
