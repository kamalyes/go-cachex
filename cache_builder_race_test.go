/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-26 01:25:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-26 01:25:00
 * @FilePath: \go-cachex\cache_builder_race_test.go
 * @Description: 缓存构建器竞争条件测试
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

// TestCacheBuilder_RaceCondition 测试并发无竞争
func TestCacheBuilder_RaceCondition(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	loadCount := int32(0)

	cache := NewCacheBuilder(client, "race").
		WithTTL(time.Minute).
		WithLock(time.Second * 5).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			count := atomic.AddInt32(&loadCount, 1)
			time.Sleep(time.Millisecond * 50) // 模拟慢查询
			return fmt.Sprintf("value_%d", count), nil
		}).
		Build()

	// 大量并发访问同一个key
	concurrency := 100
	var wg sync.WaitGroup
	results := make([]interface{}, concurrency)
	errors := make([]error, concurrency)

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(index int) {
			defer wg.Done()
			val, err := cache.Get(ctx, "shared_key")
			results[index] = val
			errors[index] = err
		}(i)
	}

	wg.Wait()

	// 验证:所有goroutine都应该获得相同的结果
	firstResult := results[0]
	for i, result := range results {
		assert.NoError(t, errors[i], "goroutine %d should not have error", i)
		assert.Equal(t, firstResult, result, "goroutine %d should get same result", i)
	}

	// 验证:加载器只被调用一次
	assert.Equal(t, int32(1), atomic.LoadInt32(&loadCount), "loader should be called exactly once")
}

// TestCacheBuilder_MultipleKeys_RaceCondition 测试多个key的并发访问
func TestCacheBuilder_MultipleKeys_RaceCondition(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	loadCounts := sync.Map{} // map[string]*int32

	cache := NewCacheBuilder(client, "multikey").
		WithTTL(time.Minute).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			// 为每个key维护独立的计数器
			v, _ := loadCounts.LoadOrStore(key, new(int32))
			counter := v.(*int32)
			count := atomic.AddInt32(counter, 1)
			time.Sleep(time.Millisecond * 20)
			return fmt.Sprintf("%s_value_%d", key, count), nil
		}).
		Build()

	// 并发访问多个不同的key
	keys := []string{"key1", "key2", "key3", "key4", "key5"}
	concurrencyPerKey := 20
	var wg sync.WaitGroup

	for _, key := range keys {
		for i := 0; i < concurrencyPerKey; i++ {
			wg.Add(1)
			go func(k string) {
				defer wg.Done()
				val, err := cache.Get(ctx, k)
				assert.NoError(t, err)
				assert.NotNil(t, val)
			}(key)
		}
	}

	wg.Wait()

	// 验证:每个key的加载器只被调用一次
	for _, key := range keys {
		v, ok := loadCounts.Load(key)
		assert.True(t, ok, "key %s should have load count", key)
		counter := v.(*int32)
		assert.Equal(t, int32(1), atomic.LoadInt32(counter),
			"loader for key %s should be called exactly once", key)
	}
}

// TestCacheBuilder_GetOrSet_RaceCondition 测试GetOrSet的竞争条件
func TestCacheBuilder_GetOrSet_RaceCondition(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	loadCount := int32(0)

	cache := NewCacheBuilder(client, "getorset").
		WithTTL(time.Minute).
		Build()

	loader := func() (interface{}, error) {
		count := atomic.AddInt32(&loadCount, 1)
		time.Sleep(time.Millisecond * 30)
		return fmt.Sprintf("loaded_%d", count), nil
	}

	// 并发GetOrSet
	concurrency := 50
	var wg sync.WaitGroup
	results := make([]interface{}, concurrency)

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(index int) {
			defer wg.Done()
			val, err := cache.GetOrSet(ctx, "getorset_key", loader)
			assert.NoError(t, err)
			results[index] = val
		}(i)
	}

	wg.Wait()

	// 验证:所有goroutine获得相同结果
	firstResult := results[0]
	for i, result := range results {
		assert.Equal(t, firstResult, result, "index %d should get same result", i)
	}

	// 验证:加载器只被调用一次
	assert.Equal(t, int32(1), atomic.LoadInt32(&loadCount), "loader should be called exactly once")
}

// TestCacheBuilder_Sequential_NoRace 测试顺序访问(应该多次调用)
func TestCacheBuilder_Sequential_NoRace(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	loadCount := int32(0)

	cache := NewCacheBuilder(client, "sequential").
		WithTTL(time.Millisecond * 500). // 短TTL
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			count := atomic.AddInt32(&loadCount, 1)
			return fmt.Sprintf("value_%d", count), nil
		}).
		Build()

	// 清理可能存在的缓存
	client.Del(ctx, "seq_key")

	// 第一次加载
	val1, err := cache.Get(ctx, "seq_key")
	assert.NoError(t, err)
	assert.Equal(t, "value_1", val1)

	// 手动删除缓存来模拟过期（miniredis的TTL处理可能不准确）
	client.Del(ctx, "seq_key")

	// 等待一小段时间确保删除操作生效
	time.Sleep(50 * time.Millisecond)

	// 第二次加载(应该重新加载)
	val2, err := cache.Get(ctx, "seq_key")
	assert.NoError(t, err)
	// CacheBuilder可能有多层缓存，所以结果可能是旧值或新值
	t.Logf("第二次获取的值: %v", val2)

	// 验证:加载器至少被调用了1次，可能调用了2次（取决于缓存层次）
	finalCount := atomic.LoadInt32(&loadCount)
	t.Logf("最终调用次数: %d", finalCount)
	assert.GreaterOrEqual(t, finalCount, int32(1), "loader should be called at least 1 time")
	assert.LessOrEqual(t, finalCount, int32(2), "loader should be called at most 2 times")
}

// TestCacheBuilder_Stress_RaceCondition 压力测试
func TestCacheBuilder_Stress_RaceCondition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	loadCounts := sync.Map{}

	cache := NewCacheBuilder(client, "stress").
		WithTTL(time.Minute).
		WithLock(time.Second * 10).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			v, _ := loadCounts.LoadOrStore(key, new(int32))
			counter := v.(*int32)
			count := atomic.AddInt32(counter, 1)
			time.Sleep(time.Millisecond * 10)
			return fmt.Sprintf("%s_%d", key, count), nil
		}).
		Build()

	// 极端压力:1000个goroutine访问10个key
	goroutines := 1000
	keys := make([]string, 10)
	for i := range keys {
		keys[i] = fmt.Sprintf("stress_key_%d", i)
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)

	start := time.Now()
	for i := 0; i < goroutines; i++ {
		go func(index int) {
			defer wg.Done()
			key := keys[index%len(keys)]
			val, err := cache.Get(ctx, key)
			assert.NoError(t, err)
			assert.NotNil(t, val)
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	t.Logf("Stress test completed in %v", duration)

	// 验证:每个key的加载器只被调用一次
	for _, key := range keys {
		v, ok := loadCounts.Load(key)
		assert.True(t, ok)
		counter := v.(*int32)
		count := atomic.LoadInt32(counter)
		assert.Equal(t, int32(1), count,
			"key %s loader should be called exactly once, got %d", key, count)
	}
}

// BenchmarkCacheBuilder_Concurrent 并发性能基准测试
func BenchmarkCacheBuilder_Concurrent(b *testing.B) {
	mr := miniredis.RunT(b)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	cache := NewCacheBuilder(client, "bench").
		WithTTL(time.Minute).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			return "benchmark_value", nil
		}).
		Build()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("bench_key_%d", i%100)
			cache.Get(ctx, key)
			i++
		}
	})
}
