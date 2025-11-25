/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-26 01:20:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-26 01:20:00
 * @FilePath: \go-cachex\multi_level_cache_test.go
 * @Description: 多级缓存测试
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */

package cachex

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// TestMultiLevelCache_L1Hit 测试L1缓存命中
func TestMultiLevelCache_L1Hit(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()
	loadCount := int32(0)

	config := MultiLevelConfig{
		Namespace: "test",
		L1Size:    100,
		L1TTL:     time.Minute,
		L2TTL:     time.Hour,
	}
	cache := NewMultiLevelCache[string](client, config)

	loader := func() (string, error) {
		atomic.AddInt32(&loadCount, 1)
		return "loaded_value", nil
	}

	// 第一次Get - 加载数据
	val1, err := cache.Get(ctx, "key1", loader)
	assert.NoError(t, err)
	assert.Equal(t, "loaded_value", val1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&loadCount))

	// 第二次Get - L1命中
	val2, err := cache.Get(ctx, "key1", loader)
	assert.NoError(t, err)
	assert.Equal(t, "loaded_value", val2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&loadCount), "should hit L1 cache")

	// 验证统计
	stats := cache.GetStats()
	t.Logf("Statistics: %+v", stats)
	assert.Equal(t, int64(1), stats["l1_hits"], "L1 hits should be 1")
	assert.Equal(t, int64(1), stats["misses"], "Misses should be 1")
}

// TestMultiLevelCache_L2Hit 测试L2缓存命中
func TestMultiLevelCache_L2Hit(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()
	loadCount := int32(0)

	config := MultiLevelConfig{
		Namespace: "test",
		L1Size:    100,
		L1TTL:     time.Minute,
		L2TTL:     time.Hour,
	}
	cache := NewMultiLevelCache[string](client, config)

	loader := func() (string, error) {
		atomic.AddInt32(&loadCount, 1)
		return "loaded_value", nil
	}

	// 第一次Get - 加载数据
	val1, err := cache.Get(ctx, "key1", loader)
	assert.NoError(t, err)
	assert.Equal(t, "loaded_value", val1)

	// 使L1失效
	cache.InvalidateL1("key1")

	// 第二次Get - L2命中
	val2, err := cache.Get(ctx, "key1", loader)
	assert.NoError(t, err)
	assert.Equal(t, "loaded_value", val2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&loadCount), "should hit L2 cache")

	// 验证统计
	stats := cache.GetStats()
	assert.Equal(t, int64(1), stats["l2_hits"])
}

// TestMultiLevelCache_SetAndDelete 测试设置和删除
func TestMultiLevelCache_SetAndDelete(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()

	config := MultiLevelConfig{
		Namespace: "test",
		L1Size:    100,
		L1TTL:     time.Minute,
		L2TTL:     time.Hour,
	}
	cache := NewMultiLevelCache[string](client, config)

	// 设置缓存
	err := cache.Set(ctx, "key1", "value1")
	assert.NoError(t, err)

	// 从L1读取
	val1, err := cache.l1Cache.Get([]byte("key1"))
	assert.NoError(t, err)
	assert.NotEmpty(t, val1)

	// 从L2读取
	val2, exists, err := cache.l2Cache.Get(ctx, "key1")
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "value1", val2)

	// 删除缓存
	err = cache.Delete(ctx, "key1")
	assert.NoError(t, err)

	// 验证L1已删除
	_, err = cache.l1Cache.Get([]byte("key1"))
	assert.Error(t, err)

	// 验证L2已删除
	_, exists, err = cache.l2Cache.Get(ctx, "key1")
	assert.NoError(t, err)
	assert.False(t, exists)
}

// TestMultiLevelCache_Stats 测试统计功能
func TestMultiLevelCache_Stats(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()

	config := MultiLevelConfig{
		Namespace: "test",
		L1Size:    100,
		L1TTL:     time.Minute,
		L2TTL:     time.Hour,
	}
	cache := NewMultiLevelCache[int](client, config)

	loader := func() (int, error) {
		return 100, nil
	}

	// 第1次 - Miss + Load
	cache.Get(ctx, "key1", loader)

	// 第2次 - L1 Hit
	cache.Get(ctx, "key1", loader)

	// 使L1失效
	cache.InvalidateL1("key1")

	// 第3次 - L2 Hit
	cache.Get(ctx, "key1", loader)

	// 第4次 - Miss + Load
	cache.Get(ctx, "key2", loader)

	// 验证统计
	stats := cache.GetStats()
	t.Logf("Statistics: %+v", stats)
	assert.Equal(t, int64(1), stats["l1_hits"], "L1 hits should be 1")
	assert.Equal(t, int64(1), stats["l2_hits"], "L2 hits should be 1")
	assert.Equal(t, int64(2), stats["misses"], "Misses should be 2")
	assert.Equal(t, int64(50), stats["hit_rate"], "Hit rate should be 50%") // (1+1)/(1+1+2) = 50%
}

// TestMultiLevelCache_Compression 测试压缩功能
func TestMultiLevelCache_Compression(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()

	config := MultiLevelConfig{
		Namespace:         "test",
		L1Size:            100,
		L1TTL:             time.Minute,
		L2TTL:             time.Hour,
		EnableCompression: true,
	}
	cache := NewMultiLevelCache[string](client, config)

	// 设置一个较大的值
	largeValue := string(make([]byte, 10000))
	err := cache.Set(ctx, "large_key", largeValue)
	assert.NoError(t, err)

	// 读取验证
	val, err := cache.Get(ctx, "large_key", nil)
	assert.NoError(t, err)
	assert.Equal(t, len(largeValue), len(val))
}

// TestCachePattern_CacheAside 测试旁路缓存模式
func TestCachePattern_CacheAside(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()
	dbValue := "db_value"
	loadCount := int32(0)
	writeCount := int32(0)

	config := MultiLevelConfig{
		Namespace: "test",
		L1Size:    100,
		L1TTL:     time.Minute,
		L2TTL:     time.Hour,
	}
	cache := NewMultiLevelCache[string](client, config)
	pattern := NewCachePattern(cache)

	dbLoader := func() (string, error) {
		atomic.AddInt32(&loadCount, 1)
		return dbValue, nil
	}

	dbWriter := func(val string) error {
		atomic.AddInt32(&writeCount, 1)
		dbValue = val
		return nil
	}

	op := pattern.CacheAside(ctx, "key1", dbLoader, dbWriter)

	// 读取 - 从DB加载
	val1, err := op.Read()
	assert.NoError(t, err)
	assert.Equal(t, "db_value", val1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&loadCount))

	// 写入 - 写DB并删除缓存
	err = op.Write("new_value")
	assert.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&writeCount))
	assert.Equal(t, "new_value", dbValue)

	// 等待延迟删除
	time.Sleep(time.Millisecond * 600)

	// 再次读取 - 从DB加载新值
	val2, err := op.Read()
	assert.NoError(t, err)
	assert.Equal(t, "new_value", val2)
	assert.Equal(t, int32(2), atomic.LoadInt32(&loadCount))
}

// TestCachePattern_ReadThrough 测试穿透读模式
func TestCachePattern_ReadThrough(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()
	loadCount := int32(0)

	config := MultiLevelConfig{
		Namespace: "test",
		L1Size:    100,
		L1TTL:     time.Minute,
		L2TTL:     time.Hour,
	}
	cache := NewMultiLevelCache[string](client, config)
	pattern := NewCachePattern(cache)

	dbLoader := func() (string, error) {
		atomic.AddInt32(&loadCount, 1)
		return "db_data", nil
	}

	// 第一次读 - 加载
	val1, err := pattern.ReadThrough(ctx, "key1", dbLoader)
	assert.NoError(t, err)
	assert.Equal(t, "db_data", val1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&loadCount))

	// 第二次读 - 缓存命中
	val2, err := pattern.ReadThrough(ctx, "key1", dbLoader)
	assert.NoError(t, err)
	assert.Equal(t, "db_data", val2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&loadCount))
}

// TestCachePattern_WriteThrough 测试穿透写模式
func TestCachePattern_WriteThrough(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()
	dbValue := ""
	writeCount := int32(0)

	config := MultiLevelConfig{
		Namespace: "test",
		L1Size:    100,
		L1TTL:     time.Minute,
		L2TTL:     time.Hour,
	}
	cache := NewMultiLevelCache[string](client, config)
	pattern := NewCachePattern(cache)

	dbWriter := func(val string) error {
		atomic.AddInt32(&writeCount, 1)
		dbValue = val
		return nil
	}

	// 写入
	err := pattern.WriteThrough(ctx, "key1", "new_value", dbWriter)
	assert.NoError(t, err)
	assert.Equal(t, "new_value", dbValue)
	assert.Equal(t, int32(1), atomic.LoadInt32(&writeCount))

	// 验证缓存
	val, err := cache.Get(ctx, "key1", nil)
	assert.NoError(t, err)
	assert.Equal(t, "new_value", val)
}

// TestCachePattern_WriteBehind 测试异步写模式
func TestCachePattern_WriteBehind(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()
	dbValue := ""
	writeCount := int32(0)
	var wg sync.WaitGroup

	config := MultiLevelConfig{
		Namespace: "test",
		L1Size:    100,
		L1TTL:     time.Minute,
		L2TTL:     time.Hour,
	}
	cache := NewMultiLevelCache[string](client, config)
	pattern := NewCachePattern(cache)

	wg.Add(1) // 为goroutine计数

	dbWriter := func(val string) error {
		defer wg.Done()
		time.Sleep(time.Millisecond * 100) // 模拟慢写
		atomic.AddInt32(&writeCount, 1)
		dbValue = val
		return nil
	}

	// 写入 - 立即返回
	err := pattern.WriteBehind(ctx, "key1", "async_value", dbWriter)
	assert.NoError(t, err)

	// 缓存立即可用
	val, err := cache.Get(ctx, "key1", nil)
	assert.NoError(t, err)
	assert.Equal(t, "async_value", val)

	// DB可能还未写入
	assert.Equal(t, int32(0), atomic.LoadInt32(&writeCount))

	// 等待异步写入完成
	wg.Wait()
	assert.Equal(t, int32(1), atomic.LoadInt32(&writeCount))
	assert.Equal(t, "async_value", dbValue)
}

// Benchmark测试
func BenchmarkMultiLevelCache_L1Hit(b *testing.B) {
	mr := miniredis.RunT(b)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()
	config := MultiLevelConfig{
		Namespace: "bench",
		L1Size:    1000,
		L1TTL:     time.Minute,
		L2TTL:     time.Hour,
	}
	cache := NewMultiLevelCache[string](client, config)

	// 预热缓存
	cache.Set(ctx, "bench_key", "bench_value")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(ctx, "bench_key", nil)
	}
}

func BenchmarkMultiLevelCache_Set(b *testing.B) {
	mr := miniredis.RunT(b)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()
	config := MultiLevelConfig{
		Namespace: "bench",
		L1Size:    1000,
		L1TTL:     time.Minute,
		L2TTL:     time.Hour,
	}
	cache := NewMultiLevelCache[string](client, config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set(ctx, fmt.Sprintf("key_%d", i), fmt.Sprintf("value_%d", i))
	}
}
