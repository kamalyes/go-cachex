/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-08-09 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-08-09 00:00:00
 * @FilePath: \go-cachex\sharded_test.go
 * @Description: 分片缓存（ShardedHandler）测试，覆盖构造/路由/简化与带 ctx 方法/批量并发/统计/Close
 *
 * 使用 NewLRUHandler 作为 factory 创建每个 shard 的 Handler 实例，
 * 通过多 key 写入验证分片路由与并发收集路径。
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lruFactory 返回一个创建 LRUHandler 的 factory，供 NewShardedHandler 使用
func lruFactory(capacity int) func() Handler {
	return func() Handler { return NewLRUHandler(capacity) }
}

// errCloseHandler 嵌入 *LRUHandler 并覆盖 Close，用于覆盖 ShardedHandler.Close 的 lastErr 分支
type errCloseHandler struct {
	*LRUHandler
	closeErr error
}

func (e *errCloseHandler) Close() error { return e.closeErr }

// ============================================================
// NewShardedHandler 构造 测试
// ============================================================

func TestNewShardedHandler_DefaultShards(t *testing.T) {
	// shards <= 0 时默认 16
	h := NewShardedHandler(lruFactory(10), 0)
	defer h.Close()

	stats := h.Stats()
	assert.Equal(t, 16, stats["total_shards"])
	assert.Equal(t, "sharded", stats["cache_type"])
	for i := 0; i < 16; i++ {
		assert.Contains(t, stats, fmt.Sprintf("shard_%d", i))
	}
}

func TestNewShardedHandler_NegativeShards(t *testing.T) {
	h := NewShardedHandler(lruFactory(10), -1)
	defer h.Close()

	assert.Equal(t, 16, h.Stats()["total_shards"])
}

func TestNewShardedHandler_CustomShards(t *testing.T) {
	h := NewShardedHandler(lruFactory(10), 4)
	defer h.Close()

	stats := h.Stats()
	assert.Equal(t, 4, stats["total_shards"])
	for i := 0; i < 4; i++ {
		assert.Contains(t, stats, fmt.Sprintf("shard_%d", i))
	}
}

// ============================================================
// 简化版方法（不带 context） 测试
// ============================================================

func TestSharded_SetGetDel(t *testing.T) {
	h := NewShardedHandler(lruFactory(100), 4)
	defer h.Close()

	keys := [][]byte{[]byte("k1"), []byte("k2"), []byte("k3")}
	for i, k := range keys {
		require.NoError(t, h.Set(k, []byte(fmt.Sprintf("v%d", i+1))))
	}

	for i, k := range keys {
		v, err := h.Get(k)
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("v%d", i+1), string(v))
	}

	// 无 TTL 时 GetTTL 返回 0
	ttl, err := h.GetTTL([]byte("k1"))
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), ttl)

	// 删除后 Get 返回 ErrNotFound
	require.NoError(t, h.Del([]byte("k1")))
	_, err = h.Get([]byte("k1"))
	assert.ErrorIs(t, err, ErrNotFound)

	// 未命中 GetTTL 也返回 ErrNotFound
	_, err = h.GetTTL([]byte("k1"))
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestSharded_SetWithTTL_GetTTL(t *testing.T) {
	h := NewShardedHandler(lruFactory(100), 4)
	defer h.Close()

	require.NoError(t, h.SetWithTTL([]byte("tk"), []byte("tv"), time.Minute))

	// 简化版 Get / GetTTL
	v, err := h.Get([]byte("tk"))
	require.NoError(t, err)
	assert.Equal(t, "tv", string(v))

	ttl, err := h.GetTTL([]byte("tk"))
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))
	assert.LessOrEqual(t, ttl, time.Minute)
}

// ============================================================
// 带context方法 测试
// ============================================================

func TestSharded_WithCtxMethods(t *testing.T) {
	h := NewShardedHandler(lruFactory(100), 4)
	defer h.Close()

	ctx := context.Background()

	// SetWithCtx
	require.NoError(t, h.SetWithCtx(ctx, []byte("ck"), []byte("cv")))

	// GetWithCtx
	v, err := h.GetWithCtx(ctx, []byte("ck"))
	require.NoError(t, err)
	assert.Equal(t, "cv", string(v))

	// GetTTLWithCtx（无 TTL → 0）
	ttl, err := h.GetTTLWithCtx(ctx, []byte("ck"))
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), ttl)

	// SetWithTTLAndCtx
	require.NoError(t, h.SetWithTTLAndCtx(ctx, []byte("ck2"), []byte("cv2"), 30*time.Second))
	ttl2, err := h.GetTTLWithCtx(ctx, []byte("ck2"))
	require.NoError(t, err)
	assert.Greater(t, ttl2, time.Duration(0))

	// DelWithCtx
	require.NoError(t, h.DelWithCtx(ctx, []byte("ck")))
	_, err = h.GetWithCtx(ctx, []byte("ck"))
	assert.ErrorIs(t, err, ErrNotFound)
}

// ============================================================
// BatchGet / BatchGetWithCtx 测试
// ============================================================

func TestSharded_BatchGet(t *testing.T) {
	h := NewShardedHandler(lruFactory(100), 4)
	defer h.Close()

	for i := 0; i < 5; i++ {
		require.NoError(t, h.Set([]byte(fmt.Sprintf("bk%d", i)), []byte(fmt.Sprintf("bv%d", i))))
	}

	keys := [][]byte{
		[]byte("bk0"), []byte("bk1"), []byte("bk2"),
		[]byte("bk3"), []byte("bk4"), []byte("missing"),
	}

	// 简化版 BatchGet
	vals, errs := h.BatchGet(keys)
	require.Len(t, vals, len(keys))
	require.Len(t, errs, len(keys))
	for i := 0; i < 5; i++ {
		assert.NoError(t, errs[i])
		assert.Equal(t, fmt.Sprintf("bv%d", i), string(vals[i]))
	}
	assert.ErrorIs(t, errs[5], ErrNotFound)
	assert.Nil(t, vals[5])
}

func TestSharded_BatchGetWithCtx_Empty(t *testing.T) {
	h := NewShardedHandler(lruFactory(10), 4)
	defer h.Close()

	// 空 keys 直接返回 nil, nil（覆盖 len(keys)==0 早返回分支）
	vals, errs := h.BatchGetWithCtx(context.Background(), nil)
	assert.Nil(t, vals)
	assert.Nil(t, errs)
}

func TestSharded_BatchGetWithCtx_MultiShard(t *testing.T) {
	h := NewShardedHandler(lruFactory(1000), 8)
	defer h.Close()

	ctx := context.Background()

	// 写入大量 key，确保分布到多个 shard，触发并发 goroutine + channel 收集路径
	const n = 50
	for i := 0; i < n; i++ {
		require.NoError(t, h.Set([]byte(fmt.Sprintf("mk-%d", i)), []byte(fmt.Sprintf("mv-%d", i))))
	}

	keys := make([][]byte, n)
	for i := 0; i < n; i++ {
		keys[i] = []byte(fmt.Sprintf("mk-%d", i))
	}

	vals, errs := h.BatchGetWithCtx(ctx, keys)
	require.Len(t, vals, n)
	require.Len(t, errs, n)
	for i := 0; i < n; i++ {
		assert.NoError(t, errs[i])
		assert.Equal(t, fmt.Sprintf("mv-%d", i), string(vals[i]))
	}
}

// ============================================================
// Stats 测试
// ============================================================

func TestSharded_Stats(t *testing.T) {
	h := NewShardedHandler(lruFactory(100), 3)
	defer h.Close()

	require.NoError(t, h.Set([]byte("s1"), []byte("v1")))

	stats := h.Stats()
	assert.Equal(t, 3, stats["total_shards"])
	assert.Equal(t, "sharded", stats["cache_type"])
	for i := 0; i < 3; i++ {
		assert.Contains(t, stats, fmt.Sprintf("shard_%d", i))
		// 每个分片统计应是 map
		assert.NotNil(t, stats[fmt.Sprintf("shard_%d", i)])
	}
}

// ============================================================
// GetOrCompute / GetOrComputeWithCtx 测试
// ============================================================

func TestSharded_GetOrCompute_Hit(t *testing.T) {
	h := NewShardedHandler(lruFactory(100), 4)
	defer h.Close()

	require.NoError(t, h.Set([]byte("goc"), []byte("cached")))

	called := false
	v, err := h.GetOrCompute([]byte("goc"), time.Minute, func() ([]byte, error) {
		called = true
		return []byte("computed"), nil
	})
	require.NoError(t, err)
	assert.Equal(t, "cached", string(v))
	assert.False(t, called, "缓存命中时 loader 不应被调用")
}

func TestSharded_GetOrCompute_Miss(t *testing.T) {
	h := NewShardedHandler(lruFactory(100), 4)
	defer h.Close()

	called := false
	v, err := h.GetOrCompute([]byte("goc-miss"), time.Minute, func() ([]byte, error) {
		called = true
		return []byte("computed"), nil
	})
	require.NoError(t, err)
	assert.Equal(t, "computed", string(v))
	assert.True(t, called, "缓存未命中时 loader 应被调用")

	// 再次获取应命中，loader 不再调用
	called = false
	v, err = h.GetOrCompute([]byte("goc-miss"), time.Minute, func() ([]byte, error) {
		called = true
		return []byte("computed2"), nil
	})
	require.NoError(t, err)
	assert.Equal(t, "computed", string(v))
	assert.False(t, called)
}

func TestSharded_GetOrComputeWithCtx(t *testing.T) {
	h := NewShardedHandler(lruFactory(100), 4)
	defer h.Close()

	ctx := context.Background()
	calls := 0
	loader := func(c context.Context) ([]byte, error) {
		calls++
		return []byte("ctx-val"), nil
	}

	// 未命中 → loader 调用
	v, err := h.GetOrComputeWithCtx(ctx, []byte("ctx-key"), time.Minute, loader)
	require.NoError(t, err)
	assert.Equal(t, "ctx-val", string(v))
	assert.Equal(t, 1, calls)

	// 命中 → loader 不调用
	v, err = h.GetOrComputeWithCtx(ctx, []byte("ctx-key"), time.Minute, loader)
	require.NoError(t, err)
	assert.Equal(t, "ctx-val", string(v))
	assert.Equal(t, 1, calls)
}

func TestSharded_GetOrCompute_LoaderError(t *testing.T) {
	h := NewShardedHandler(lruFactory(100), 4)
	defer h.Close()

	// loader 返回错误时透传
	loaderErr := errors.New("loader fail")
	_, err := h.GetOrCompute([]byte("err-key"), time.Minute, func() ([]byte, error) {
		return nil, loaderErr
	})
	assert.ErrorIs(t, err, loaderErr)
}

// ============================================================
// Close 测试
// ============================================================

func TestSharded_Close(t *testing.T) {
	h := NewShardedHandler(lruFactory(100), 4)

	// Close 返回 nil（所有 shard 的 Close 均返回 nil）
	require.NoError(t, h.Close())

	// 重复 Close 幂等
	require.NoError(t, h.Close())
}

func TestSharded_CloseWithError(t *testing.T) {
	// 直接构造含错误 shard 的 ShardedHandler，覆盖 Close 的 lastErr 赋值分支
	h := &ShardedHandler{
		shards: []Handler{
			NewLRUHandler(10),
			&errCloseHandler{LRUHandler: NewLRUHandler(10), closeErr: errors.New("close fail")},
		},
		n: 2,
	}

	err := h.Close()
	require.Error(t, err)
	assert.Equal(t, "close fail", err.Error())
}
