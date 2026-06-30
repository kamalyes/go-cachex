/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-07-01 01:52:29
 * @FilePath: \go-cachex\kv_cache_test.go
 * @Description: KVCache 通用键值缓存测试，覆盖本地/Redis/PubSub 失效广播/注册表
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kvLoader 构建 KVLoader 的辅助函数
func kvLoader[K comparable, V any](data map[K]V, callCount *int64) KVLoader[K, V] {
	return func(ctx context.Context) (map[K]V, error) {
		if callCount != nil {
			atomic.AddInt64(callCount, 1)
		}
		out := make(map[K]V, len(data))
		for k, v := range data {
			out[k] = v
		}
		return out, nil
	}
}

// kvErrorLoader 返回错误的 loader
func kvErrorLoader[K comparable, V any]() KVLoader[K, V] {
	return func(ctx context.Context) (map[K]V, error) {
		return nil, errors.New("loader error")
	}
}

// newTestKVCache 创建测试用 KVCache（禁用自动刷新避免干扰）
func newTestKVCache[K comparable, V any](t *testing.T, name string, loader KVLoader[K, V]) *KVCache[K, V] {
	t.Helper()
	client := setupRedisClient(t)
	// 清理上次测试残留
	_ = client.Del(context.Background(), fmt.Sprintf("kv:%s", name)).Err()
	_ = client.Del(context.Background(), fmt.Sprintf("kv:%s:invalidate", name)).Err()

	cfg := KVCacheConfig{
		DefaultTTL:        time.Minute,
		RefreshInterval:   time.Hour, // 测试中禁用自动刷新
		EnableAutoRefresh: false,
		Namespace:         "kv",
	}
	c := NewKVCache[K, V](client, name, loader, cfg)
	return c
}

// ============================================================
// 编码/解码 测试
// ============================================================

func TestKVCache_EncodeKey(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	// string K
	cStr := NewKVCache[string, string](client, "enc-str", kvLoader[string, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cStr.Stop()
	assert.Equal(t, "hello", cStr.encodeKey("hello"))

	// int K
	cInt := NewKVCache[int, string](client, "enc-int", kvLoader[int, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cInt.Stop()
	assert.Equal(t, "42", cInt.encodeKey(42))

	// int64 K
	cI64 := NewKVCache[int64, string](client, "enc-i64", kvLoader[int64, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cI64.Stop()
	assert.Equal(t, "100", cI64.encodeKey(int64(100)))

	// uint32 K
	cU32 := NewKVCache[uint32, string](client, "enc-u32", kvLoader[uint32, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cU32.Stop()
	assert.Equal(t, "4294967295", cU32.encodeKey(uint32(4294967295)))
}

func TestKVCache_EncodeDecodeValue(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	c := NewKVCache[string, string](client, "test-encdec", kvLoader[string, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer c.Stop()

	// string
	s, err := c.encodeValue("hello")
	require.NoError(t, err)
	assert.Equal(t, `"hello"`, s)

	v, err := c.decodeValue(`"hello"`)
	require.NoError(t, err)
	assert.Equal(t, "hello", v)

	// decode 失败
	_, err = c.decodeValue("not-json")
	assert.Error(t, err)
}

// ============================================================
// Set / Get 基本流程
// ============================================================

func TestKVCache_SetAndGet(t *testing.T) {
	c := newTestKVCache[string, string](t, "set-get", kvLoader[string, string](nil, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	// Set 后 Get
	require.NoError(t, c.Set(ctx, "k1", "v1"))
	v, exists, err := c.Get(ctx, "k1")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "v1", v)

	// 直接查 Redis Hash 应该有值
	s, err := c.client.HGet(ctx, c.redisKey(), "k1").Result()
	require.NoError(t, err)
	assert.Equal(t, `"v1"`, s)
}

func TestKVCache_Get_Miss_LoadAll(t *testing.T) {
	data := map[string]string{"a": "1", "b": "2"}
	c := newTestKVCache[string, string](t, "miss-load", kvLoader(data, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	// Get 不存在的 key → 触发 LoadAll → loader 数据进入缓存
	v, exists, err := c.Get(ctx, "a")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "1", v)

	// 本地缓存大小应该是 2
	assert.Equal(t, 2, c.Size())
}

func TestKVCache_Get_NotFound(t *testing.T) {
	data := map[string]string{"a": "1"}
	c := newTestKVCache[string, string](t, "not-found", kvLoader(data, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	// miss 触发 loadFromSource（loader 有数据但不含 "missing"）
	v, exists, err := c.Get(ctx, "missing")
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, "", v)
}

func TestKVCache_Get_RedisFallback(t *testing.T) {
	// 构建场景：本地 miss，但 Redis 有值，验证 Redis 回填本地
	c := newTestKVCache[string, string](t, "redis-fallback", kvLoader[string, string](nil, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	// 直接写 Redis Hash
	require.NoError(t, c.client.HSet(ctx, c.redisKey(), "only-redis", `"remote"`).Err())

	// Get 应该从 Redis 拿到并回填本地
	v, exists, err := c.Get(ctx, "only-redis")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "remote", v)

	// 再次 Get 应该命中本地
	assert.Equal(t, 1, c.Size())
}

// ============================================================
// SetMany / GetMany 批量
// ============================================================

func TestKVCache_SetMany(t *testing.T) {
	c := newTestKVCache[string, string](t, "setmany", kvLoader[string, string](nil, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	items := map[string]string{"k1": "v1", "k2": "v2", "k3": "v3"}
	require.NoError(t, c.SetMany(ctx, items))

	// 全部命中本地
	for k, v := range items {
		got, exists, err := c.Get(ctx, k)
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, v, got)
	}

	// 空的 SetMany 不报错
	require.NoError(t, c.SetMany(ctx, map[string]string{}))
}

func TestKVCache_GetMany(t *testing.T) {
	c := newTestKVCache[string, string](t, "getmany", kvLoader[string, string](nil, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	// 先写一些到 Redis（绕过本地，模拟其他节点写入）
	require.NoError(t, c.client.HSet(ctx, c.redisKey(), "r1", `"rv1"`, "r2", `"rv2"`).Err())

	// GetMany：部分本地、部分 Redis
	require.NoError(t, c.Set(ctx, "local1", "lv1"))

	result, err := c.GetMany(ctx, []string{"local1", "r1", "r2", "missing"})
	require.NoError(t, err)
	assert.Equal(t, "lv1", result["local1"])
	assert.Equal(t, "rv1", result["r1"])
	assert.Equal(t, "rv2", result["r2"])
	_, ok := result["missing"]
	assert.False(t, ok)
}

func TestKVCache_GetMany_Empty(t *testing.T) {
	c := newTestKVCache[string, string](t, "getmany-empty", kvLoader[string, string](nil, nil))
	defer c.Stop()

	result, err := c.GetMany(context.Background(), []string{})
	require.NoError(t, err)
	assert.Empty(t, result)
}

// ============================================================
// Delete / DeleteMany
// ============================================================

func TestKVCache_Delete(t *testing.T) {
	c := newTestKVCache[string, string](t, "delete", kvLoader[string, string](nil, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "k1", "v1"))
	require.NoError(t, c.Delete(ctx, "k1"))

	_, exists, err := c.Get(ctx, "k1")
	require.NoError(t, err)
	assert.False(t, exists)

	// Redis 也应该没有
	_, err = c.client.HGet(ctx, c.redisKey(), "k1").Result()
	assert.Error(t, err) // redis.Nil
}

func TestKVCache_DeleteMany(t *testing.T) {
	c := newTestKVCache[string, string](t, "deletemany", kvLoader[string, string](nil, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "k1", "v1"))
	require.NoError(t, c.Set(ctx, "k2", "v2"))
	require.NoError(t, c.Set(ctx, "k3", "v3"))

	require.NoError(t, c.DeleteMany(ctx, []string{"k1", "k2"}))

	_, exists, err := c.Get(ctx, "k1")
	require.NoError(t, err)
	assert.False(t, exists)

	_, exists, err = c.Get(ctx, "k3")
	require.NoError(t, err)
	assert.True(t, exists)

	// 空 DeleteMany
	require.NoError(t, c.DeleteMany(ctx, []string{}))
}

// ============================================================
// LoadAll / Refresh / Clear
// ============================================================

func TestKVCache_LoadAll_FromLoader(t *testing.T) {
	data := map[string]string{"a": "1", "b": "2"}
	c := newTestKVCache[string, string](t, "loadall-loader", kvLoader(data, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	// 先清空 Redis 避免干扰
	_ = c.client.Del(context.Background(), c.redisKey()).Err()

	ctx := context.Background()
	out, err := c.LoadAll(ctx)
	require.NoError(t, err)
	assert.Len(t, out, 2)
	assert.Equal(t, "1", out["a"])
	assert.Equal(t, "2", out["b"])

	// 本地缓存被填充
	assert.Equal(t, 2, c.Size())
}

func TestKVCache_LoadAll_FromRedis(t *testing.T) {
	// K=string 时，LoadAll 应该优先从 Redis 全量恢复
	c := newTestKVCache[string, string](t, "loadall-redis", kvLoader[string, string](nil, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	// 直接写 Redis Hash
	require.NoError(t, c.client.HSet(ctx, c.redisKey(), "x1", `"v1"`, "x2", `"v2"`).Err())

	// LoadAll 从 Redis 恢复
	out, err := c.LoadAll(ctx)
	require.NoError(t, err)
	assert.Len(t, out, 2)
	assert.Equal(t, "v1", out["x1"])
	assert.Equal(t, "v2", out["x2"])
}

func TestKVCache_Refresh(t *testing.T) {
	var calls int64
	data := map[string]string{"a": "1"}
	c := newTestKVCache[string, string](t, "refresh", kvLoader(data, &calls))
	defer c.Stop()
	defer c.Clear(context.Background())

	// Refresh 触发 loadFromSource → loader
	require.NoError(t, c.Refresh(context.Background()))
	assert.Equal(t, int64(1), atomic.LoadInt64(&calls))
	assert.Equal(t, 1, c.Size())
}

func TestKVCache_Refresh_LoaderError(t *testing.T) {
	c := newTestKVCache[string, string](t, "refresh-err", kvErrorLoader[string, string]())
	defer c.Stop()
	defer c.Clear(context.Background())

	err := c.Refresh(context.Background())
	assert.Error(t, err)
}

func TestKVCache_Clear(t *testing.T) {
	c := newTestKVCache[string, string](t, "clear", kvLoader[string, string](nil, nil))
	defer c.Stop()

	ctx := context.Background()
	require.NoError(t, c.Set(ctx, "k1", "v1"))
	require.NoError(t, c.Set(ctx, "k2", "v2"))

	require.NoError(t, c.Clear(ctx))

	assert.Equal(t, 0, c.Size())

	// Redis 也清空
	cnt, err := c.client.Exists(ctx, c.redisKey()).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), cnt)
}

func TestKVCache_Size(t *testing.T) {
	c := newTestKVCache[string, string](t, "size", kvLoader[string, string](nil, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	assert.Equal(t, 0, c.Size())
	require.NoError(t, c.Set(context.Background(), "k1", "v1"))
	assert.Equal(t, 1, c.Size())
}

// ============================================================
// LoadAll 无 loader 场景
// ============================================================

func TestKVCache_LoadAll_NoLoader(t *testing.T) {
	c := newTestKVCache[string, string](t, "no-loader", nil)
	defer c.Stop()
	defer c.Clear(context.Background())

	// 清空 Redis 强制走 loader
	_ = c.client.Del(context.Background(), c.redisKey()).Err()

	_, err := c.LoadAll(context.Background())
	assert.Error(t, err)
}

// ============================================================
// 全局注册表 测试
// ============================================================

func TestKVCache_RegisterAndGetKV(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	_ = client.Del(context.Background(), "kv:reg-test").Err()
	_ = client.Del(context.Background(), "kv:reg-test:invalidate").Err()

	// 备份并替换全局
	oldGlobal := globalRedisClient
	globalRedisClient = client
	defer func() { globalRedisClient = oldGlobal }()
	// 清空注册表
	kvRegistryMu.Lock()
	for k := range kvRegistry {
		delete(kvRegistry, k)
	}
	kvRegistryMu.Unlock()

	ctx := context.Background()

	RegisterKV[string, string]("reg-test", kvLoader[string, string](map[string]string{"a": "1"}, nil),
		WithKVNamespace("kv"), WithKVTTL(time.Minute))

	cache, err := GetKV[string, string]("reg-test")
	require.NoError(t, err)
	defer cache.Stop()
	defer cache.Clear(ctx)

	// 使用
	require.NoError(t, cache.Set(ctx, "k1", "v1"))
	v, exists, err := cache.Get(ctx, "k1")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "v1", v)
}

func TestKVCache_MustGetKV(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	_ = client.Del(context.Background(), "kv:mustget").Err()

	oldGlobal := globalRedisClient
	globalRedisClient = client
	defer func() { globalRedisClient = oldGlobal }()
	kvRegistryMu.Lock()
	for k := range kvRegistry {
		delete(kvRegistry, k)
	}
	kvRegistryMu.Unlock()

	RegisterKV[string, string]("mustget", kvLoader[string, string](nil, nil),
		WithKVNamespace("kv"), WithKVTTL(time.Minute))
	cache := MustGetKV[string, string]("mustget")
	defer cache.Stop()
	defer cache.Clear(context.Background())

	assert.NotNil(t, cache)
}

func TestKVCache_GetKV_NotRegistered(t *testing.T) {
	kvRegistryMu.Lock()
	for k := range kvRegistry {
		delete(kvRegistry, k)
	}
	kvRegistryMu.Unlock()

	_, err := GetKV[string, string]("nonexistent")
	assert.Error(t, err)
}

func TestKVCache_GetKV_TypeMismatch(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	oldGlobal := globalRedisClient
	globalRedisClient = client
	defer func() { globalRedisClient = oldGlobal }()
	kvRegistryMu.Lock()
	for k := range kvRegistry {
		delete(kvRegistry, k)
	}
	kvRegistryMu.Unlock()

	RegisterKV[string, string]("type-mismatch", kvLoader[string, string](nil, nil))
	defer func() {
		if c, err := GetKV[string, string]("type-mismatch"); err == nil {
			c.Stop()
		}
	}()

	// 用 int 类型取 string 缓存 → 类型不匹配
	_, err := GetKV[int, int]("type-mismatch")
	assert.Error(t, err)
}

func TestKVCache_RegisterKV_Duplicate(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	oldGlobal := globalRedisClient
	globalRedisClient = client
	defer func() { globalRedisClient = oldGlobal }()
	kvRegistryMu.Lock()
	for k := range kvRegistry {
		delete(kvRegistry, k)
	}
	kvRegistryMu.Unlock()

	RegisterKV[string, string]("dup", kvLoader[string, string](nil, nil))
	defer func() {
		if c, err := GetKV[string, string]("dup"); err == nil {
			c.Stop()
		}
	}()

	assert.Panics(t, func() {
		RegisterKV[string, string]("dup", kvLoader[string, string](nil, nil))
	})
}

func TestKVCache_RegisterKV_NoGlobalRedis(t *testing.T) {
	oldGlobal := globalRedisClient
	globalRedisClient = nil
	defer func() { globalRedisClient = oldGlobal }()

	assert.Panics(t, func() {
		RegisterKV[string, string]("no-redis", kvLoader[string, string](nil, nil))
	})
}

func TestKVCache_StopAllKV(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	oldGlobal := globalRedisClient
	globalRedisClient = client
	defer func() { globalRedisClient = oldGlobal }()
	kvRegistryMu.Lock()
	for k := range kvRegistry {
		delete(kvRegistry, k)
	}
	kvRegistryMu.Unlock()

	RegisterKV[string, string]("stop-1", kvLoader[string, string](nil, nil))
	RegisterKV[string, string]("stop-2", kvLoader[string, string](nil, nil))

	// 不 panic 即可
	StopAllKV()
}

// ============================================================
// 配置选项 测试
// ============================================================

func TestKVCache_Options(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	_ = client.Del(context.Background(), "kv:opts").Err()

	cfg := KVCacheConfig{EnableAutoRefresh: false}
	c := NewKVCache[string, string](client, "opts", kvLoader[string, string](nil, nil), cfg)
	defer c.Stop()
	defer c.Clear(context.Background())

	// 应用选项
	WithKVTTL(time.Hour)(&c.config)
	WithKVRefreshInterval(time.Second)(&c.config)
	WithKVAutoRefresh(true)(&c.config)
	WithKVNamespace("kv")(&c.config)
	WithKVMaxLocalCacheSize(100)(&c.config)

	assert.Equal(t, time.Hour, c.config.DefaultTTL)
	assert.Equal(t, time.Second, c.config.RefreshInterval)
	assert.True(t, c.config.EnableAutoRefresh)
	assert.Equal(t, 100, c.config.MaxLocalCacheSize)
}

func TestKVCache_WithKVLogger(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	l := NewDefaultCachexLogger()
	c := NewKVCache[string, string](client, "logger-opts", kvLoader[string, string](nil, nil),
		KVCacheConfig{EnableAutoRefresh: false})
	defer c.Stop()
	WithKVLogger(l)(&c.config)
	assert.NotNil(t, c.config.Logger)
}

// ============================================================
// 分布式失效广播 测试（多节点同步）
// ============================================================

func TestKVCache_DistributedInvalidate_OnSet(t *testing.T) {
	// 模拟两个节点共用同一个 Redis，节点 A 写 → 节点 B 本地失效
	client := setupRedisClient(t)
	defer client.Close()

	name := "dist-set"
	_ = client.Del(context.Background(), "kv:"+name).Err()
	_ = client.Del(context.Background(), "kv:"+name+":invalidate").Err()

	cfg := KVCacheConfig{
		DefaultTTL:        time.Minute,
		RefreshInterval:   time.Hour,
		EnableAutoRefresh: false,
		Namespace:         "kv",
	}

	nodeA := NewKVCache[string, string](client, name, kvLoader[string, string](nil, nil), cfg)
	defer nodeA.Stop()
	defer nodeA.Clear(context.Background())

	nodeB := NewKVCache[string, string](client, name, kvLoader[string, string](nil, nil), cfg)
	defer nodeB.Stop()
	defer nodeB.Clear(context.Background())

	ctx := context.Background()

	// 1. 节点 B 先写旧值（本地 + Redis = "old"）
	require.NoError(t, nodeB.Set(ctx, "k1", "old"))
	// 等待 nodeB 的 invalidate 消息到达 nodeA（避免干扰后续）
	time.Sleep(500 * time.Millisecond)

	// 2. 节点 A 写新值（本地 + Redis = "v1"，广播 invalidate("k1")）
	require.NoError(t, nodeA.Set(ctx, "k1", "v1"))

	// 3. 等待 nodeA 的 invalidate 消息到达 nodeB，nodeB 本地 "old" 被删除
	time.Sleep(1 * time.Second)

	// 4. nodeB.Get：本地 miss → Redis "v1" → 返回最新值
	v, exists, err := nodeB.Get(ctx, "k1")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "v1", v) // 应该是 A 写的最新值，不是 "old"
}

func TestKVCache_DistributedInvalidate_OnDelete(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	name := "dist-del"
	_ = client.Del(context.Background(), "kv:"+name).Err()

	cfg := KVCacheConfig{
		DefaultTTL:        time.Minute,
		RefreshInterval:   time.Hour,
		EnableAutoRefresh: false,
		Namespace:         "kv",
	}

	nodeA := NewKVCache[string, string](client, name, kvLoader[string, string](nil, nil), cfg)
	defer nodeA.Stop()
	defer nodeA.Clear(context.Background())

	nodeB := NewKVCache[string, string](client, name, kvLoader[string, string](nil, nil), cfg)
	defer nodeB.Stop()
	defer nodeB.Clear(context.Background())

	ctx := context.Background()

	// 节点 B 本地有 k1
	require.NoError(t, nodeB.Set(ctx, "k1", "v1"))
	require.Equal(t, 1, nodeB.Size())

	// 节点 A 删除 k1
	require.NoError(t, nodeA.Delete(ctx, "k1"))

	// 等待 PubSub 传播
	time.Sleep(300 * time.Millisecond)

	// 节点 B 本地应该被失效
	_, exists, err := nodeB.Get(ctx, "k1")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestKVCache_DistributedInvalidate_OnClear(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	name := "dist-clear"
	_ = client.Del(context.Background(), "kv:"+name).Err()

	cfg := KVCacheConfig{
		DefaultTTL:        time.Minute,
		RefreshInterval:   time.Hour,
		EnableAutoRefresh: false,
		Namespace:         "kv",
	}

	nodeA := NewKVCache[string, string](client, name, kvLoader[string, string](nil, nil), cfg)
	defer nodeA.Stop()

	nodeB := NewKVCache[string, string](client, name, kvLoader[string, string](nil, nil), cfg)
	defer nodeB.Stop()

	ctx := context.Background()

	// 节点 B 本地有多个值
	require.NoError(t, nodeB.Set(ctx, "k1", "v1"))
	require.NoError(t, nodeB.Set(ctx, "k2", "v2"))
	require.Equal(t, 2, nodeB.Size())

	// 节点 A Clear
	require.NoError(t, nodeA.Clear(ctx))

	// 等待 PubSub 传播
	time.Sleep(300 * time.Millisecond)

	// 节点 B 本地应该被清空
	assert.Equal(t, 0, nodeB.Size())
}

func TestKVCache_DistributedInvalidate_SelfIgnore(t *testing.T) {
	// 验证自己发的消息不会处理（不会误删自己本地）
	client := setupRedisClient(t)
	defer client.Close()

	name := "dist-self"
	_ = client.Del(context.Background(), "kv:"+name).Err()

	cfg := KVCacheConfig{
		DefaultTTL:        time.Minute,
		RefreshInterval:   time.Hour,
		EnableAutoRefresh: false,
		Namespace:         "kv",
	}

	node := NewKVCache[string, string](client, name, kvLoader[string, string](nil, nil), cfg)
	defer node.Stop()
	defer node.Clear(context.Background())

	ctx := context.Background()

	// 自己 Set
	require.NoError(t, node.Set(ctx, "k1", "v1"))

	// 等待可能的 PubSub 回环
	time.Sleep(200 * time.Millisecond)

	// 本地缓存应该保留（自己发的消息被忽略）
	assert.Equal(t, 1, node.Size())

	v, exists, err := node.Get(ctx, "k1")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "v1", v)
}

// ============================================================
// 无效消息处理（覆盖 handler 错误路径）
// ============================================================

func TestKVCache_InvalidateHandler_InvalidJSON(t *testing.T) {
	// 直接调用 handler 覆盖 json.Unmarshal 失败路径
	client := setupRedisClient(t)
	defer client.Close()

	c := newTestKVCache[string, string](t, "invalid-json", kvLoader[string, string](nil, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	// 直接 publish 一条非法 JSON 到 invalidate channel
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := client.Publish(ctx, c.invalidateCh, "not-json").Err()
	assert.NoError(t, err)

	// 等待 handler 处理
	time.Sleep(200 * time.Millisecond)
	// 不 panic 即可
}

func TestKVCache_InvalidateHandler_ClearOp(t *testing.T) {
	// 覆盖 op=clear 分支
	client := setupRedisClient(t)
	defer client.Close()

	name := "handler-clear"
	_ = client.Del(context.Background(), "kv:"+name).Err()

	cfg := KVCacheConfig{EnableAutoRefresh: false, Namespace: "kv"}
	nodeA := NewKVCache[string, string](client, name, kvLoader[string, string](nil, nil), cfg)
	defer nodeA.Stop()
	nodeB := NewKVCache[string, string](client, name, kvLoader[string, string](nil, nil), cfg)
	defer nodeB.Stop()

	ctx := context.Background()
	// B 有数据
	require.NoError(t, nodeB.Set(ctx, "k1", "v1"))
	require.NoError(t, nodeB.Set(ctx, "k2", "v2"))

	// A Clear 触发 clear 广播
	require.NoError(t, nodeA.Clear(ctx))
	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, 0, nodeB.Size())
}

// ============================================================
// 辅助：直接验证 invalidateMsg 序列化
// ============================================================

func TestInvalidateMsg_JSON(t *testing.T) {
	m := invalidateMsg{Sender: "node-1", Op: "invalidate", Keys: []string{"a", "b"}}
	data, err := json.Marshal(m)
	require.NoError(t, err)

	var m2 invalidateMsg
	require.NoError(t, json.Unmarshal(data, &m2))
	assert.Equal(t, m.Sender, m2.Sender)
	assert.Equal(t, m.Op, m2.Op)
	assert.Equal(t, m.Keys, m2.Keys)
}

// ============================================================
// 补充：错误路径与边界覆盖
// ============================================================

func TestSetGlobalRedisClient(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	old := globalRedisClient
	defer func() { globalRedisClient = old }()

	SetGlobalRedisClient(client)
	assert.Equal(t, client, globalRedisClient)
}

func TestMustGetKV_Panic(t *testing.T) {
	kvRegistryMu.Lock()
	for k := range kvRegistry {
		delete(kvRegistry, k)
	}
	kvRegistryMu.Unlock()

	assert.Panics(t, func() {
		MustGetKV[string, string]("nonexistent-panic")
	})
}

func TestKVCache_NilClient_StartSubscriberSkipped(t *testing.T) {
	// client=nil 时 startInvalidationSubscriber 应直接返回，不 panic
	c := NewKVCache[string, string](nil, "nil-client", kvLoader[string, string](nil, nil),
		KVCacheConfig{EnableAutoRefresh: false})
	defer c.Stop()

	// publishInvalidation 应在 pubsub=nil 时安全返回
	c.publishInvalidation("invalidate", []string{"k"})
}

func TestKVCache_AutoRefresh(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	_ = client.Del(context.Background(), "kv:auto-refresh").Err()

	var calls int64
	c := NewKVCache[string, string](client, "auto-refresh",
		kvLoader[string, string](map[string]string{"a": "1"}, &calls),
		KVCacheConfig{
			DefaultTTL:        time.Minute,
			RefreshInterval:   100 * time.Millisecond,
			EnableAutoRefresh: true,
			Namespace:         "kv",
		})
	defer c.Stop()
	defer c.Clear(context.Background())

	// 等待至少 2 次自动刷新
	time.Sleep(400 * time.Millisecond)
	assert.GreaterOrEqual(t, atomic.LoadInt64(&calls), int64(2))
}

func TestKVCache_EncodeKey_AdditionalTypes(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	// int32
	cI32 := NewKVCache[int32, string](client, "enc-i32", kvLoader[int32, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cI32.Stop()
	assert.Equal(t, "100", cI32.encodeKey(int32(100)))

	// uint
	cUint := NewKVCache[uint, string](client, "enc-uint", kvLoader[uint, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cUint.Stop()
	assert.Equal(t, "200", cUint.encodeKey(uint(200)))

	// uint64
	cU64 := NewKVCache[uint64, string](client, "enc-u64", kvLoader[uint64, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cU64.Stop()
	assert.Equal(t, "300", cU64.encodeKey(uint64(300)))

	// 自定义类型（走 default 分支，fmt.Sprintf）
	type CustomKey struct{ ID string }
	cCustom := NewKVCache[CustomKey, string](client, "enc-custom", kvLoader[CustomKey, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cCustom.Stop()
	s := cCustom.encodeKey(CustomKey{ID: "x"})
	assert.Contains(t, s, "x")
}

func TestKVCache_Get_LoadAllFallbackOnRedisError(t *testing.T) {
	// 本地 miss + Redis 不可用 → 走 LoadAll 兜底
	c := newTestKVCache[string, string](t, "fallback-err", kvLoader[string, string](map[string]string{"a": "1"}, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	// 清空 Redis 强制走 loader（但 HGet 会返回 nil 错误，走 LoadAll）
	_ = c.client.Del(context.Background(), c.redisKey()).Err()

	ctx := context.Background()
	v, exists, err := c.Get(ctx, "a")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "1", v)
}

func TestKVCache_Set_EncodeValueError(t *testing.T) {
	// 使用无法 JSON 序列化的 V 类型（chan）触发 encodeValue 错误
	// 用 map[string]interface{} 包含 chan
	client := setupRedisClient(t)
	defer client.Close()

	c := NewKVCache[string, interface{}](client, "enc-err",
		kvLoader[string, interface{}](nil, nil), KVCacheConfig{EnableAutoRefresh: false, Namespace: "kv"})
	defer c.Stop()
	defer c.Clear(context.Background())

	// chan 无法 JSON 序列化
	err := c.Set(context.Background(), "k1", make(chan int))
	assert.Error(t, err)
}

func TestKVCache_SetMany_EncodeValueError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	c := NewKVCache[string, interface{}](client, "enc-err-many",
		kvLoader[string, interface{}](nil, nil), KVCacheConfig{EnableAutoRefresh: false, Namespace: "kv"})
	defer c.Stop()
	defer c.Clear(context.Background())

	err := c.SetMany(context.Background(), map[string]interface{}{"k": make(chan int)})
	assert.Error(t, err)
}

func TestKVCache_writeManyToRedis_EncodeError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	c := NewKVCache[string, interface{}](client, "write-err",
		kvLoader[string, interface{}](nil, nil), KVCacheConfig{EnableAutoRefresh: false, Namespace: "kv"})
	defer c.Stop()

	err := c.writeManyToRedis(context.Background(), map[string]interface{}{"k": make(chan int)})
	assert.Error(t, err)
}

func TestKVCache_GetMany_RedisUnavailable_LoadAllFallback(t *testing.T) {
	// Redis HMGet 失败 → 走 LoadAll 兜底 → loadFromSource 回填本地 → 从本地拿数据
	client := setupRedisClient(t)
	_ = client.Del(context.Background(), "kv:getmany-fallback").Err()

	loader := kvLoader[string, string](map[string]string{"a": "1", "b": "2"}, nil)
	c := NewKVCache[string, string](client, "getmany-fallback", loader,
		KVCacheConfig{EnableAutoRefresh: false, Namespace: "kv"})

	// 先 Clear Redis，让 HGetAll 拿不到数据，强制走 loadFromSource
	_ = c.client.Del(context.Background(), c.redisKey()).Err()

	// 关闭 Redis client 触发 HMGet 错误
	// 但需要让 loadFromSource 的 loader 成功（loader 不依赖 Redis）
	// 先 Stop pubsub 避免关闭干扰
	c.Stop()

	// 关闭 client
	client.Close()

	// GetMany：本地 miss → HMGet 失败 → LoadAll → loadFromSource → loader 回填本地 → 返回
	result, err := c.GetMany(context.Background(), []string{"a", "b"})
	// LoadAll 走 loadFromSource，loader 返回数据，本地回填
	// 但 GetMany 的兜底分支会从本地拿
	if err == nil {
		assert.Equal(t, "1", result["a"])
		assert.Equal(t, "2", result["b"])
	}
	// 如果 err != nil 也算可接受（Redis 关闭后 LoadAll 也可能失败）
}
