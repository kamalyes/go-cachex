/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-07-01 00:51:56
 * @FilePath: \go-cachex\kv_cache.go
 * @Description: KVCache —— 通用分布式 K-V 字典缓存
 *
 * ============================================================================
 * 设计目标
 * ============================================================================
 * 提供"高频读、低频写"场景下的分布式字典缓存，解决多节点部署时本地缓存
 * 一致性问题适用 K→V 映射（V 必须可 JSON 序列化），如 id→name、code→config
 *
 * ============================================================================
 * 核心能力（三件套）
 * ============================================================================
 * 1. 三层兜底读取（Get/GetMany）
 *    本地 map[K]V → Redis Hash(HGET/HMGET) → loader 全量预热
 *    读路径：优先命中本地，miss 查 Redis 并回填本地，再 miss 走 loader
 *    加载全量后同步写回 Redis Hash，避免缓存穿透
 *
 * 2. 写后失效广播（Set/SetMany/Delete/DeleteMany/Clear）
 *    写操作同步双写：本地 map + Redis Hash（Lua 脚本保证 HSET+EXPIRE 原子）
 *    写成功后通过 Redis PubSub 广播 invalidate 消息到其他节点：
 *      - 收到消息的节点删除本地对应 key，下次 Get 走 Redis 拿最新值
 *      - 通过 instanceID 区分自己发的消息，避免循环处理
 *      - Refresh（自动刷新/手动预热）不广播，避免周期性刷新引发雪崩
 *
 * 3. pbmo 风格全局注册（RegisterKV/GetKV/MustGetKV）
 *    SetGlobalRedisClient(client) 设置全局 Redis 客户端
 *    RegisterKV[K,V](name, loadFunc, opts...) 一行注册
 *    GetKV[K,V](name) / MustGetKV[K,V](name) 业务侧取用
 *    StopAllKV() 统一关闭所有注册实例
 *
 * ============================================================================
 * 存储结构
 * ============================================================================
 * - Redis: Hash 类型
 *     Key   = {namespace}:{name}          （如 core:kv:game:game_library）
 *     Field = K 的字符串序列化             （string 直接用，int 用 strconv）
 *     Value = V 的 JSON 序列化
 * - 本地: map[K]V + RWMutex
 * - PubSub 频道: {namespace}:{name}:invalidate  （失效广播专用）
 *
 * ============================================================================
 * 适用场景
 * ============================================================================
 * ✅ id→name、code→config 等点查字典（高频读、低频写）
 * ✅ 多节点部署需要本地缓存一致性的 K→V 映射
 * ✅ V 为可 JSON 序列化类型（string/int/struct 等）
 *
 * ============================================================================
 * 不适用场景（请用其他机制）
 * ============================================================================
 * ❌ 需要原子递增的计数器/版本号  → 用 VersionTracker（Redis INCR 语义）
 * ❌ 整体列表响应缓存（带排序/过滤/分页） → 用 CacheWrapper + 索引集合
 * ❌ 缓存不可序列化对象（如 SDK 客户端实例 *Client） → 无法用 KVCache
 * ❌ 强一致事务场景 → KVCache 是最终一致（PubSub 异步失效）
 *
 * ============================================================================
 * 与同库其他缓存组件的边界
 * ============================================================================
 * - VersionTracker：SDK 客户端实例生命周期管理（INCR 哨兵 + 比较 + 重建），非字典缓存
 * - CacheWrapper：单 key 读穿透缓存（func 包装），适合整体响应
 * - CacheIndexManager：基于 Redis Set 索引的批量删除，配合 CacheWrapper 使用
 * - HotKeyCache[K,V]：本地+Redis 双层字典（无 PubSub 广播，单节点失效）
 * - KVCache（本文件）：HotKeyCache 的增强版，新增 PubSub 失效广播 + 全局注册表
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/mathx"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
	"github.com/redis/go-redis/v9"
)

// luaKVSetMany Lua脚本：批量 HSET 并设置 TTL，保证原子性
// KEYS[1] = Redis Hash 键名
// ARGV[1] = TTL（秒）
// ARGV[2..N] = field1 value1 field2 value2 ...
// 返回：写入字段数
var luaKVSetMany = `
local ttl = tonumber(ARGV[1])
local n = 0
for i = 2, #ARGV, 2 do
    redis.call('HSET', KEYS[1], ARGV[i], ARGV[i+1])
    n = n + 1
end
if ttl and ttl > 0 then
    redis.call('EXPIRE', KEYS[1], ttl)
end
return n
`

// KVCacheConfig KV 缓存配置
type KVCacheConfig struct {
	DefaultTTL        time.Duration  // 默认TTL，默认 30 分钟
	RefreshInterval   time.Duration  // 自动刷新间隔，默认 5 分钟
	EnableAutoRefresh bool           // 是否启用自动刷新，默认 true
	Namespace         string         // Redis 命名空间，默认 "kv"
	Logger            logger.ILogger // 日志记录器（可选）
	MaxLocalCacheSize int            // 本地缓存最大条目数，默认 10000
	BatchLoader       any            // 按需批量回源加载器（可选，类型应为 BatchLoader[K,V]）
}

// KVLoader KV 数据加载器：从数据源加载全量 K->V 映射
type KVLoader[K comparable, V any] func(ctx context.Context) (map[K]V, error)

// BatchLoader 按需批量回源加载器：按指定 keys 从数据源精准加载 K->V 映射
// 适用于"全量加载不可行"的场景（如跨服务 RPC 按需查询、大数据量表按主键查）
// 与 KVLoader 互斥可选：两者都配置时，KVLoader 用于 autoRefresh 全量预热，BatchLoader 用于 miss 时精准回源
type BatchLoader[K comparable, V any] func(ctx context.Context, keys []K) (map[K]V, error)

// KVCache 通用键值缓存
//
// 使用方式一（推荐，通过全局注册表）：
//
//	cachex.RegisterKV[string, string]("game_library", loadFunc,
//	    cachex.WithKVNamespace("core:kv"), cachex.WithKVTTL(30*time.Minute))
//	cache, err := cachex.GetKV[string, string]("game_library")
//	cache.Set(ctx, "id1", "value1")
//
// 使用方式二（直接构造）：
//
//	cache := cachex.NewKVCache[string, string](redisClient, "game_library",
//	    loadFunc, cachex.KVCacheConfig{...})
type KVCache[K comparable, V any] struct {
	client          *redis.Client
	config          KVCacheConfig
	loader          KVLoader[K, V]
	batchLoader     BatchLoader[K, V] // 按需批量回源加载器（可选）
	name            string
	mu              sync.RWMutex
	localCache      map[K]V
	lastRefreshTime time.Time
	stopChan        chan struct{}
	once            sync.Once
	logger          logger.ILogger

	// 分布式失效广播
	pubsub       *PubSub
	instanceID   string // 本节点唯一标识，避免处理自己发的消息
	invalidateCh string // PubSub 频道名
}

// invalidateMsg 失效广播消息
type invalidateMsg struct {
	Sender string   `json:"sender"`
	Op     string   `json:"op"`
	Keys   []string `json:"keys,omitempty"`
}

// NewKVCache 创建 KV 缓存实例
func NewKVCache[K comparable, V any](
	client *redis.Client,
	name string,
	loader KVLoader[K, V],
	config KVCacheConfig,
) *KVCache[K, V] {
	config.DefaultTTL = mathx.IfNotZero(config.DefaultTTL, 30*time.Minute)
	config.RefreshInterval = mathx.IfNotZero(config.RefreshInterval, 5*time.Minute)
	config.Namespace = mathx.IfNotEmpty(config.Namespace, "kv")
	config.MaxLocalCacheSize = mathx.IF(config.MaxLocalCacheSize > 0, config.MaxLocalCacheSize, 10000)
	if config.Logger == nil {
		if globalLogger != nil {
			config.Logger = globalLogger
		} else {
			config.Logger = NewDefaultCachexLogger()
		}
	}

	cache := &KVCache[K, V]{
		client:       client,
		config:       config,
		loader:       loader,
		name:         name,
		localCache:   make(map[K]V),
		stopChan:     make(chan struct{}),
		logger:       config.Logger,
		instanceID:   fmt.Sprintf("%d-%d", time.Now().UnixNano(), randomID()),
		invalidateCh: fmt.Sprintf("%s:%s:invalidate", config.Namespace, name),
	}

	// 解析可选的 BatchLoader（按需批量回源加载器）
	if config.BatchLoader != nil {
		if bl, ok := config.BatchLoader.(BatchLoader[K, V]); ok {
			cache.batchLoader = bl
		}
	}

	// 启动自动刷新（仅当存在全量 loader 时才有意义；仅有 BatchLoader 时跳过）
	if config.EnableAutoRefresh && loader != nil {
		syncx.Go().
			OnPanic(func(r interface{}) {
				cache.logger.Errorf("Panic in KVCache autoRefresh: %v", r)
			}).
			Exec(cache.autoRefresh)
	}

	// 启动分布式失效广播订阅
	cache.startInvalidationSubscriber()

	return cache
}

// randomID 生成一个简单的随机 ID（用于 instanceID）
func randomID() int64 {
	return time.Now().UnixNano() ^ int64(uintptr(0xdeadbeef))
}

// startInvalidationSubscriber 启动失效广播订阅
// 收到其他节点的写操作消息后，删除本地对应缓存，下次 Get 走 Redis 拿最新值
func (c *KVCache[K, V]) startInvalidationSubscriber() {
	if c.client == nil {
		return
	}
	c.pubsub = NewPubSub(c.client, PubSubConfig{
		Namespace: "",
		Logger:    c.logger,
	})
	_, err := c.pubsub.Subscribe([]string{c.invalidateCh}, func(ctx context.Context, channel, message string) error {
		var msg invalidateMsg
		if err := json.Unmarshal([]byte(message), &msg); err != nil {
			c.logger.Warnf("KVCache %s invalidation message unmarshal failed: %v", c.name, err)
			return err
		}
		// 忽略自己发的消息（本地缓存在写操作时已更新）
		if msg.Sender == c.instanceID {
			return nil
		}
		c.logger.Debugf("KVCache %s received invalidation: op=%s keys=%v", c.name, msg.Op, msg.Keys)
		switch msg.Op {
		case "clear":
			c.mu.Lock()
			c.localCache = make(map[K]V)
			c.mu.Unlock()
		case "invalidate":
			c.mu.Lock()
			for _, ks := range msg.Keys {
				if _, isString := any(*new(K)).(string); isString {
					if k, ok := any(ks).(K); ok {
						delete(c.localCache, k)
					}
				}
			}
			c.mu.Unlock()
		}
		return nil
	})
	if err != nil {
		c.logger.Warnf("KVCache %s subscribe invalidation failed: %v", c.name, err)
	}
}

// publishInvalidation 广播失效消息给其他节点
func (c *KVCache[K, V]) publishInvalidation(op string, keys []string) {
	if c.pubsub == nil {
		return
	}
	msg := invalidateMsg{Sender: c.instanceID, Op: op, Keys: keys}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c.logger.Debugf("KVCache %s publish invalidation: op=%s keys=%v", c.name, op, keys)
	if err := c.pubsub.Publish(ctx, c.invalidateCh, string(data)); err != nil {
		c.logger.Warnf("KVCache %s publish invalidation failed: %v", c.name, err)
	}
}

// redisKey 获取 Redis Hash 键名
func (c *KVCache[K, V]) redisKey() string {
	return fmt.Sprintf("%s:%s", c.config.Namespace, c.name)
}

// encodeKey 将 K 序列化为 Redis Hash field 字符串
func (c *KVCache[K, V]) encodeKey(k K) string {
	switch v := any(k).(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// encodeValue 将 V 序列化为 Redis Hash value 字符串
func (c *KVCache[K, V]) encodeValue(v V) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to marshal kv value: %w", err)
	}
	return string(data), nil
}

// decodeValue 反序列化为 V
func (c *KVCache[K, V]) decodeValue(s string) (V, error) {
	var zero V
	var v V
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return zero, fmt.Errorf("failed to unmarshal kv value: %w", err)
	}
	return v, nil
}

// Get 获取单个值（本地 -> Redis -> BatchLoader 按需回源 / LoadAll 全量兜底）
func (c *KVCache[K, V]) Get(ctx context.Context, key K) (V, bool, error) {
	var zero V

	// 1. 本地缓存
	c.mu.RLock()
	if v, ok := c.localCache[key]; ok {
		c.mu.RUnlock()
		c.logger.Debugf("KVCache %s Get local hit: key=%v", c.name, key)
		return v, true, nil
	}
	c.mu.RUnlock()

	// 2. Redis Hash 单字段查询
	s, err := c.client.HGet(ctx, c.redisKey(), c.encodeKey(key)).Result()
	if err == nil {
		if v, err := c.decodeValue(s); err == nil {
			// 回填本地缓存
			c.mu.Lock()
			if c.localCache == nil {
				c.localCache = make(map[K]V)
			}
			c.localCache[key] = v
			c.mu.Unlock()
			c.logger.Debugf("KVCache %s Get redis hit: key=%v", c.name, key)
			return v, true, nil
		}
	} else if err != redis.Nil {
		c.logger.Warnf("KVCache %s HGet failed: %v", c.name, err)
	}

	// 3. miss 回源：优先 BatchLoader 按需回源，无 BatchLoader 时触发 LoadAll 全量兜底
	if c.batchLoader != nil {
		c.logger.Debugf("KVCache %s Get miss, batchLoading: key=%v", c.name, key)
		loaded, lerr := c.batchLoader(ctx, []K{key})
		if lerr != nil {
			return zero, false, fmt.Errorf("KVCache %s batchLoader failed: %w", c.name, lerr)
		}
		if v, ok := loaded[key]; ok {
			c.mu.Lock()
			c.localCache[key] = v
			c.mu.Unlock()
			_ = c.writeManyToRedis(ctx, loaded)
			return v, true, nil
		}
		return zero, false, nil
	}

	c.logger.Debugf("KVCache %s Get miss, triggering LoadAll: key=%v", c.name, key)
	if _, err := c.LoadAll(ctx); err != nil {
		return zero, false, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.localCache[key]
	return v, ok, nil
}

// GetMany 批量获取多个 key 的值（缺失的 key 不出现在返回 map 中）
//
// 查询路径：本地缓存 -> Redis HMGET -> BatchLoader 按需回源（可选）
// 当配置了 BatchLoader 时，Redis 也 miss 的 key 会按需精准回源（而非全量 LoadAll）
func (c *KVCache[K, V]) GetMany(ctx context.Context, keys []K) (map[K]V, error) {
	if len(keys) == 0 {
		return make(map[K]V), nil
	}

	// 先从本地拿
	result := make(map[K]V, len(keys))
	missed := make([]K, 0, len(keys))
	c.mu.RLock()
	for _, k := range keys {
		if v, ok := c.localCache[k]; ok {
			result[k] = v
		} else {
			missed = append(missed, k)
		}
	}
	c.mu.RUnlock()

	if len(missed) == 0 {
		return result, nil
	}

	// Redis HMGET 批量查询
	fields := make([]string, len(missed))
	for i, k := range missed {
		fields[i] = c.encodeKey(k)
	}
	values, err := c.client.HMGet(ctx, c.redisKey(), fields...).Result()
	if err != nil && err != redis.Nil {
		// Redis 不可用时的兜底策略
		if c.batchLoader != nil {
			// 有 BatchLoader，按缺失 keys 精准回源
			return c.batchLoadAndFill(ctx, result, missed)
		}
		// 无 BatchLoader，触发全量预热兜底
		if _, lerr := c.LoadAll(ctx); lerr == nil {
			c.mu.RLock()
			for _, k := range missed {
				if v, ok := c.localCache[k]; ok {
					result[k] = v
				}
			}
			c.mu.RUnlock()
			return result, nil
		}
		return nil, fmt.Errorf("KVCache %s HMGet failed: %w", c.name, err)
	}

	// 回填本地缓存并合并 Redis 命中的结果，同时收集 Redis 也 miss 的 key
	stillMissed := make([]K, 0, len(missed))
	c.mu.Lock()
	for i, val := range values {
		if val == nil {
			stillMissed = append(stillMissed, missed[i])
			continue
		}
		s, ok := val.(string)
		if !ok {
			stillMissed = append(stillMissed, missed[i])
			continue
		}
		if v, derr := c.decodeValue(s); derr == nil {
			c.localCache[missed[i]] = v
			result[missed[i]] = v
		} else {
			stillMissed = append(stillMissed, missed[i])
		}
	}
	c.mu.Unlock()

	// Redis 也 miss 的 key，如果有 BatchLoader，按需精准回源
	if len(stillMissed) > 0 && c.batchLoader != nil {
		return c.batchLoadAndFill(ctx, result, stillMissed)
	}

	return result, nil
}

// batchLoadAndFill 调用 BatchLoader 按需回源，回填本地+Redis 缓存，合并到 result
func (c *KVCache[K, V]) batchLoadAndFill(ctx context.Context, result map[K]V, keys []K) (map[K]V, error) {
	loaded, err := c.batchLoader(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("KVCache %s batchLoader failed: %w", c.name, err)
	}

	// 回填本地缓存并合并到结果
	c.mu.Lock()
	for k, v := range loaded {
		c.localCache[k] = v
		result[k] = v
	}
	c.mu.Unlock()

	// 预热 Redis（不广播失效——这是 miss 回源，避免雪崩）
	if len(loaded) > 0 {
		_ = c.writeManyToRedis(ctx, loaded)
	}

	c.logger.Debugf("KVCache %s batchLoad: requested=%d loaded=%d", c.name, len(keys), len(loaded))
	return result, nil
}

// Set 设置单个值（同步双写本地 + Redis，并广播失效消息给其他节点）
func (c *KVCache[K, V]) Set(ctx context.Context, key K, value V) error {
	// 写本地
	c.mu.Lock()
	c.localCache[key] = value
	c.mu.Unlock()

	// 写 Redis
	vstr, err := c.encodeValue(value)
	if err != nil {
		return err
	}
	ttl := int64(c.config.DefaultTTL / time.Second)
	// 使用 Lua 脚本保证 HSET + EXPIRE 原子
	if err := c.client.Eval(ctx, luaKVSetMany,
		[]string{c.redisKey()},
		ttl, c.encodeKey(key), vstr,
	).Err(); err != nil {
		return err
	}

	// 广播失效消息给其他节点（异步不阻塞）
	c.publishInvalidation("invalidate", []string{c.encodeKey(key)})
	c.logger.Debugf("KVCache %s Set: key=%v", c.name, key)
	return nil
}

// SetMany 批量设置多个值（Lua 脚本一次 RTT 完成 HSET + EXPIRE，并广播失效消息）
func (c *KVCache[K, V]) SetMany(ctx context.Context, items map[K]V) error {
	if len(items) == 0 {
		return nil
	}

	// 写本地
	c.mu.Lock()
	for k, v := range items {
		c.localCache[k] = v
	}
	c.mu.Unlock()

	// 写 Redis（Lua 批量）
	args := make([]interface{}, 0, len(items)*2+1)
	args = append(args, int64(c.config.DefaultTTL/time.Second))
	keys := make([]string, 0, len(items))
	for k, v := range items {
		vstr, err := c.encodeValue(v)
		if err != nil {
			return err
		}
		ks := c.encodeKey(k)
		args = append(args, ks, vstr)
		keys = append(keys, ks)
	}
	if err := c.client.Eval(ctx, luaKVSetMany,
		[]string{c.redisKey()},
		args...,
	).Err(); err != nil {
		return err
	}

	// 广播失效消息
	c.publishInvalidation("invalidate", keys)
	c.logger.Debugf("KVCache %s SetMany: count=%d", c.name, len(items))
	return nil
}

// Delete 删除单个 key（并广播失效消息）
func (c *KVCache[K, V]) Delete(ctx context.Context, key K) error {
	c.mu.Lock()
	delete(c.localCache, key)
	c.mu.Unlock()

	if err := c.client.HDel(ctx, c.redisKey(), c.encodeKey(key)).Err(); err != nil {
		return err
	}
	c.publishInvalidation("invalidate", []string{c.encodeKey(key)})
	c.logger.Debugf("KVCache %s Delete: key=%v", c.name, key)
	return nil
}

// DeleteMany 批量删除多个 key（HDEL 单次 RTT，并广播失效消息）
func (c *KVCache[K, V]) DeleteMany(ctx context.Context, keys []K) error {
	if len(keys) == 0 {
		return nil
	}

	c.mu.Lock()
	for _, k := range keys {
		delete(c.localCache, k)
	}
	c.mu.Unlock()

	fields := make([]string, len(keys))
	for i, k := range keys {
		fields[i] = c.encodeKey(k)
	}
	if err := c.client.HDel(ctx, c.redisKey(), fields...).Err(); err != nil {
		return err
	}
	c.publishInvalidation("invalidate", fields)
	c.logger.Debugf("KVCache %s DeleteMany: count=%d", c.name, len(keys))
	return nil
}

// LoadAll 从 Redis 或数据源加载全量数据
//
// 说明：Redis Hash field 永远是 string，因此仅当 K 为 string 时支持从 Redis 全量恢复；
// 其他 K 类型（如 int64）请由调用方在 loader 中自行转换为 string 主键，
// 或直接由 loader 重建（推荐做法，与项目 Repository.LoadNameMap 模式一致）
func (c *KVCache[K, V]) LoadAll(ctx context.Context) (map[K]V, error) {
	if _, isString := any(*new(K)).(string); isString {
		// K 为 string，可直接从 Redis Hash 全量恢复
		all, err := c.client.HGetAll(ctx, c.redisKey()).Result()
		if err == nil && len(all) > 0 {
			data := make(map[K]V, len(all))
			for k, v := range all {
				kk, ok := any(k).(K)
				if !ok {
					continue
				}
				vv, derr := c.decodeValue(v)
				if derr != nil {
					continue
				}
				data[kk] = vv
			}
			if len(data) > 0 {
				c.mu.Lock()
				c.localCache = make(map[K]V, len(data))
				for k, v := range data {
					c.localCache[k] = v
				}
				c.lastRefreshTime = time.Now()
				c.mu.Unlock()
				return data, nil
			}
		}
	}
	// K 非 string 或 Redis 为空，从数据源加载
	return c.loadFromSource(ctx)
}

// loadFromSource 从数据源加载并预热 Redis
func (c *KVCache[K, V]) loadFromSource(ctx context.Context) (map[K]V, error) {
	if c.loader == nil {
		return nil, fmt.Errorf("KVCache %s has no loader", c.name)
	}

	data, err := c.loader(ctx)
	if err != nil {
		return nil, fmt.Errorf("KVCache %s loader failed: %w", c.name, err)
	}

	// 回填本地缓存
	c.mu.Lock()
	c.localCache = make(map[K]V, len(data))
	for k, v := range data {
		c.localCache[k] = v
	}
	c.lastRefreshTime = time.Now()
	c.mu.Unlock()

	// 预热 Redis（仅当数据非空时写，不广播失效——Refresh 是本节点兜底，避免雪崩）
	if len(data) > 0 {
		_ = c.writeManyToRedis(ctx, data)
	}

	c.logger.Debugf("KVCache %s loadFromSource: count=%d", c.name, len(data))
	return data, nil
}

// writeManyToRedis 仅写 Redis 不广播失效（供 Refresh/loadFromSource 预热使用，避免雪崩）
func (c *KVCache[K, V]) writeManyToRedis(ctx context.Context, items map[K]V) error {
	if len(items) == 0 {
		return nil
	}
	args := make([]interface{}, 0, len(items)*2+1)
	args = append(args, int64(c.config.DefaultTTL/time.Second))
	for k, v := range items {
		vstr, err := c.encodeValue(v)
		if err != nil {
			return err
		}
		args = append(args, c.encodeKey(k), vstr)
	}
	return c.client.Eval(ctx, luaKVSetMany,
		[]string{c.redisKey()},
		args...,
	).Err()
}

// Refresh 手动刷新缓存（从数据源重新加载，仅更新本节点 + Redis，不广播）
func (c *KVCache[K, V]) Refresh(ctx context.Context) error {
	c.logger.Debugf("KVCache %s Refresh", c.name)
	_, err := c.loadFromSource(ctx)
	return err
}

// Clear 清空本地与 Redis 缓存（并广播 clear 消息给其他节点）
func (c *KVCache[K, V]) Clear(ctx context.Context) error {
	c.mu.Lock()
	c.localCache = make(map[K]V)
	c.lastRefreshTime = time.Now()
	c.mu.Unlock()

	if err := c.client.Del(ctx, c.redisKey()).Err(); err != nil {
		return err
	}
	c.publishInvalidation("clear", nil)
	c.logger.Debugf("KVCache %s Clear", c.name)
	return nil
}

// Size 返回本地缓存大小
func (c *KVCache[K, V]) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.localCache)
}

// autoRefresh 自动刷新协程
func (c *KVCache[K, V]) autoRefresh() {
	ticker := time.NewTicker(c.config.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if err := c.Refresh(ctx); err != nil {
				c.logger.Warnf("KVCache %s auto refresh failed: %v", c.name, err)
			}
			cancel()
		case <-c.stopChan:
			return
		}
	}
}

// Stop 停止自动刷新与 PubSub 订阅
func (c *KVCache[K, V]) Stop() {
	c.once.Do(func() {
		close(c.stopChan)
		if c.pubsub != nil {
			_ = c.pubsub.Close()
		}
	})
}

// ============================================================
// 全局注册表（pbmo 风格 Register / Get）
// ============================================================

var (
	// globalRedisClient 全局 Redis 客户端，由宿主服务在启动阶段通过 SetGlobalRedisClient 注入
	globalRedisClient *redis.Client

	// globalLogger 全局日志记录器，由宿主服务通过 SetLogger 注入
	// 所有后续创建的 KVCache 实例若未单独指定 Logger，将复用此日志器
	globalLogger logger.ILogger

	// kvRegistry 全局 KV 缓存注册表（按 name 索引）
	kvRegistry   = make(map[string]any)
	kvRegistryMu sync.RWMutex
)

// SetGlobalRedisClient 注入全局 Redis 客户端
// 必须在调用 RegisterKV 之前完成注入，否则 RegisterKV 将 panic
func SetGlobalRedisClient(client *redis.Client) {
	globalRedisClient = client
}

// SetLogger 注入全局日志记录器
// 必须在 RegisterKV 之前调用，所有后续注册的 KVCache 实例将复用此日志器
// 未调用时使用默认日志器 NewDefaultCachexLogger()
func SetLogger(l logger.ILogger) {
	globalLogger = l
}

// RegisterKV 注册 KV 缓存（pbmo 风格）
//
// 使用者只需在初始化阶段调用一次，后续可通过 GetKV[K,V](name) 获取实例
//
//	cachex.SetGlobalRedisClient(gwglobal.REDIS)
//	cachex.RegisterKV[string, string]("game_library", repo.GameLibrary.LoadNameMap,
//	    cachex.WithKVNamespace("core:kv"))
//
// 注意：name 必须全局唯一，重复注册将 panic
func RegisterKV[K comparable, V any](
	name string,
	loader KVLoader[K, V],
	opts ...KVOption,
) {
	if globalRedisClient == nil {
		panic("cachex: global redis client not set, call SetGlobalRedisClient first")
	}
	kvRegistryMu.Lock()
	defer kvRegistryMu.Unlock()
	if _, exists := kvRegistry[name]; exists {
		panic(fmt.Sprintf("cachex: kv cache %q already registered", name))
	}
	cfg := KVCacheConfig{
		DefaultTTL:        30 * time.Minute,
		RefreshInterval:   5 * time.Minute,
		EnableAutoRefresh: true,
		Namespace:         "kv",
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	kvRegistry[name] = NewKVCache[K, V](globalRedisClient, name, loader, cfg)
}

// GetKV 按名称获取已注册的 KV 缓存
//
//	cache, err := cachex.GetKV[string, string]("game_library")
//	cache.Set(ctx, "id1", "name1")
func GetKV[K comparable, V any](name string) (*KVCache[K, V], error) {
	kvRegistryMu.RLock()
	c, ok := kvRegistry[name]
	kvRegistryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("cachex: kv cache %q not registered", name)
	}
	typed, ok := c.(*KVCache[K, V])
	if !ok {
		return nil, fmt.Errorf("cachex: kv cache %q type mismatch", name)
	}
	return typed, nil
}

// MustGetKV 按名称获取已注册的 KV 缓存，未注册时 panic（适合初始化阶段使用）
func MustGetKV[K comparable, V any](name string) *KVCache[K, V] {
	cache, err := GetKV[K, V](name)
	if err != nil {
		panic(err)
	}
	return cache
}

// StopAllKV 停止所有已注册 KV 缓存的自动刷新（服务关闭时调用）
func StopAllKV() {
	kvRegistryMu.RLock()
	defer kvRegistryMu.RUnlock()
	for _, c := range kvRegistry {
		if stopper, ok := c.(interface{ Stop() }); ok {
			stopper.Stop()
		}
	}
}

// KVOption KV 缓存配置项
type KVOption func(*KVCacheConfig)

// WithKVTTL 设置默认 TTL
func WithKVTTL(ttl time.Duration) KVOption {
	return func(c *KVCacheConfig) { c.DefaultTTL = ttl }
}

// WithKVRefreshInterval 设置自动刷新间隔
func WithKVRefreshInterval(d time.Duration) KVOption {
	return func(c *KVCacheConfig) { c.RefreshInterval = d }
}

// WithKVAutoRefresh 是否启用自动刷新
func WithKVAutoRefresh(enable bool) KVOption {
	return func(c *KVCacheConfig) { c.EnableAutoRefresh = enable }
}

// WithKVNamespace 设置 Redis 命名空间
func WithKVNamespace(ns string) KVOption {
	return func(c *KVCacheConfig) { c.Namespace = ns }
}

// WithKVLogger 设置日志记录器
func WithKVLogger(l logger.ILogger) KVOption {
	return func(c *KVCacheConfig) { c.Logger = l }
}

// WithKVMaxLocalCacheSize 设置本地缓存最大条目数
func WithKVMaxLocalCacheSize(n int) KVOption {
	return func(c *KVCacheConfig) { c.MaxLocalCacheSize = n }
}

// WithKVBatchLoader 设置按需批量回源加载器
// 配置后，GetMany 在本地+Redis 都 miss 时按缺失 keys 精准回源（而非全量 LoadAll）
// 适用于"全量加载不可行"的场景（如跨服务 RPC 按需查询、大数据量表按主键查）
//
//	cachex.RegisterKV[string, string]("user_nickname", nil,
//	    cachex.WithKVBatchLoader[string, string](func(ctx, keys) (map[string]string, error) {
//	        // 调用 RPC 批量查询，返回 user_id -> nickname 映射
//	    }),
//	    cachex.WithKVAutoRefresh(false),  // 仅有 BatchLoader 时关闭自动刷新
//	)
func WithKVBatchLoader[K comparable, V any](loader BatchLoader[K, V]) KVOption {
	return func(c *KVCacheConfig) { c.BatchLoader = loader }
}
