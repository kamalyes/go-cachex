/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-26 01:05:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-26 01:05:00
 * @FilePath: \go-cachex\cache_builder.go
 * @Description: 缓存构建器 - 提供链式调用构建复杂缓存策略
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */

package cachex

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// CacheStrategy 缓存策略
type CacheStrategy struct {
	// 基础配置
	Namespace   string
	KeyPattern  string        // key模板,如: "user:{user_id}"
	DefaultTTL  time.Duration // 默认过期时间
	Compression CompressionType

	// 高级功能
	EnableHotKey     bool          // 启用热key保护
	EnableLock       bool          // 启用分布式锁
	EnablePubSub     bool          // 启用发布订阅
	LockTimeout      time.Duration // 锁超时时间
	RefreshThreshold float64       // 刷新阈值(0-1),剩余TTL低于此值时自动刷新

	// 回调函数
	OnCacheMiss func(ctx context.Context, key string) (interface{}, error) // 缓存未命中时的回调
	OnCacheSet  func(ctx context.Context, key string, value interface{})   // 缓存设置后的回调
	OnError     func(ctx context.Context, err error)                       // 错误回调
}

// CacheBuilder 缓存构建器
type CacheBuilder struct {
	strategy    CacheStrategy
	redisClient redis.UniversalClient

	// 组件实例
	cache       interface{}
	lockManager *LockManager
	hotKeyMgr   *HotKeyManager
	pubsub      *PubSub
}

// NewCacheBuilder 创建缓存构建器
func NewCacheBuilder(redisClient redis.UniversalClient, namespace string) *CacheBuilder {
	return &CacheBuilder{
		strategy: CacheStrategy{
			Namespace:        namespace,
			DefaultTTL:       time.Hour,
			Compression:      CompressionNone,
			RefreshThreshold: 0.2, // 剩余20%时自动刷新
		},
		redisClient: redisClient,
	}
}

// WithKeyPattern 设置key模板
func (b *CacheBuilder) WithKeyPattern(pattern string) *CacheBuilder {
	b.strategy.KeyPattern = pattern
	return b
}

// WithTTL 设置默认过期时间
func (b *CacheBuilder) WithTTL(ttl time.Duration) *CacheBuilder {
	b.strategy.DefaultTTL = ttl
	return b
}

// WithCompression 启用压缩
func (b *CacheBuilder) WithCompression(compType CompressionType) *CacheBuilder {
	b.strategy.Compression = compType
	return b
}

// WithHotKey 启用热key保护
func (b *CacheBuilder) WithHotKey() *CacheBuilder {
	b.strategy.EnableHotKey = true
	return b
}

// WithLock 启用分布式锁
func (b *CacheBuilder) WithLock(timeout time.Duration) *CacheBuilder {
	b.strategy.EnableLock = true
	b.strategy.LockTimeout = timeout
	return b
}

// WithPubSub 启用发布订阅
func (b *CacheBuilder) WithPubSub() *CacheBuilder {
	b.strategy.EnablePubSub = true
	return b
}

// WithRefreshThreshold 设置自动刷新阈值
func (b *CacheBuilder) WithRefreshThreshold(threshold float64) *CacheBuilder {
	if threshold > 0 && threshold < 1 {
		b.strategy.RefreshThreshold = threshold
	}
	return b
}

// OnMiss 设置缓存未命中回调(数据加载器)
func (b *CacheBuilder) OnMiss(fn func(ctx context.Context, key string) (interface{}, error)) *CacheBuilder {
	b.strategy.OnCacheMiss = fn
	return b
}

// OnSet 设置缓存设置后回调
func (b *CacheBuilder) OnSet(fn func(ctx context.Context, key string, value interface{})) *CacheBuilder {
	b.strategy.OnCacheSet = fn
	return b
}

// OnError 设置错误回调
func (b *CacheBuilder) OnError(fn func(ctx context.Context, err error)) *CacheBuilder {
	b.strategy.OnError = fn
	return b
}

// Build 构建缓存实例
func (b *CacheBuilder) Build() *SmartCache {
	// 初始化组件
	if b.strategy.EnableLock {
		lockConfig := LockConfig{
			TTL:            b.strategy.LockTimeout,
			RetryInterval:  time.Millisecond * 50,
			MaxRetries:     10,
			Namespace:      b.strategy.Namespace,
			EnableWatchdog: true,
		}
		b.lockManager = NewLockManager(b.redisClient, lockConfig)
	}

	if b.strategy.EnableHotKey {
		hotkeyConfig := HotKeyConfig{
			DefaultTTL:        b.strategy.DefaultTTL,
			RefreshInterval:   time.Minute * 5,
			EnableAutoRefresh: true,
			Namespace:         b.strategy.Namespace,
		}
		b.hotKeyMgr = NewHotKeyManager(b.redisClient, hotkeyConfig)
	}

	if b.strategy.EnablePubSub {
		pubsubConfig := PubSubConfig{
			Namespace:  b.strategy.Namespace,
			MaxRetries: 3,
			RetryDelay: time.Millisecond * 100,
			BufferSize: 100,
		}
		b.pubsub = NewPubSub(b.redisClient, pubsubConfig)
	}

	return &SmartCache{
		strategy:    b.strategy,
		redis:       b.redisClient,
		lockManager: b.lockManager,
		hotKeyMgr:   b.hotKeyMgr,
		pubsub:      b.pubsub,
	}
}

// SmartCache 智能缓存 - 集成多种高级功能
type SmartCache struct {
	strategy    CacheStrategy
	redis       redis.UniversalClient
	lockManager *LockManager
	hotKeyMgr   *HotKeyManager
	pubsub      *PubSub

	// singleflight用于防止缓存击穿
	loadGroup sync.Map // map[string]*singleCall
}

// singleCall 单次调用封装
type singleCall struct {
	wg   sync.WaitGroup
	val  interface{}
	err  error
	done uint32 // 使用原子操作标记完成状态
}

// doCall 执行单次调用(singleflight模式) - 优化版无竞争
func (c *SmartCache) doCall(key string, fn func() (interface{}, error)) (interface{}, error) {
	// 快速路径:检查是否已有进行中的调用
	if v, loaded := c.loadGroup.Load(key); loaded {
		sc := v.(*singleCall)
		sc.wg.Wait()
		return sc.val, sc.err
	}

	// 创建新的调用并预先设置wg
	call := &singleCall{}
	call.wg.Add(1)

	// 尝试存储(竞争条件处理)
	actual, loaded := c.loadGroup.LoadOrStore(key, call)
	if loaded {
		// 其他goroutine已经开始调用,等待结果
		sc := actual.(*singleCall)
		sc.wg.Wait()
		return sc.val, sc.err
	}

	// 当前goroutine负责执行调用
	// 使用defer确保即使panic也能清理
	func() {
		defer func() {
			call.wg.Done()
			// 延迟删除,确保等待的goroutine都能获取结果
			time.AfterFunc(time.Millisecond*10, func() {
				c.loadGroup.Delete(key)
			})
		}()
		call.val, call.err = fn()
	}()

	return call.val, call.err
}

// Get 获取缓存(支持自动加载和刷新)
func (c *SmartCache) Get(ctx context.Context, key string) (interface{}, error) {
	// 1. 尝试从Redis获取
	fullKey := c.buildKey(key)

	val, err := c.redis.Get(ctx, fullKey).Result()
	if err == nil {
		// 缓存命中,检查是否需要刷新
		c.checkAndRefresh(ctx, fullKey, key)
		return val, nil
	}

	if err != redis.Nil {
		return nil, err
	}

	// 2. 缓存未命中,使用回调加载
	if c.strategy.OnCacheMiss == nil {
		return nil, fmt.Errorf("cache miss and no loader provided")
	}

	// 3. 使用singleflight防止缓存击穿(即使没有分布式锁也有效)
	if c.strategy.EnableLock {
		return c.loadWithLock(ctx, key, fullKey)
	}

	// 不使用分布式锁,但仍使用进程内singleflight
	return c.doCall(fullKey, func() (interface{}, error) {
		// 再次检查缓存
		val, err := c.redis.Get(ctx, fullKey).Result()
		if err == nil {
			return val, nil
		}
		return c.loadData(ctx, key, fullKey)
	})
}

// Set 设置缓存
func (c *SmartCache) Set(ctx context.Context, key string, value interface{}, ttl ...time.Duration) error {
	fullKey := c.buildKey(key)
	expiration := c.strategy.DefaultTTL
	if len(ttl) > 0 {
		expiration = ttl[0]
	}

	err := c.redis.Set(ctx, fullKey, value, expiration).Err()
	if err != nil {
		return err
	}

	// 回调通知
	if c.strategy.OnCacheSet != nil {
		c.strategy.OnCacheSet(ctx, key, value)
	}

	// 发布更新事件
	if c.strategy.EnablePubSub {
		c.pubsub.Publish(ctx, "cache.updated", map[string]interface{}{
			"key":   key,
			"value": value,
		})
	}

	return nil
}

// Delete 删除缓存
func (c *SmartCache) Delete(ctx context.Context, key string) error {
	fullKey := c.buildKey(key)

	err := c.redis.Del(ctx, fullKey).Err()
	if err != nil {
		return err
	}

	// 发布删除事件
	if c.strategy.EnablePubSub {
		c.pubsub.Publish(ctx, "cache.deleted", map[string]interface{}{
			"key": key,
		})
	}

	return nil
}

// GetOrSet 获取或设置缓存(简化版) - 使用singleflight优化
func (c *SmartCache) GetOrSet(ctx context.Context, key string, loader func() (interface{}, error)) (interface{}, error) {
	fullKey := c.buildKey(key)

	// 尝试获取
	val, err := c.redis.Get(ctx, fullKey).Result()
	if err == nil {
		return val, nil
	}

	if err != redis.Nil {
		return nil, err
	}

	// 使用singleflight加载数据
	return c.doCall(fullKey, func() (interface{}, error) {
		// 再次检查缓存
		val, err := c.redis.Get(ctx, fullKey).Result()
		if err == nil {
			return val, nil
		}

		// 加载数据
		data, err := loader()
		if err != nil {
			return nil, err
		}

		// 设置缓存
		c.Set(ctx, key, data)

		return data, nil
	})
}

// loadWithLock 使用锁加载数据(防止缓存击穿) - 优化版使用singleflight
func (c *SmartCache) loadWithLock(ctx context.Context, key, fullKey string) (interface{}, error) {
	// 使用singleflight模式,确保同一个key只有一个goroutine执行加载
	return c.doCall(fullKey, func() (interface{}, error) {
		// 再次检查缓存(double-check)
		val, err := c.redis.Get(ctx, fullKey).Result()
		if err == nil {
			return val, nil
		}

		if err != redis.Nil {
			return nil, err
		}

		// 使用分布式锁(可选,如果需要跨进程互斥)
		if c.lockManager != nil {
			lockKey := fmt.Sprintf("lock:%s", fullKey)
			lock := c.lockManager.GetLock(lockKey)

			if err := lock.Lock(ctx); err != nil {
				// 锁获取失败,但可以继续尝试加载(降级策略)
				return c.loadData(ctx, key, fullKey)
			}
			defer lock.Unlock(ctx)

			// 三次检查(在获取分布式锁后)
			val, err := c.redis.Get(ctx, fullKey).Result()
			if err == nil {
				return val, nil
			}
		}

		return c.loadData(ctx, key, fullKey)
	})
}

// loadData 加载数据
func (c *SmartCache) loadData(ctx context.Context, key, fullKey string) (interface{}, error) {
	data, err := c.strategy.OnCacheMiss(ctx, key)
	if err != nil {
		if c.strategy.OnError != nil {
			c.strategy.OnError(ctx, err)
		}
		return nil, err
	}

	// 设置缓存
	c.redis.Set(ctx, fullKey, data, c.strategy.DefaultTTL)

	return data, nil
}

// checkAndRefresh 检查并自动刷新缓存
func (c *SmartCache) checkAndRefresh(ctx context.Context, fullKey, key string) {
	ttl, err := c.redis.TTL(ctx, fullKey).Result()
	if err != nil {
		return
	}

	// 计算剩余时间百分比
	remaining := float64(ttl) / float64(c.strategy.DefaultTTL)

	// 低于阈值时异步刷新
	if remaining < c.strategy.RefreshThreshold && c.strategy.OnCacheMiss != nil {
		go func() {
			freshCtx := context.Background()
			data, err := c.strategy.OnCacheMiss(freshCtx, key)
			if err == nil {
				c.redis.Set(freshCtx, fullKey, data, c.strategy.DefaultTTL)
			}
		}()
	}
}

// buildKey 构建完整key
func (c *SmartCache) buildKey(key string) string {
	if c.strategy.KeyPattern != "" {
		return fmt.Sprintf("%s:%s", c.strategy.Namespace, key)
	}
	return fmt.Sprintf("%s:%s", c.strategy.Namespace, key)
}

// Subscribe 订阅缓存事件
func (c *SmartCache) Subscribe(ctx context.Context, event string, handler func(data interface{})) (*Subscriber, error) {
	if !c.strategy.EnablePubSub {
		return nil, fmt.Errorf("pubsub not enabled")
	}
	// 使用正确的Subscribe API: MessageHandler需要ctx, channel, message参数
	return c.pubsub.Subscribe([]string{event}, func(ctx context.Context, channel string, message string) error {
		handler(message)
		return nil
	})
}

// Close 关闭缓存
func (c *SmartCache) Close() error {
	if c.lockManager != nil {
		c.lockManager.ReleaseAllLocks(context.Background())
	}
	if c.hotKeyMgr != nil {
		c.hotKeyMgr.StopAll()
	}
	if c.pubsub != nil {
		c.pubsub.Close()
	}
	return nil
}
