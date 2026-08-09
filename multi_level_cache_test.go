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

// TestMultiLevelCache_Clear 测试清空所有缓存
func TestMultiLevelCache_Clear(t *testing.T) {
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

	// 设置一些数据
	cache.Set(ctx, "key1", "value1")
	cache.Set(ctx, "key2", "value2")

	// 验证 L1 有数据
	stats := cache.GetStats()
	assert.Greater(t, stats["l1_sets"], int64(0))

	// 清空缓存
	err := cache.Clear(ctx)
	assert.NoError(t, err)

	// 验证 L1 已清空
	cache.stats.RLock()
	l1Size := len(cache.l1Cache.shards[0].cache)
	cache.stats.RUnlock()
	t.Logf("L1 shard[0] size after clear: %d", l1Size)
}

// TestCachePattern_WriteThrough_DBError 测试 WriteThrough 在 DB 写入失败时返回错误
func TestCachePattern_WriteThrough_DBError(t *testing.T) {
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
	pattern := NewCachePattern(cache)

	dbWriter := func(val string) error {
		return fmt.Errorf("db write failed")
	}

	err := pattern.WriteThrough(ctx, "key1", "value1", dbWriter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db write failed")
}

// TestCachePattern_WriteBehind_SetError 测试 WriteBehind 在缓存设置失败时返回错误
func TestCachePattern_WriteBehind_SetError(t *testing.T) {
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
	pattern := NewCachePattern(cache)

	// 关闭 miniredis 使 Set 失败
	mr.Close()

	dbWriter := func(val string) error {
		return nil
	}

	err := pattern.WriteBehind(ctx, "key1", "value1", dbWriter)
	assert.Error(t, err)
}

// TestCachePattern_WriteBehind_DBError 测试 WriteBehind 在 DB 写入失败时删除缓存
func TestCachePattern_WriteBehind_DBError(t *testing.T) {
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
	pattern := NewCachePattern(cache)

	dbWriteCount := int32(0)
	dbWriter := func(val string) error {
		atomic.AddInt32(&dbWriteCount, 1)
		return fmt.Errorf("db write failed")
	}

	err := pattern.WriteBehind(ctx, "key1", "value1", dbWriter)
	assert.NoError(t, err, "WriteBehind 应立即返回成功")

	// 等待异步 DB 写入
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, int32(1), atomic.LoadInt32(&dbWriteCount), "DB writer 应被调用")
}

// TestCachePattern_CacheAside_WriteError 测试 CacheAside Write 在 DB 写入失败时返回错误
func TestCachePattern_CacheAside_WriteError(t *testing.T) {
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
	pattern := NewCachePattern(cache)

	dbLoader := func() (string, error) {
		return "db_value", nil
	}
	dbWriter := func(val string) error {
		return fmt.Errorf("write failed")
	}

	op := pattern.CacheAside(ctx, "key1", dbLoader, dbWriter)

	err := op.Write("value1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "write failed")
}

// TestMultiLevelCache_Get_NoLoader 测试 Get 在缓存未命中且无 loader 时返回错误
func TestMultiLevelCache_Get_NoLoader(t *testing.T) {
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

	_, err := cache.Get(ctx, "nonexistent", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no loader")
}

// TestMultiLevelCache_Get_LoaderError 测试 Get 在 loader 失败时返回错误
func TestMultiLevelCache_Get_LoaderError(t *testing.T) {
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

	loader := func() (string, error) {
		return "", fmt.Errorf("loader error")
	}

	_, err := cache.Get(ctx, "key1", loader)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "loader error")
}

// TestMultiLevelCache_Get_L1UnmarshalError 测试 Get 在 L1 缓存反序列化失败时回退到 L2
func TestMultiLevelCache_Get_L1UnmarshalError(t *testing.T) {
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

	// 向 L1 写入损坏的数据（无法反序列化为 int）
	cache.l1Cache.SetWithTTL([]byte("bad_key"), []byte("not_an_int"), time.Minute)

	loader := func() (int, error) {
		return 42, nil
	}

	// Get 应在 L1 反序列化失败时回退
	val, err := cache.Get(ctx, "bad_key", loader)
	assert.NoError(t, err)
	assert.Equal(t, 42, val)
}

// TestMultiLevelCache_CalculateHitRate 测试命中率计算
func TestMultiLevelCache_CalculateHitRate(t *testing.T) {
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

	loader := func() (string, error) {
		return "data", nil
	}

	// 无数据时命中率应为 0
	stats := cache.GetStats()
	assert.Equal(t, int64(0), stats["hit_rate"])

	// 第一次 Get - miss
	cache.Get(ctx, "key1", loader)
	// 第二次 Get - L1 hit
	cache.Get(ctx, "key1", loader)

	// 命中率 = 1 / (1+0+1) = 50%
	stats = cache.GetStats()
	assert.Equal(t, int64(50), stats["hit_rate"])
}

// TestMultiLevelCache_NewMultiLevelCache_Compression 测试创建启用压缩的缓存
func TestMultiLevelCache_NewMultiLevelCache_Compression(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	config := MultiLevelConfig{
		Namespace:         "test",
		L1Size:            100,
		L1TTL:             time.Minute,
		L2TTL:             time.Hour,
		EnableCompression: true,
	}
	cache := NewMultiLevelCache[string](client, config)
	assert.NotNil(t, cache)
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
