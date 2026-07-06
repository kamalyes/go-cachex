/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-07-06 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-07-06 11:16:00
 * @FilePath: \go-cachex\generic_cache_test.go
 * @Description: GenericTTLCache 单元测试，覆盖本地 TTL、惰性过期、PubSub 跨节点失效、KeyCodec
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newGenericTestRedis 创建基于 miniredis 的测试 Redis 客户端
func newGenericTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() { client.Close() })
	return client, s
}

// waitForInvalidation 等待 PubSub 失效消息传播
func waitForInvalidation() {
	time.Sleep(200 * time.Millisecond)
}

// ============================================================================
// 本地缓存基础功能
// ============================================================================

func TestGenericTTLCache_StoreAndLoad(t *testing.T) {
	cache := NewGenericTTLCache[string, string]()

	cache.Store("k1", "v1", time.Minute)
	v, ok := cache.Load("k1")
	assert.True(t, ok)
	assert.Equal(t, "v1", v)

	_, ok = cache.Load("missing")
	assert.False(t, ok)
}

func TestGenericTTLCache_LazyExpire(t *testing.T) {
	cache := NewGenericTTLCache[string, string]()

	cache.Store("k1", "v1", 50*time.Millisecond)
	v, ok := cache.Load("k1")
	assert.True(t, ok)
	assert.Equal(t, "v1", v)

	time.Sleep(60 * time.Millisecond)
	_, ok = cache.Load("k1")
	assert.False(t, ok) // 惰性过期：读取时发现过期并删除
}

func TestGenericTTLCache_Delete(t *testing.T) {
	cache := NewGenericTTLCache[string, string]()
	cache.Store("k1", "v1", time.Minute)

	cache.Delete("k1")
	_, ok := cache.Load("k1")
	assert.False(t, ok)
}

func TestGenericTTLCache_GenericValueTypes(t *testing.T) {
	// 验证泛型值类型（bool、slice、指针等）均可存储
	t.Run("bool", func(t *testing.T) {
		cache := NewGenericTTLCache[string, bool]()
		cache.Store("flag", true, time.Minute)
		v, ok := cache.Load("flag")
		assert.True(t, ok)
		assert.True(t, v)
	})

	t.Run("slice", func(t *testing.T) {
		cache := NewGenericTTLCache[string, []string]()
		cache.Store("ids", []string{"a", "b"}, time.Minute)
		v, ok := cache.Load("ids")
		assert.True(t, ok)
		assert.Equal(t, []string{"a", "b"}, v)
	})

	t.Run("int-key", func(t *testing.T) {
		cache := NewGenericTTLCache[int, string]()
		cache.Store(42, "answer", time.Minute)
		v, ok := cache.Load(42)
		assert.True(t, ok)
		assert.Equal(t, "answer", v)
	})
}

// ============================================================================
// KeyCodec
// ============================================================================

func TestStringKeyCodec(t *testing.T) {
	c := StringKeyCodec{}
	assert.Equal(t, "abc", c.Encode("abc"))
	v, err := c.Decode("abc")
	assert.NoError(t, err)
	assert.Equal(t, "abc", v)
}

func TestIntKeyCodec(t *testing.T) {
	c := IntKeyCodec{}
	assert.Equal(t, "42", c.Encode(42))
	v, err := c.Decode("42")
	assert.NoError(t, err)
	assert.Equal(t, 42, v)
}

func TestInt64KeyCodec(t *testing.T) {
	c := Int64KeyCodec{}
	assert.Equal(t, "9999999999", c.Encode(9999999999))
	v, err := c.Decode("9999999999")
	assert.NoError(t, err)
	assert.Equal(t, int64(9999999999), v)
}

// ============================================================================
// PubSub 跨节点失效
// ============================================================================

// TestGenericTTLCache_PubSub_DeleteInvalidation 验证节点 A 的 Delete 广播到节点 B
func TestGenericTTLCache_PubSub_DeleteInvalidation(t *testing.T) {
	client, _ := newGenericTestRedis(t)

	ps1 := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	ps2 := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	t.Cleanup(func() {
		ps1.Close()
		ps2.Close()
	})

	const channel = "test:generic:invalidate"

	// 节点 A 和节点 B 使用相同 channel 和 codec
	cacheA := NewGenericTTLCache(
		WithPubSub[string, string](ps1, channel, StringKeyCodec{}),
	)
	cacheB := NewGenericTTLCache(
		WithPubSub[string, string](ps2, channel, StringKeyCodec{}),
	)
	t.Cleanup(func() {
		cacheA.Stop()
		cacheB.Stop()
	})

	// 两个节点各自写入相同 key
	cacheA.Store("k1", "v1", time.Minute)
	cacheB.Store("k1", "v1", time.Minute)
	require.True(t, cacheB.LoadExist("k1"))

	// 节点 A 删除，节点 B 应收到失效事件并删除本地缓存
	cacheA.Delete("k1")
	waitForInvalidation()

	_, ok := cacheB.Load("k1")
	assert.False(t, ok, "节点 B 应通过 PubSub 收到 Delete 事件并失效本地缓存")
}

// TestGenericTTLCache_PubSub_ClearInvalidation 验证 Clear 广播
func TestGenericTTLCache_PubSub_ClearInvalidation(t *testing.T) {
	client, _ := newGenericTestRedis(t)

	ps1 := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	ps2 := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	t.Cleanup(func() {
		ps1.Close()
		ps2.Close()
	})

	const channel = "test:generic:clear"
	cacheA := NewGenericTTLCache(
		WithPubSub[string, string](ps1, channel, StringKeyCodec{}),
	)
	cacheB := NewGenericTTLCache(
		WithPubSub[string, string](ps2, channel, StringKeyCodec{}),
	)
	t.Cleanup(func() {
		cacheA.Stop()
		cacheB.Stop()
	})

	cacheB.Store("a", "1", time.Minute)
	cacheB.Store("b", "2", time.Minute)
	require.True(t, cacheB.LoadExist("a"))
	require.True(t, cacheB.LoadExist("b"))

	cacheA.Clear()
	waitForInvalidation()

	assert.False(t, cacheB.LoadExist("a"), "节点 B 的 a 应被 clear 事件清除")
	assert.False(t, cacheB.LoadExist("b"), "节点 B 的 b 应被 clear 事件清除")
}

// TestGenericTTLCache_PubSub_IntKey 验证非 string 键类型（int）通过 KeyCodec 正确还原
// 这是 KeyCodec 解决 "冰不住 generic 变量" 的核心验证：any(ks).(K) 对 int 静默失效，KeyCodec 不会
func TestGenericTTLCache_PubSub_IntKey(t *testing.T) {
	client, _ := newGenericTestRedis(t)

	ps1 := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	ps2 := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	t.Cleanup(func() {
		ps1.Close()
		ps2.Close()
	})

	const channel = "test:generic:int"
	cacheA := NewGenericTTLCache(
		WithPubSub[int, string](ps1, channel, IntKeyCodec{}),
	)
	cacheB := NewGenericTTLCache(
		WithPubSub[int, string](ps2, channel, IntKeyCodec{}),
	)
	t.Cleanup(func() {
		cacheA.Stop()
		cacheB.Stop()
	})

	cacheB.Store(42, "answer", time.Minute)
	require.True(t, cacheB.LoadExist(42))

	cacheA.Delete(42) // 节点 A 删除 int 键 42
	waitForInvalidation()

	_, ok := cacheB.Load(42)
	assert.True(t, ok == false, "节点 B 的 int 键 42 应通过 KeyCodec 还原后被删除")
}

// TestGenericTTLCache_PubSub_IgnoreSelfMessage 验证不处理自己发出的消息
func TestGenericTTLCache_PubSub_IgnoreSelfMessage(t *testing.T) {
	client, _ := newGenericTestRedis(t)

	ps := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	t.Cleanup(func() { ps.Close() })

	const channel = "test:generic:self"
	cache := NewGenericTTLCache(
		WithPubSub[string, string](ps, channel, StringKeyCodec{}),
	)
	t.Cleanup(func() { cache.Stop() })

	cache.Store("k1", "v1", time.Minute)
	cache.Delete("k1")

	// 自己 Delete 后本地应已删除（通过本地 m.Delete，而非 PubSub 回环）
	_, ok := cache.Load("k1")
	assert.False(t, ok)
}

// TestGenericTTLCache_PubSub_Stop 验证 Stop 后不再接收失效事件
func TestGenericTTLCache_PubSub_Stop(t *testing.T) {
	client, _ := newGenericTestRedis(t)

	ps1 := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	ps2 := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	t.Cleanup(func() {
		ps1.Close()
		ps2.Close()
	})

	const channel = "test:generic:stop"
	cacheA := NewGenericTTLCache(
		WithPubSub[string, string](ps1, channel, StringKeyCodec{}),
	)
	cacheB := NewGenericTTLCache(
		WithPubSub[string, string](ps2, channel, StringKeyCodec{}),
	)

	cacheB.Store("k1", "v1", time.Minute)
	cacheB.Stop() // 节点 B 停止订阅

	cacheA.Store("k1", "v1", time.Minute)
	cacheA.Delete("k1")
	waitForInvalidation()

	// 节点 B 已 Stop，不应收到失效事件，本地缓存仍在（虽已 Stop 但 m 未清）
	assert.True(t, cacheB.LoadExist("k1"), "节点 B Stop 后不再接收失效事件，本地缓存应保留")
}

// LoadExist 测试辅助：判断 key 是否存在且未过期（不返回值）
func (c *GenericTTLCache[K, V]) LoadExist(key K) bool {
	_, ok := c.Load(key)
	return ok
}

// ============================================================================
// Invalidate / DeleteLocal
// ============================================================================

// TestGenericTTLCache_PubSub_Invalidate 验证 Invalidate 仅广播不删本地
// 场景：节点 A 写入新值后 Invalidate，节点 B 删除旧值，节点 A 本地保留
func TestGenericTTLCache_PubSub_Invalidate(t *testing.T) {
	client, _ := newGenericTestRedis(t)

	ps1 := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	ps2 := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	t.Cleanup(func() {
		ps1.Close()
		ps2.Close()
	})

	const channel = "test:generic:invalidate-only"
	cacheA := NewGenericTTLCache(
		WithPubSub[string, string](ps1, channel, StringKeyCodec{}),
	)
	cacheB := NewGenericTTLCache(
		WithPubSub[string, string](ps2, channel, StringKeyCodec{}),
	)
	t.Cleanup(func() {
		cacheA.Stop()
		cacheB.Stop()
	})

	// 节点 B 先缓存旧值
	cacheB.Store("k1", "old", time.Minute)
	require.True(t, cacheB.LoadExist("k1"))

	// 节点 A 写入新值并广播失效（不删本地，因为刚 Store 了新值）
	cacheA.Store("k1", "new", time.Minute)
	cacheA.Invalidate("k1")
	waitForInvalidation()

	// 节点 A 本地保留（Invalidate 不删本地）
	v, ok := cacheA.Load("k1")
	assert.True(t, ok)
	assert.Equal(t, "new", v)

	// 节点 B 本地被删除（收到失效事件后删除旧值，下次读回源拿新值）
	assert.False(t, cacheB.LoadExist("k1"), "节点 B 应收到 Invalidate 后删除本地旧值")
}

// TestGenericTTLCache_DeleteLocal 验证 DeleteLocal 仅删本地不广播
func TestGenericTTLCache_DeleteLocal(t *testing.T) {
	client, _ := newGenericTestRedis(t)

	ps1 := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	ps2 := NewPubSub(client, PubSubConfig{MaxWorkers: 5, WorkerQueueSize: 10})
	t.Cleanup(func() {
		ps1.Close()
		ps2.Close()
	})

	const channel = "test:generic:delete-local"
	cacheA := NewGenericTTLCache(
		WithPubSub[string, string](ps1, channel, StringKeyCodec{}),
	)
	cacheB := NewGenericTTLCache(
		WithPubSub[string, string](ps2, channel, StringKeyCodec{}),
	)
	t.Cleanup(func() {
		cacheA.Stop()
		cacheB.Stop()
	})

	cacheA.Store("k1", "v1", time.Minute)
	cacheB.Store("k1", "v1", time.Minute)
	require.True(t, cacheB.LoadExist("k1"))

	// 节点 A 仅本地删除，不广播
	cacheA.DeleteLocal("k1")
	waitForInvalidation()

	assert.False(t, cacheA.LoadExist("k1"), "节点 A 本地应已删除")
	assert.True(t, cacheB.LoadExist("k1"), "节点 B 不应受 DeleteLocal 影响")
}
