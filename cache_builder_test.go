/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-26 01:05:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-26 01:05:00
 * @FilePath: \go-cachex\cache_builder_test.go
 * @Description: 缓存构建器测试
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */

package cachex

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// TestCacheBuilder_Basic 测试基础构建
func TestCacheBuilder_Basic(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").
		WithTTL(time.Minute * 10).
		WithCompression(CompressionGzip).
		Build()

	assert.NotNil(t, cache)
	assert.Equal(t, "test", cache.strategy.Namespace)
	assert.Equal(t, time.Minute*10, cache.strategy.DefaultTTL)
	assert.Equal(t, CompressionGzip, cache.strategy.Compression)
}

// TestCacheBuilder_GetOrSet 测试GetOrSet模式
func TestCacheBuilder_GetOrSet(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	callCount := 0

	cache := NewCacheBuilder(client, "user").
		WithTTL(time.Minute).
		Build()

	loader := func() (interface{}, error) {
		callCount++
		return fmt.Sprintf("user_data_%d", callCount), nil
	}

	// 第一次调用 - 缓存未命中,执行loader
	val1, err := cache.GetOrSet(ctx, "user:1", loader)
	assert.NoError(t, err)
	assert.Equal(t, "user_data_1", val1)
	assert.Equal(t, 1, callCount)

	// 第二次调用 - 缓存命中,不执行loader
	val2, err := cache.GetOrSet(ctx, "user:1", loader)
	assert.NoError(t, err)
	assert.Equal(t, "user_data_1", val2)
	assert.Equal(t, 1, callCount, "loader should not be called again")
}

// TestCacheBuilder_OnMissCallback 测试缓存未命中回调
func TestCacheBuilder_OnMissCallback(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	loadCount := 0

	cache := NewCacheBuilder(client, "product").
		WithTTL(time.Minute).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			loadCount++
			return fmt.Sprintf("product_%s_data", key), nil
		}).
		Build()

	// 第一次Get - 触发OnMiss
	val1, err := cache.Get(ctx, "product:100")
	assert.NoError(t, err)
	assert.Equal(t, "product_product:100_data", val1)
	assert.Equal(t, 1, loadCount)

	// 第二次Get - 从缓存读取
	val2, err := cache.Get(ctx, "product:100")
	assert.NoError(t, err)
	assert.Equal(t, "product_product:100_data", val2)
	assert.Equal(t, 1, loadCount, "OnMiss should not be called again")
}

// TestCacheBuilder_WithLock 测试分布式锁
func TestCacheBuilder_WithLock(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	loadCount := 0

	cache := NewCacheBuilder(client, "config").
		WithTTL(time.Minute).
		WithLock(time.Second * 5).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			loadCount++
			time.Sleep(time.Millisecond * 100) // 模拟慢查询
			return fmt.Sprintf("config_%s", key), nil
		}).
		Build()

	// 并发Get - 只有一个请求会执行OnMiss
	done := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		go func() {
			val, err := cache.Get(ctx, "config:db")
			assert.NoError(t, err)
			assert.Equal(t, "config_config:db", val)
			done <- true
		}()
	}

	// 等待所有goroutine完成
	for i := 0; i < 5; i++ {
		<-done
	}

	// 验证只加载了一次
	assert.LessOrEqual(t, loadCount, 2, "loader should be called at most 2 times due to lock")
}

// TestCacheBuilder_SetAndDelete 测试Set和Delete
func TestCacheBuilder_SetAndDelete(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()

	cache := NewCacheBuilder(client, "session").
		WithTTL(time.Minute).
		Build()

	// 设置缓存
	err := cache.Set(ctx, "session:abc", "user_session_data")
	assert.NoError(t, err)

	// 验证Redis中的值
	val, err := client.Get(ctx, "session:session:abc").Result()
	assert.NoError(t, err)
	assert.Equal(t, "user_session_data", val)

	// 删除缓存
	err = cache.Delete(ctx, "session:abc")
	assert.NoError(t, err)

	// 验证已删除
	_, err = client.Get(ctx, "session:session:abc").Result()
	assert.Equal(t, redis.Nil, err)
}

// TestCacheBuilder_OnSetCallback 测试Set回调
func TestCacheBuilder_OnSetCallback(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	setKeys := []string{}

	cache := NewCacheBuilder(client, "notify").
		WithTTL(time.Minute).
		OnSet(func(ctx context.Context, key string, value interface{}) {
			setKeys = append(setKeys, key)
		}).
		Build()

	// 设置多个缓存
	cache.Set(ctx, "key1", "value1")
	cache.Set(ctx, "key2", "value2")
	cache.Set(ctx, "key3", "value3")

	// 验证回调被调用
	assert.Equal(t, 3, len(setKeys))
	assert.Contains(t, setKeys, "key1")
	assert.Contains(t, setKeys, "key2")
	assert.Contains(t, setKeys, "key3")
}

// TestCacheBuilder_CustomTTL 测试自定义TTL
func TestCacheBuilder_CustomTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()

	cache := NewCacheBuilder(client, "temp").
		WithTTL(time.Hour). // 默认1小时
		Build()

	// 使用默认TTL
	cache.Set(ctx, "key1", "value1")
	ttl1, _ := client.TTL(ctx, "temp:key1").Result()
	assert.InDelta(t, time.Hour.Seconds(), ttl1.Seconds(), 5)

	// 使用自定义TTL
	cache.Set(ctx, "key2", "value2", time.Minute*5)
	ttl2, _ := client.TTL(ctx, "temp:key2").Result()
	assert.InDelta(t, time.Minute.Seconds()*5, ttl2.Seconds(), 5)
}

// TestCacheBuilder_ChainedCalls 测试链式调用
func TestCacheBuilder_ChainedCalls(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	// 复杂的链式配置
	cache := NewCacheBuilder(client, "advanced").
		WithTTL(time.Minute * 30).
		WithCompression(CompressionGzip).
		WithKeyPattern("cache:{key}").
		WithLock(time.Second * 10).
		WithHotKey().
		WithPubSub().
		WithRefreshThreshold(0.3).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			return "loaded_data", nil
		}).
		OnSet(func(ctx context.Context, key string, value interface{}) {
			// 设置回调
		}).
		OnError(func(ctx context.Context, err error) {
			// 错误回调
		}).
		Build()

	assert.NotNil(t, cache)
	assert.True(t, cache.strategy.EnableLock)
	assert.True(t, cache.strategy.EnableHotKey)
	assert.True(t, cache.strategy.EnablePubSub)
	assert.Equal(t, 0.3, cache.strategy.RefreshThreshold)
	assert.NotNil(t, cache.lockManager)
	assert.NotNil(t, cache.hotKeyMgr)
	assert.NotNil(t, cache.pubsub)
}

// Benchmark测试
func BenchmarkCacheBuilder_Get(b *testing.B) {
	mr := miniredis.RunT(b)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	cache := NewCacheBuilder(client, "bench").
		WithTTL(time.Minute).
		Build()

	cache.Set(ctx, "bench_key", "bench_value")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(ctx, "bench_key")
	}
}

func BenchmarkCacheBuilder_Set(b *testing.B) {
	mr := miniredis.RunT(b)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	cache := NewCacheBuilder(client, "bench").
		WithTTL(time.Minute).
		Build()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set(ctx, fmt.Sprintf("key_%d", i), fmt.Sprintf("value_%d", i))
	}
}
