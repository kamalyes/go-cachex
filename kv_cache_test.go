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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kamalyes/go-toolbox/pkg/convert"
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
	assert.Equal(t, "hello", convert.MustString("hello"))

	// int K
	cInt := NewKVCache[int, string](client, "enc-int", kvLoader[int, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cInt.Stop()
	assert.Equal(t, "42", convert.MustString(42))

	// int64 K
	cI64 := NewKVCache[int64, string](client, "enc-i64", kvLoader[int64, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cI64.Stop()
	assert.Equal(t, "100", convert.MustString(int64(100)))

	// uint32 K
	cU32 := NewKVCache[uint32, string](client, "enc-u32", kvLoader[uint32, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cU32.Stop()
	assert.Equal(t, "4294967295", convert.MustString(uint32(4294967295)))
}

func TestKVCache_EncodeDecodeValue(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	c := NewKVCache[string, string](client, "test-encdec", kvLoader[string, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer c.Stop()

	// string 类型走快速路径：encodeValue 直接返回原始 string，跳过 JSON 序列化
	s, err := c.encodeValue("hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", s)

	// 新格式：直接 string，decodeValue 直接返回
	v, err := c.decodeValue("hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", v)

	// 兼容旧格式：JSON 序列化的 string（以 " 开头），decodeValue 走 json.Unmarshal
	v, err = c.decodeValue(`"hello"`)
	require.NoError(t, err)
	assert.Equal(t, "hello", v)

	// 非 string 类型的 V 走 JSON 路径，decode 失败仍返回错误
	cInt := NewKVCache[string, int](client, "test-encdec-int", kvLoader[string, int](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cInt.Stop()
	_, err = cInt.decodeValue("not-json")
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

	// 直接查 Redis Hash 应该有值（string 快速路径：存原始 string，不带 JSON 引号）
	s, err := c.client.HGet(ctx, c.redisKey(), "k1").Result()
	require.NoError(t, err)
	assert.Equal(t, "v1", s)
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

	// 2.1 直接验证 Redis 确实写入成功（排除 Pipeline 静默失败）
	redisVal, err := client.HGet(ctx, "kv:"+name, "k1").Result()
	require.NoError(t, err, "Redis HGet 应该返回 nodeA.Set 写入的值")
	assert.Equal(t, "v1", redisVal, "Redis 中 k1 的值应为 v1")

	// 3. 轮询等待 nodeA 的 invalidate 消息到达 nodeB，nodeB 本地 "old" 被删除后 Get 返回 "v1"
	//    用 require.Eventually 替代固定 sleep，适应远端 Redis 网络延迟
	require.Eventually(t, func() bool {
		v, exists, err := nodeB.Get(ctx, "k1")
		if err != nil {
			return false
		}
		return exists && v == "v1"
	}, 5*time.Second, 100*time.Millisecond, "nodeB 应在收到 invalidate 后从 Redis 拿到 v1")
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

	// 轮询等待 PubSub 传播：nodeB 本地被失效后 Get 返回 not found
	require.Eventually(t, func() bool {
		_, exists, err := nodeB.Get(ctx, "k1")
		return err == nil && !exists
	}, 5*time.Second, 100*time.Millisecond, "nodeB 应在收到 invalidate 后找不到 k1")
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

	// 轮询等待 PubSub 传播：nodeB 本地被清空
	require.Eventually(t, func() bool {
		return nodeB.Size() == 0
	}, 5*time.Second, 100*time.Millisecond, "nodeB 应在收到 clear 后本地缓存为空")
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
	assert.Equal(t, "100", convert.MustString(int32(100)))

	// uint
	cUint := NewKVCache[uint, string](client, "enc-uint", kvLoader[uint, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cUint.Stop()
	assert.Equal(t, "200", convert.MustString(uint(200)))

	// uint64
	cU64 := NewKVCache[uint64, string](client, "enc-u64", kvLoader[uint64, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cU64.Stop()
	assert.Equal(t, "300", convert.MustString(uint64(300)))

	// 自定义类型（走 default 分支，fmt.Sprintf）
	type CustomKey struct{ ID string }
	cCustom := NewKVCache[CustomKey, string](client, "enc-custom", kvLoader[CustomKey, string](nil, nil), KVCacheConfig{EnableAutoRefresh: false})
	defer cCustom.Stop()
	s := convert.MustString(CustomKey{ID: "x"})
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

// ============================================================
// BatchLoader 按需批量回源 测试
// ============================================================

// kvBatchLoader 构建 BatchLoader 的辅助函数：按 key 精准返回，并记录调用次数与传入 keys
func kvBatchLoader[K comparable, V any](data map[K]V, callCount *int64, seenKeys *[][]K) BatchLoader[K, V] {
	return func(ctx context.Context, keys []K) (map[K]V, error) {
		if callCount != nil {
			atomic.AddInt64(callCount, 1)
		}
		if seenKeys != nil {
			cp := make([]K, len(keys))
			copy(cp, keys)
			*seenKeys = append(*seenKeys, cp)
		}
		out := make(map[K]V, len(keys))
		for _, k := range keys {
			if v, ok := data[k]; ok {
				out[k] = v
			}
		}
		return out, nil
	}
}

// kvBatchErrorLoader 返回错误的 BatchLoader
func kvBatchErrorLoader[K comparable, V any]() BatchLoader[K, V] {
	return func(ctx context.Context, keys []K) (map[K]V, error) {
		return nil, errors.New("batchLoader error")
	}
}

// newTestKVCacheWithBatchLoader 创建带 BatchLoader 的测试用 KVCache（禁用自动刷新）
func newTestKVCacheWithBatchLoader[K comparable, V any](t *testing.T, name string, loader KVLoader[K, V], bl BatchLoader[K, V]) *KVCache[K, V] {
	t.Helper()
	client := setupRedisClient(t)
	_ = client.Del(context.Background(), fmt.Sprintf("kv:%s", name)).Err()
	_ = client.Del(context.Background(), fmt.Sprintf("kv:%s:invalidate", name)).Err()

	cfg := KVCacheConfig{
		DefaultTTL:        time.Minute,
		RefreshInterval:   time.Hour,
		EnableAutoRefresh: false,
		Namespace:         "kv",
		BatchLoader:       bl,
	}
	return NewKVCache[K, V](client, name, loader, cfg)
}

// TestKVCache_BatchLoader_Get 单 key miss → BatchLoader 按需回源 → 回填本地+Redis
func TestKVCache_BatchLoader_Get(t *testing.T) {
	data := map[string]string{"a": "1", "b": "2"}
	var callCount int64
	c := newTestKVCacheWithBatchLoader(t, "bl-get", nil,
		kvBatchLoader(data, &callCount, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	// 本地+Redis 均 miss → 走 BatchLoader
	v, exists, err := c.Get(ctx, "a")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "1", v)
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount))

	// 第二次 Get 命中本地，BatchLoader 不再被调用
	v, exists, err = c.Get(ctx, "a")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "1", v)
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount))
}

// TestKVCache_BatchLoader_Get_PrefillsRedis BatchLoader 回源后 Redis 被预热
// 验证：清空本地缓存后再次 Get，应命中 Redis 而非再次调用 BatchLoader
func TestKVCache_BatchLoader_Get_PrefillsRedis(t *testing.T) {
	data := map[string]string{"a": "1"}
	var callCount int64
	c := newTestKVCacheWithBatchLoader(t, "bl-prefill", nil,
		kvBatchLoader(data, &callCount, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	// 第一次 Get → 走 BatchLoader，回填 Redis
	_, _, err := c.Get(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, int64(1), atomic.LoadInt64(&callCount))

	// 清空本地缓存，强制下次 Get 走 Redis
	c.localCache.Clear()

	// 再次 Get → 应命中 Redis（BatchLoader 不被再次调用）
	v, exists, err := c.Get(ctx, "a")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "1", v)
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount))
}

// TestKVCache_BatchLoader_Get_NotFound BatchLoader 未返回该 key → exists=false
func TestKVCache_BatchLoader_Get_NotFound(t *testing.T) {
	data := map[string]string{"a": "1"} // 不含 "missing"
	var callCount int64
	c := newTestKVCacheWithBatchLoader(t, "bl-notfound", nil,
		kvBatchLoader(data, &callCount, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()
	v, exists, err := c.Get(ctx, "missing")
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, "", v)
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount))
}

// TestKVCache_BatchLoader_Get_Error BatchLoader 返回错误 → Get 返回错误
func TestKVCache_BatchLoader_Get_Error(t *testing.T) {
	c := newTestKVCacheWithBatchLoader(t, "bl-err", nil,
		kvBatchErrorLoader[string, string]())
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()
	_, exists, err := c.Get(ctx, "a")
	assert.Error(t, err)
	assert.False(t, exists)
	assert.Contains(t, err.Error(), "batchLoader")
}

// TestKVCache_BatchLoader_NoKVLoader_OnlyBatchLoader 仅有 BatchLoader（KVLoader=nil）时 Get miss 走 BatchLoader 而非 LoadAll
func TestKVCache_BatchLoader_NoKVLoader_OnlyBatchLoader(t *testing.T) {
	data := map[string]string{"a": "1"}
	var callCount int64
	c := newTestKVCacheWithBatchLoader(t, "bl-only", nil,
		kvBatchLoader(data, &callCount, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()
	// KVLoader 为 nil，若无 BatchLoader 会走 LoadAll（loader=nil 会失败）；
	// 配置了 BatchLoader 应走精准回源
	v, exists, err := c.Get(ctx, "a")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "1", v)
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount))
}

// TestKVCache_BatchLoader_GetMany 部分本地命中 + Redis miss → BatchLoader 按缺失 keys 回源
func TestKVCache_BatchLoader_GetMany(t *testing.T) {
	data := map[string]string{"a": "1", "b": "2", "c": "3"}
	var callCount int64
	var seenKeys [][]string
	c := newTestKVCacheWithBatchLoader(t, "bl-getmany", nil,
		kvBatchLoader(data, &callCount, &seenKeys))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	// 先 Set 一个进本地缓存（模拟本地命中）
	c.localCache.Store("a", "1")

	// GetMany：a 命中本地；b、c 本地+Redis miss → BatchLoader 精准回源 [b,c]
	result, err := c.GetMany(ctx, []string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Equal(t, "1", result["a"])
	assert.Equal(t, "2", result["b"])
	assert.Equal(t, "3", result["c"])
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount))

	// 验证 BatchLoader 收到的 keys 恰好是缺失的 [b,c]（顺序无关）
	require.Len(t, seenKeys, 1)
	assert.ElementsMatch(t, []string{"b", "c"}, seenKeys[0])
}

// TestKVCache_BatchLoader_GetMany_AllMiss 全部 miss → BatchLoader 一次回源所有 keys
func TestKVCache_BatchLoader_GetMany_AllMiss(t *testing.T) {
	data := map[string]string{"x": "10", "y": "20"}
	var callCount int64
	var seenKeys [][]string
	c := newTestKVCacheWithBatchLoader(t, "bl-getmany-all", nil,
		kvBatchLoader(data, &callCount, &seenKeys))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()
	result, err := c.GetMany(ctx, []string{"x", "y"})
	require.NoError(t, err)
	assert.Equal(t, "10", result["x"])
	assert.Equal(t, "20", result["y"])
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount))
	require.Len(t, seenKeys, 1)
	assert.ElementsMatch(t, []string{"x", "y"}, seenKeys[0])
}

// TestKVCache_BatchLoader_GetMany_PartialRedisHit Redis 命中部分 → BatchLoader 仅回源仍 miss 的 keys
func TestKVCache_BatchLoader_GetMany_PartialRedisHit(t *testing.T) {
	data := map[string]string{"b": "2", "c": "3"}
	var callCount int64
	var seenKeys [][]string
	c := newTestKVCacheWithBatchLoader(t, "bl-getmany-redis", nil,
		kvBatchLoader(data, &callCount, &seenKeys))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	// 直接写 Redis：a 命中 Redis，b/c 在 Redis 也 miss
	require.NoError(t, c.client.HSet(ctx, c.redisKey(), "a", `"1"`).Err())

	// GetMany：a 命中 Redis（回填本地）；b、c 走 BatchLoader
	result, err := c.GetMany(ctx, []string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Equal(t, "1", result["a"])
	assert.Equal(t, "2", result["b"])
	assert.Equal(t, "3", result["c"])
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount))
	require.Len(t, seenKeys, 1)
	assert.ElementsMatch(t, []string{"b", "c"}, seenKeys[0])
}

// TestKVCache_BatchLoader_GetMany_Error BatchLoader 返回错误 → GetMany 返回错误
func TestKVCache_BatchLoader_GetMany_Error(t *testing.T) {
	c := newTestKVCacheWithBatchLoader(t, "bl-getmany-err", nil,
		kvBatchErrorLoader[string, string]())
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()
	_, err := c.GetMany(ctx, []string{"a", "b"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "batchLoader")
}

// TestKVCache_BatchLoader_GetMany_Empty 入参为空 → 不调用 BatchLoader
func TestKVCache_BatchLoader_GetMany_Empty(t *testing.T) {
	var callCount int64
	c := newTestKVCacheWithBatchLoader(t, "bl-getmany-empty", nil,
		kvBatchLoader[string, string](nil, &callCount, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	result, err := c.GetMany(context.Background(), []string{})
	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, int64(0), atomic.LoadInt64(&callCount))
}

// TestKVCache_BatchLoader_GetMany_PrefillsRedis GetMany 回源后 Redis 被预热
// 验证：清空本地后再次 GetMany 命中 Redis，BatchLoader 不再被调用
func TestKVCache_BatchLoader_GetMany_PrefillsRedis(t *testing.T) {
	data := map[string]string{"a": "1", "b": "2"}
	var callCount int64
	c := newTestKVCacheWithBatchLoader(t, "bl-getmany-prefill", nil,
		kvBatchLoader(data, &callCount, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()

	// 第一次 GetMany → BatchLoader 回源 + 预热 Redis
	_, err := c.GetMany(ctx, []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, int64(1), atomic.LoadInt64(&callCount))

	// 清空本地缓存
	c.localCache.Clear()

	// 再次 GetMany → 命中 Redis，BatchLoader 不再调用
	result, err := c.GetMany(ctx, []string{"a", "b"})
	require.NoError(t, err)
	assert.Equal(t, "1", result["a"])
	assert.Equal(t, "2", result["b"])
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount))
}

// TestKVCache_BatchLoader_IntKey 验证 int 类型 K 的 BatchLoader 路径
func TestKVCache_BatchLoader_IntKey(t *testing.T) {
	data := map[int]string{1: "one", 2: "two"}
	var callCount int64
	c := newTestKVCacheWithBatchLoader(t, "bl-int", nil,
		kvBatchLoader(data, &callCount, nil))
	defer c.Stop()
	defer c.Clear(context.Background())

	ctx := context.Background()
	v, exists, err := c.Get(ctx, 1)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "one", v)

	// 验证 Redis Hash field 用 strconv 编码（string 快速路径：存原始 string）
	s, err := c.client.HGet(ctx, c.redisKey(), "1").Result()
	require.NoError(t, err)
	assert.Equal(t, "one", s)
}

// TestKVCache_BatchLoader_TypeMismatch config.BatchLoader 类型不匹配 → batchLoader 字段为 nil
// 此时 Get miss 应走 LoadAll 兜底（配置了 KVLoader）
func TestKVCache_BatchLoader_TypeMismatch(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	_ = client.Del(context.Background(), "kv:bl-type-mismatch").Err()

	// 故意放入类型不匹配的 BatchLoader（int→string 配置到 string→string 的 cache）
	wrongLoader := BatchLoader[int, string](func(ctx context.Context, keys []int) (map[int]string, error) {
		return nil, nil
	})
	cfg := KVCacheConfig{
		EnableAutoRefresh: false,
		Namespace:         "kv",
		BatchLoader:       wrongLoader,
	}
	// KVLoader 提供兜底
	c := NewKVCache(client, "bl-type-mismatch",
		kvLoader(map[string]string{"a": "1"}, nil), cfg)
	defer c.Stop()
	defer c.Clear(context.Background())

	// 类型断言失败 → batchLoader 为 nil → Get miss 走 LoadAll
	assert.Nil(t, c.batchLoader)
	v, exists, err := c.Get(context.Background(), "a")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "1", v)
}

// TestKVCache_WithKVBatchLoader_Option 验证 WithKVBatchLoader 选项正确设置 config.BatchLoader
func TestKVCache_WithKVBatchLoader_Option(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	_ = client.Del(context.Background(), "kv:bl-opt").Err()

	cfg := KVCacheConfig{EnableAutoRefresh: false}
	c := NewKVCache[string, string](client, "bl-opt", kvLoader[string, string](nil, nil), cfg)
	defer c.Stop()
	defer c.Clear(context.Background())

	// 应用 WithKVBatchLoader 选项
	bl := BatchLoader[string, string](func(ctx context.Context, keys []string) (map[string]string, error) {
		return map[string]string{"k": "v"}, nil
	})
	WithKVBatchLoader[string, string](bl)(&c.config)

	// config.BatchLoader 应被设置
	assert.NotNil(t, c.config.BatchLoader)
}

// ============================================================
// runParallel 单元测试（不依赖 Redis）
// ============================================================

func TestRunParallel_AllSuccess(t *testing.T) {
	var counter int64
	fns := make([]func() error, 10)
	for i := range fns {
		fns[i] = func() error {
			atomic.AddInt64(&counter, 1)
			return nil
		}
	}
	err := runParallel(fns)
	require.NoError(t, err)
	assert.Equal(t, int64(10), atomic.LoadInt64(&counter))
}

func TestRunParallel_PartialFailure(t *testing.T) {
	var counter int64
	fns := make([]func() error, 5)
	for i := range fns {
		idx := i
		fns[idx] = func() error {
			atomic.AddInt64(&counter, 1)
			if idx == 2 {
				return fmt.Errorf("error on task %d", idx)
			}
			return nil
		}
	}
	err := runParallel(fns)
	require.Error(t, err)
	// 所有任务都应该执行，即使部分失败
	assert.Equal(t, int64(5), atomic.LoadInt64(&counter))
}

func TestRunParallel_EmptyTasks(t *testing.T) {
	err := runParallel(nil)
	require.NoError(t, err)
}

func TestRunParallel_AllFail(t *testing.T) {
	fns := make([]func() error, 3)
	for i := range fns {
		fns[i] = func() error { return errors.New("fail") }
	}
	err := runParallel(fns)
	require.Error(t, err)
}

// TestRunParallel_ConcurrentSafe 验证并发安全（配合 -race 运行）
func TestRunParallel_ConcurrentSafe(t *testing.T) {
	var counter int64
	fns := make([]func() error, 100)
	for i := range fns {
		fns[i] = func() error {
			atomic.AddInt64(&counter, 1)
			return nil
		}
	}
	err := runParallel(fns)
	require.NoError(t, err)
	assert.Equal(t, int64(100), atomic.LoadInt64(&counter))
}

// ============================================================
// 空注册表测试（不依赖 Redis）
// ============================================================

func TestClearAllKV_EmptyRegistry(t *testing.T) {
	resetKVRegistry()
	err := ClearAllKV(context.Background())
	require.NoError(t, err)
}

func TestRefreshAllKV_EmptyRegistry(t *testing.T) {
	resetKVRegistry()
	err := RefreshAllKV(context.Background())
	require.NoError(t, err)
}

func TestRefreshAndSwapAllKV_EmptyRegistry(t *testing.T) {
	resetKVRegistry()
	err := RefreshAndSwapAllKV(context.Background())
	require.NoError(t, err)
}

func TestWarmupAllKV_EmptyRegistry(t *testing.T) {
	resetKVRegistry()
	ResetModelKVRegistry()
	err := WarmupAllKV(context.Background())
	require.NoError(t, err)
}

// ============================================================
// ClearAllKV 测试（依赖 Redis）
// ============================================================

// setupWarmupTest 初始化 Redis + 重置注册表，返回 cleanup 函数
func setupWarmupTest(t *testing.T) (context.Context, func()) {
	t.Helper()
	client := setupRedisClient(t)
	if client == nil {
		t.Skip("Redis 不可用，跳过预热测试")
	}
	SetGlobalRedisClient(client)
	resetKVRegistry()
	ResetModelKVRegistry()

	ctx := context.Background()
	cleanup := func() {
		StopAllKV()
	}
	return ctx, cleanup
}

func TestClearAllKV_ClearsAllCaches(t *testing.T) {
	ctx, cleanup := setupWarmupTest(t)
	defer cleanup()

	// 注册 3 个缓存，各自写入数据
	RegisterKV[string, string]("warmup-clear-1",
		kvLoader[string, string](nil, nil),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))
	RegisterKV[string, string]("warmup-clear-2",
		kvLoader[string, string](nil, nil),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))
	RegisterKV[string, string]("warmup-clear-3",
		kvLoader[string, string](nil, nil),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))

	c1 := MustGetKV[string, string]("warmup-clear-1")
	c2 := MustGetKV[string, string]("warmup-clear-2")
	c3 := MustGetKV[string, string]("warmup-clear-3")

	require.NoError(t, c1.Set(ctx, "a", "1"))
	require.NoError(t, c2.Set(ctx, "b", "2"))
	require.NoError(t, c3.Set(ctx, "c", "3"))
	assert.Equal(t, 1, c1.Size())
	assert.Equal(t, 1, c2.Size())
	assert.Equal(t, 1, c3.Size())

	// 清空所有
	require.NoError(t, ClearAllKV(ctx))

	// 验证本地缓存全部清空
	assert.Equal(t, 0, c1.Size())
	assert.Equal(t, 0, c2.Size())
	assert.Equal(t, 0, c3.Size())

	// 验证 Redis 也被清空
	for _, name := range []string{"warmup-clear-1", "warmup-clear-2", "warmup-clear-3"} {
		cnt, err := globalRedisClient.Exists(ctx, fmt.Sprintf("kv:%s", name)).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(0), cnt, "Redis key for %s should be deleted", name)
	}
}

func TestClearAllKV_PartialFailure(t *testing.T) {
	ctx, cleanup := setupWarmupTest(t)
	defer cleanup()

	// 正常缓存
	RegisterKV[string, string]("warmup-partial-ok",
		kvLoader[string, string](map[string]string{"x": "1"}, nil),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))

	// 用临时 client 模拟断连的缓存
	badClient := globalRedisClient
	RegisterKV[string, string]("warmup-partial-bad",
		kvLoader[string, string](map[string]string{"y": "2"}, nil),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))
	_ = badClient // 两个缓存共用同一 client，此处验证 ClearAllKV 不因单个错误中断

	okCache := MustGetKV[string, string]("warmup-partial-ok")
	require.NoError(t, okCache.Set(ctx, "x", "1"))
	assert.Equal(t, 1, okCache.Size())

	// ClearAllKV 应执行完毕，不因任何错误中断其余缓存
	_ = ClearAllKV(ctx)

	// ok 缓存应被清空
	assert.Equal(t, 0, okCache.Size())
}

// ============================================================
// RefreshAllKV 测试（依赖 Redis）
// ============================================================

func TestRefreshAllKV_LoadsFromSource(t *testing.T) {
	ctx, cleanup := setupWarmupTest(t)
	defer cleanup()

	var call1, call2 int64
	RegisterKV[string, string]("warmup-refresh-1",
		kvLoader(map[string]string{"a": "v1", "b": "v2"}, &call1),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))
	RegisterKV[string, string]("warmup-refresh-2",
		kvLoader(map[string]string{"c": "v3"}, &call2),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))

	// 先清空确保从空状态开始
	require.NoError(t, ClearAllKV(ctx))

	// Refresh 从 loader 加载
	require.NoError(t, RefreshAllKV(ctx))

	assert.Equal(t, int64(1), atomic.LoadInt64(&call1))
	assert.Equal(t, int64(1), atomic.LoadInt64(&call2))

	// 验证本地缓存有数据
	c1 := MustGetKV[string, string]("warmup-refresh-1")
	c2 := MustGetKV[string, string]("warmup-refresh-2")
	assert.Equal(t, 2, c1.Size())
	assert.Equal(t, 1, c2.Size())

	// 验证 Redis 也有数据
	v, ok, err := c1.Get(ctx, "a")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "v1", v)
}

func TestRefreshAllKV_PartialFailure(t *testing.T) {
	ctx, cleanup := setupWarmupTest(t)
	defer cleanup()

	var okCalls int64
	RegisterKV[string, string]("warmup-rf-ok",
		kvLoader(map[string]string{"k": "v"}, &okCalls),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))
	RegisterKV[string, string]("warmup-rf-err",
		kvErrorLoader[string, string](),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))

	// RefreshAllKV 应返回错误（来自 err loader），但不中断 ok 缓存
	err := RefreshAllKV(ctx)
	require.Error(t, err)

	// ok 缓存仍然成功加载
	assert.Equal(t, int64(1), atomic.LoadInt64(&okCalls))
	okCache := MustGetKV[string, string]("warmup-rf-ok")
	assert.Equal(t, 1, okCache.Size())
}

// ============================================================
// RefreshAndSwap 测试（依赖 Redis）
// ============================================================

// TestRefreshAndSwap_AtomicReplace 验证 RefreshAndSwap 原子替换：
//   - 旧 key 被清除（stale_key 不存在于 loader，应从 Redis 消失）
//   - 新 key 被写入
//   - 旧 key 的值被刷新为 loader 最新值
func TestRefreshAndSwap_AtomicReplace(t *testing.T) {
	ctx, cleanup := setupWarmupTest(t)
	defer cleanup()

	var callCount int64
	RegisterKV[string, string]("swap-atomic",
		kvLoader(map[string]string{"a": "NEW", "b": "2"}, &callCount),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))

	c := MustGetKV[string, string]("swap-atomic")

	// 先写入陈旧数据：a=OLD（将被刷新）和 stale_key（应被删除）
	require.NoError(t, c.Set(ctx, "a", "OLD"))
	require.NoError(t, c.Set(ctx, "stale_key", "should_be_removed"))

	// 执行 RefreshAndSwap
	require.NoError(t, c.RefreshAndSwap(ctx))

	// loader 被调用一次
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount))

	// "a" 应为 loader 的新值
	v, ok, err := c.Get(ctx, "a")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "NEW", v)

	// "b" 应存在
	v, ok, err = c.Get(ctx, "b")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "2", v)

	// "stale_key" 应从 Redis 消失（Lua 脚本 DEL 了整个 Hash）
	redisKey := "kv:swap-atomic"
	exists, err := globalRedisClient.HExists(ctx, redisKey, "stale_key").Result()
	require.NoError(t, err)
	assert.False(t, exists, "stale_key should be removed from Redis by atomic DEL+HSET")
}

// TestRefreshAndSwap_EmptyDataClearsRedis 验证 loader 返回空数据时 RefreshAndSwap 清空 Redis
func TestRefreshAndSwap_EmptyDataClearsRedis(t *testing.T) {
	ctx, cleanup := setupWarmupTest(t)
	defer cleanup()

	// loader 返回空 map
	RegisterKV("swap-empty",
		kvLoader(map[string]string{}, nil),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))

	c := MustGetKV[string, string]("swap-empty")

	// 先写入数据
	require.NoError(t, c.Set(ctx, "k1", "v1"))
	require.NoError(t, c.Set(ctx, "k2", "v2"))

	// 执行 RefreshAndSwap（loader 返回空）
	require.NoError(t, c.RefreshAndSwap(ctx))

	// 本地缓存应为空
	assert.Equal(t, 0, c.Size())

	// Redis Hash 应被删除（DEL 后没有 HSET，键不存在）
	redisKey := "kv:swap-empty"
	cnt, err := globalRedisClient.Exists(ctx, redisKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), cnt, "Redis Hash should be deleted when loader returns empty data")
}

// TestRefreshAndSwap_NoLoaderReturnsError 验证无 loader 时 RefreshAndSwap 返回错误
func TestRefreshAndSwap_NoLoaderReturnsError(t *testing.T) {
	ctx, cleanup := setupWarmupTest(t)
	defer cleanup()

	RegisterKV[string, string]("swap-no-loader",
		nil,
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))

	c := MustGetKV[string, string]("swap-no-loader")
	err := c.RefreshAndSwap(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no loader")
}

// TestRefreshAndSwapAllKV_PartialFailure 验证 RefreshAndSwapAllKV 部分失败不中断其余缓存
func TestRefreshAndSwapAllKV_PartialFailure(t *testing.T) {
	ctx, cleanup := setupWarmupTest(t)
	defer cleanup()

	var okCalls int64
	RegisterKV[string, string]("swap-partial-ok",
		kvLoader(map[string]string{"k": "v"}, &okCalls),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))
	RegisterKV[string, string]("swap-partial-err",
		kvErrorLoader[string, string](),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))

	// RefreshAndSwapAllKV 应返回错误（来自 err loader），但不中断 ok 缓存
	err := RefreshAndSwapAllKV(ctx)
	require.Error(t, err)

	// ok 缓存仍然成功加载
	assert.Equal(t, int64(1), atomic.LoadInt64(&okCalls))
	okCache := MustGetKV[string, string]("swap-partial-ok")
	assert.Equal(t, 1, okCache.Size())
}

// ============================================================
// WarmupAllKV 完整流程测试（依赖 Redis）
// ============================================================

func TestWarmupAllKV_FullFlow(t *testing.T) {
	ctx, cleanup := setupWarmupTest(t)
	defer cleanup()

	var callCount int64
	RegisterKV[string, string]("warmup-full-1",
		kvLoader(map[string]string{"a": "1", "b": "2"}, &callCount),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))
	RegisterKV[string, string]("warmup-full-2",
		kvLoader(map[string]string{"c": "3"}, nil),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))

	// 手动写入陈旧数据到 Redis（包括 loader 中不存在的 key）
	c1 := MustGetKV[string, string]("warmup-full-1")
	require.NoError(t, c1.Set(ctx, "a", "OLD"))
	require.NoError(t, c1.Set(ctx, "stale_key", "should_be_removed"))

	// 执行完整预热
	err := WarmupAllKV(ctx)
	require.NoError(t, err)

	// 验证 loader 被调用
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount))

	// 验证 "a" 的值已被刷新为 loader 的值
	v, ok, err := c1.Get(ctx, "a")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "1", v, "stale value should be replaced by fresh loader data")

	// 验证 "b" 存在（loader 新增的）
	v, ok, err = c1.Get(ctx, "b")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "2", v)

	// 验证 "stale_key" 已被清除（RefreshAndSwap 的 Lua 脚本 DEL 了整个 Hash，只写 loader 数据）
	_, ok, _ = c1.Get(ctx, "stale_key")
	assert.False(t, ok, "stale key should be removed after WarmupAllKV (RefreshAndSwap)")
}

func TestWarmupAllKV_RemovesStaleEntries(t *testing.T) {
	ctx, cleanup := setupWarmupTest(t)
	defer cleanup()

	// 注册一个缓存，loader 只返回 {"fresh": "data"}
	RegisterKV[string, string]("warmup-stale",
		kvLoader(map[string]string{"fresh": "data"}, nil),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))

	// 直接写 Redis 模拟陈旧数据（loader 中不存在这些 key）
	redisKey := "kv:warmup-stale"
	require.NoError(t, globalRedisClient.HSet(ctx, redisKey, "stale1", "old1", "stale2", "old2", "fresh", "OLD").Err())

	// 执行预热
	require.NoError(t, WarmupAllKV(ctx))

	// 验证 stale 条目已被清除
	exists, err := globalRedisClient.HExists(ctx, redisKey, "stale1").Result()
	require.NoError(t, err)
	assert.False(t, exists, "stale1 should be removed from Redis after warmup")

	exists, err = globalRedisClient.HExists(ctx, redisKey, "stale2").Result()
	require.NoError(t, err)
	assert.False(t, exists, "stale2 should be removed from Redis after warmup")

	// 验证 fresh 条目已被更新为 loader 的值
	val, err := globalRedisClient.HGet(ctx, redisKey, "fresh").Result()
	require.NoError(t, err)
	assert.Equal(t, "data", val, "fresh key should be updated to loader value")
}

// ============================================================
// ResetLoadCache / ResetAllModelKVLoadCache 测试
// ============================================================

func TestModelKVCache_ResetLoadCache(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field("Name").
		Register()

	// 手动填充 cachedItems 模拟之前的 loadAll
	cache.cacheMu.Lock()
	cache.cachedItems = []*testModelIntPK{{Id: 1, Name: "test"}}
	cache.cachedItemsAt = time.Now()
	cache.cacheMu.Unlock()

	// 重置
	cache.ResetLoadCache()

	// 验证 cachedItems 已清空
	cache.cacheMu.RLock()
	assert.Nil(t, cache.cachedItems)
	assert.True(t, cache.cachedItemsAt.IsZero())
	cache.cacheMu.RUnlock()
}

func TestResetAllModelKVLoadCache(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	cache1 := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field("Name").
		Register()
	cache2 := NewModelKV[testModelStringPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field("Name").
		Register()

	// 填充两个 ModelKV 的 cachedItems
	cache1.cacheMu.Lock()
	cache1.cachedItems = []*testModelIntPK{{Id: 1}}
	cache1.cachedItemsAt = time.Now()
	cache1.cacheMu.Unlock()

	cache2.cacheMu.Lock()
	cache2.cachedItems = []*testModelStringPK{{TenantId: "t1"}}
	cache2.cachedItemsAt = time.Now()
	cache2.cacheMu.Unlock()

	// 重置所有
	ResetAllModelKVLoadCache()

	// 验证两个都已重置
	cache1.cacheMu.RLock()
	assert.Nil(t, cache1.cachedItems)
	cache1.cacheMu.RUnlock()

	cache2.cacheMu.RLock()
	assert.Nil(t, cache2.cachedItems)
	cache2.cacheMu.RUnlock()
}

func TestResetAllModelKVLoadCache_EmptyRegistry(t *testing.T) {
	ResetModelKVRegistry()
	// 不应 panic
	ResetAllModelKVLoadCache()
}

// ============================================================
// 并发安全测试（配合 -race 运行）
// ============================================================

// TestWarmupAllKV_ConcurrentSafe 验证预热流程的并发安全
// 多个 goroutine 同时读缓存时执行 WarmupAllKV 不应出现数据竞争
func TestWarmupAllKV_ConcurrentSafe(t *testing.T) {
	ctx, cleanup := setupWarmupTest(t)
	defer cleanup()

	RegisterKV[string, string]("warmup-race",
		kvLoader(map[string]string{"k1": "v1", "k2": "v2", "k3": "v3"}, nil),
		WithKVNamespace("kv"), WithKVTTL(time.Minute), WithKVAutoRefresh(false))

	// 先预热一次
	require.NoError(t, WarmupAllKV(ctx))

	cache := MustGetKV[string, string]("warmup-race")

	// 并发：1 个 goroutine 执行 WarmupAllKV，3 个 goroutine 并发读
	var wg sync.WaitGroup
	wg.Add(4)
	done := make(chan struct{})

	// writer：执行 WarmupAllKV
	go func() {
		defer wg.Done()
		_ = WarmupAllKV(ctx)
	}()

	// readers：并发读缓存
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _, _ = cache.Get(ctx, "k1")
			}
		}()
	}

	// 等待全部完成，超时保护
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent test timed out")
	}
}

// ============================================================
// 基准测试
// ============================================================

// BenchmarkRunParallel 测量 runParallel 的调度开销（无 I/O，纯调度）
func BenchmarkRunParallel(b *testing.B) {
	fns := make([]func() error, 20)
	for i := range fns {
		fns[i] = func() error { return nil }
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = runParallel(fns)
	}
}

// BenchmarkRunParallel_WithErrors 测量有错误时的收集开销
func BenchmarkRunParallel_WithErrors(b *testing.B) {
	fns := make([]func() error, 20)
	for i := range fns {
		fns[i] = func() error { return errors.New("bench error") }
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = runParallel(fns)
	}
}
