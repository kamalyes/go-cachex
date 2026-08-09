/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-19 00:00:00
 * @FilePath: \go-cachex\advanced_cache_test.go
 * @Description: 高级缓存包装器测试
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdvancedCache_NewAdvancedCache 测试创建高级缓存时的默认配置填充
func TestAdvancedCache_NewAdvancedCache(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("默认配置填充", func(t *testing.T) {
		config := AdvancedCacheConfig{
			Compression:   CompressionGzip,
			EnableMetrics: true,
		}
		cache := NewAdvancedCache[string](client, config)

		// 验证默认值
		assert.Equal(t, 1024, cache.config.MinSizeForCompress)
		assert.Equal(t, time.Hour, cache.config.DefaultTTL)
		assert.Equal(t, "cache", cache.config.Namespace)
		assert.NotNil(t, cache.config.Logger)
		assert.NotNil(t, cache.queue)
		assert.NotNil(t, cache.hotkey)
		assert.NotNil(t, cache.lockMgr)
		assert.NotNil(t, cache.metrics)
		assert.NotNil(t, cache.logger)
	})

	t.Run("自定义配置不被覆盖", func(t *testing.T) {
		config := AdvancedCacheConfig{
			MinSizeForCompress: 512,
			DefaultTTL:         30 * time.Minute,
			Namespace:          "custom",
			EnableMetrics:      false,
		}
		cache := NewAdvancedCache[string](client, config)
		assert.Equal(t, 512, cache.config.MinSizeForCompress)
		assert.Equal(t, 30*time.Minute, cache.config.DefaultTTL)
		assert.Equal(t, "custom", cache.config.Namespace)
	})
}

// TestAdvancedCache_Compress 测试压缩功能
func TestAdvancedCache_Compress(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("CompressionNone返回原始数据", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Compression: CompressionNone,
		})
		data := []byte("hello world")
		result, err := cache.compress(data)
		require.NoError(t, err)
		assert.Equal(t, data, result)
	})

	t.Run("数据小于MinSizeForCompress不压缩", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Compression:        CompressionGzip,
			MinSizeForCompress: 1024,
		})
		data := []byte("small data")
		result, err := cache.compress(data)
		require.NoError(t, err)
		assert.Equal(t, data, result)
	})

	t.Run("正常压缩", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Compression:        CompressionGzip,
			MinSizeForCompress: 10,
		})
		data := []byte(strings.Repeat("Hello, World! ", 100))
		result, err := cache.compress(data)
		require.NoError(t, err)
		assert.NotEqual(t, data, result)
		assert.Less(t, len(result), len(data))
		// gzip magic number: 0x1f 0x8b
		assert.Equal(t, []byte{0x1f, 0x8b}, result[:2])
	})

	t.Run("启用metrics时增加压缩计数", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Compression:        CompressionGzip,
			MinSizeForCompress: 10,
			EnableMetrics:      true,
		})
		data := []byte(strings.Repeat("Hello, World! ", 100))
		_, err := cache.compress(data)
		require.NoError(t, err)
		metrics := cache.GetMetrics()
		assert.Equal(t, int64(1), metrics.Compressions)
	})

	t.Run("未启用metrics时不增加压缩计数", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Compression:        CompressionGzip,
			MinSizeForCompress: 10,
			EnableMetrics:      false,
		})
		data := []byte(strings.Repeat("Hello, World! ", 100))
		_, err := cache.compress(data)
		require.NoError(t, err)
		assert.Nil(t, cache.GetMetrics())
	})
}

// TestAdvancedCache_Decompress 测试解压功能
func TestAdvancedCache_Decompress(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("CompressionNone返回原始数据", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Compression: CompressionNone,
		})
		data := []byte("hello world")
		result, err := cache.decompress(data)
		require.NoError(t, err)
		assert.Equal(t, data, result)
	})

	t.Run("非gzip数据返回原始数据", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Compression: CompressionGzip,
		})
		data := []byte("not gzip data")
		result, err := cache.decompress(data)
		require.NoError(t, err)
		assert.Equal(t, data, result)
	})

	t.Run("正常解压", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Compression:        CompressionGzip,
			MinSizeForCompress: 10,
		})
		original := []byte(strings.Repeat("Hello, World! ", 100))
		compressed, err := cache.compress(original)
		require.NoError(t, err)
		result, err := cache.decompress(compressed)
		require.NoError(t, err)
		assert.Equal(t, original, result)
	})

	t.Run("截断的gzip数据返回原始数据", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Compression: CompressionGzip,
		})
		// 创建有效的gzip数据
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, err := gz.Write([]byte(strings.Repeat("Hello, World! ", 100)))
		require.NoError(t, err)
		require.NoError(t, gz.Close())
		compressed := buf.Bytes()

		// 截断数据（移除尾部8字节：CRC32+ISIZE），使io.ReadAll返回错误
		truncated := compressed[:len(compressed)-8]
		result, err := cache.decompress(truncated)
		require.NoError(t, err)
		assert.Equal(t, truncated, result)
	})

	t.Run("启用metrics时增加解压计数", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Compression:        CompressionGzip,
			MinSizeForCompress: 10,
			EnableMetrics:      true,
		})
		original := []byte(strings.Repeat("Hello, World! ", 100))
		compressed, err := cache.compress(original)
		require.NoError(t, err)
		_, err = cache.decompress(compressed)
		require.NoError(t, err)
		metrics := cache.GetMetrics()
		assert.Equal(t, int64(1), metrics.Decompressions)
	})
}

// TestAdvancedCache_Set 测试设置缓存
func TestAdvancedCache_Set(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("正常设置", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "test"})
		err := cache.Set(context.Background(), "key1", "value1", time.Minute)
		require.NoError(t, err)
		// 验证数据已存储
		val, err := client.Get(context.Background(), "test:key1").Result()
		require.NoError(t, err)
		assert.NotEmpty(t, val)
	})

	t.Run("使用默认TTL", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:  "test",
			DefaultTTL: 30 * time.Minute,
		})
		err := cache.Set(context.Background(), "key2", "value2")
		require.NoError(t, err)
		ttl, err := cache.TTL(context.Background(), "key2")
		require.NoError(t, err)
		assert.True(t, ttl > 0)
	})

	t.Run("marshal失败", func(t *testing.T) {
		cache := NewAdvancedCache[chan int](client, AdvancedCacheConfig{})
		err := cache.Set(context.Background(), "key", make(chan int))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to marshal value")
	})

	t.Run("Redis错误", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "test"})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := cache.Set(ctx, "key", "value", time.Minute)
		assert.Error(t, err)
	})

	t.Run("启用metrics时增加设置计数", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:     "metrics",
			EnableMetrics: true,
		})
		err := cache.Set(context.Background(), "key1", "value1", time.Minute)
		require.NoError(t, err)
		metrics := cache.GetMetrics()
		assert.Equal(t, int64(1), metrics.Sets)
		assert.True(t, metrics.TotalSize > 0)
	})

	t.Run("Gzip压缩模式", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:          "gzip",
			Compression:        CompressionGzip,
			MinSizeForCompress: 10,
		})
		largeData := strings.Repeat("Hello, World! ", 100)
		err := cache.Set(context.Background(), "key", largeData, time.Minute)
		require.NoError(t, err)
		// 验证数据被压缩
		rawData, err := client.Get(context.Background(), "gzip:key").Bytes()
		require.NoError(t, err)
		assert.Less(t, len(rawData), len(largeData))
		assert.Equal(t, []byte{0x1f, 0x8b}, rawData[:2])
	})
}

// TestAdvancedCache_Get 测试获取缓存
func TestAdvancedCache_Get(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("缓存命中", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "test"})
		err := cache.Set(context.Background(), "key", "value", time.Minute)
		require.NoError(t, err)
		val, exists, err := cache.Get(context.Background(), "key")
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, "value", val)
	})

	t.Run("缓存未命中", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "test"})
		val, exists, err := cache.Get(context.Background(), "nonexistent")
		require.NoError(t, err)
		assert.False(t, exists)
		assert.Empty(t, val)
	})

	t.Run("Redis错误", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "test"})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := cache.Get(ctx, "key")
		assert.Error(t, err)
	})

	t.Run("unmarshal失败", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:   "test",
			Compression: CompressionNone,
		})
		// 直接存储非JSON数据
		require.NoError(t, client.Set(context.Background(), "test:badkey", "not-json", time.Minute).Err())
		_, _, err := cache.Get(context.Background(), "badkey")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal value")
	})

	t.Run("启用metrics时命中增加计数", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:     "metrics",
			EnableMetrics: true,
		})
		require.NoError(t, cache.Set(context.Background(), "key", "value", time.Minute))
		_, _, err := cache.Get(context.Background(), "key")
		require.NoError(t, err)
		metrics := cache.GetMetrics()
		assert.Equal(t, int64(1), metrics.Hits)
	})

	t.Run("启用metrics时未命中增加计数", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:     "metrics2",
			EnableMetrics: true,
		})
		_, _, err := cache.Get(context.Background(), "nonexistent")
		require.NoError(t, err)
		metrics := cache.GetMetrics()
		assert.Equal(t, int64(1), metrics.Misses)
	})

	t.Run("Gzip压缩模式往返", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:          "gzip",
			Compression:        CompressionGzip,
			MinSizeForCompress: 10,
		})
		largeData := strings.Repeat("Hello, World! ", 100)
		err := cache.Set(context.Background(), "key", largeData, time.Minute)
		require.NoError(t, err)
		val, exists, err := cache.Get(context.Background(), "key")
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, largeData, val)
	})

	t.Run("Gzip模式小数据不压缩往返", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:          "gzip",
			Compression:        CompressionGzip,
			MinSizeForCompress: 1024,
		})
		smallData := "small"
		err := cache.Set(context.Background(), "smallkey", smallData, time.Minute)
		require.NoError(t, err)
		// 验证数据未被压缩（应该是JSON格式，以双引号开头）
		rawData, err := client.Get(context.Background(), "gzip:smallkey").Bytes()
		require.NoError(t, err)
		assert.Equal(t, '"', rune(rawData[0]))
		val, exists, err := cache.Get(context.Background(), "smallkey")
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, smallData, val)
	})
}

// TestAdvancedCache_GetOrSet 测试获取或设置缓存
func TestAdvancedCache_GetOrSet(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("缓存命中直接返回", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "getorset"})
		ctx := context.Background()
		require.NoError(t, cache.Set(ctx, "key", "cached", time.Minute))

		fnCalled := false
		val, err := cache.GetOrSet(ctx, "key", func() (string, error) {
			fnCalled = true
			return "fn-result", nil
		}, time.Minute)
		require.NoError(t, err)
		assert.Equal(t, "cached", val)
		assert.False(t, fnCalled, "缓存命中时fn不应被调用")
	})

	t.Run("缓存未命中执行fn并设置", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "getorset"})
		ctx := context.Background()

		fnCalled := false
		val, err := cache.GetOrSet(ctx, "newkey", func() (string, error) {
			fnCalled = true
			return "fn-result", nil
		}, time.Minute)
		require.NoError(t, err)
		assert.Equal(t, "fn-result", val)
		assert.True(t, fnCalled)

		// 验证缓存已设置
		cached, exists, err := cache.Get(ctx, "newkey")
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, "fn-result", cached)
	})

	t.Run("锁失败直接执行fn", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "getorset"})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		fnCalled := false
		val, err := cache.GetOrSet(ctx, "lockfail", func() (string, error) {
			fnCalled = true
			return "fn-result", nil
		}, time.Minute)
		require.NoError(t, err)
		assert.Equal(t, "fn-result", val)
		assert.True(t, fnCalled, "锁失败时fn应被调用")
	})

	t.Run("fn返回错误", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "getorset"})
		ctx := context.Background()
		fnErr := errors.New("fn error")

		val, err := cache.GetOrSet(ctx, "errkey", func() (string, error) {
			return "", fnErr
		}, time.Minute)
		assert.Equal(t, fnErr, err)
		assert.Empty(t, val)

		// 验证缓存未设置（fn返回错误时不设置缓存）
		_, exists, _ := cache.Get(ctx, "errkey")
		assert.False(t, exists)
	})

	t.Run("Set失败时记录错误但返回值", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "getorset"})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 在fn中取消context，使后续Set调用失败
		val, err := cache.GetOrSet(ctx, "setfail", func() (string, error) {
			cancel() // 取消context使后续Set失败
			return "fn-result", nil
		}, time.Minute)
		require.NoError(t, err)
		assert.Equal(t, "fn-result", val)
	})

	t.Run("第二次检查命中", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace: "getorset2",
		})
		ctx := context.Background()
		key := "second-check"

		// 设置锁键以阻塞GetOrSet的Lock调用
		// 锁的Redis键格式: <namespace>:getorset:<key>
		lockRedisKey := "getorset2:getorset:" + key
		require.NoError(t, client.Set(ctx, lockRedisKey, "blocking-token", time.Minute).Err())

		fnCalled := int32(0)
		done := make(chan struct{})
		var result string
		var resultErr error

		go func() {
			defer close(done)
			result, resultErr = cache.GetOrSet(ctx, key, func() (string, error) {
				atomic.AddInt32(&fnCalled, 1)
				return "fn-result", nil
			}, time.Minute)
		}()

		// 等待GetOrSet进入Lock等待
		time.Sleep(200 * time.Millisecond)

		// 设置缓存值（模拟另一个协程在锁等待期间设置了缓存）
		require.NoError(t, cache.Set(ctx, key, "cached-value", time.Minute))

		// 释放锁，让GetOrSet继续执行
		require.NoError(t, client.Del(ctx, lockRedisKey).Err())

		// 等待GetOrSet完成
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("GetOrSet 超时")
		}

		require.NoError(t, resultErr)
		assert.Equal(t, "cached-value", result)
		assert.Equal(t, int32(0), atomic.LoadInt32(&fnCalled), "第二次检查命中时fn不应被调用")
	})
}

// TestAdvancedCache_Delete 测试删除缓存
func TestAdvancedCache_Delete(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("空keys", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "test"})
		err := cache.Delete(context.Background())
		assert.NoError(t, err)
	})

	t.Run("正常删除", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "test"})
		ctx := context.Background()
		require.NoError(t, cache.Set(ctx, "key1", "value1", time.Minute))
		require.NoError(t, cache.Set(ctx, "key2", "value2", time.Minute))
		err := cache.Delete(ctx, "key1", "key2")
		assert.NoError(t, err)
		exists, err := cache.Exists(ctx, "key1", "key2")
		require.NoError(t, err)
		assert.Equal(t, int64(0), exists)
	})

	t.Run("Redis错误", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "test"})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := cache.Delete(ctx, "key1")
		assert.Error(t, err)
	})

	t.Run("启用metrics时增加删除计数", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:     "metrics",
			EnableMetrics: true,
		})
		ctx := context.Background()
		require.NoError(t, cache.Set(ctx, "key1", "value1", time.Minute))
		require.NoError(t, cache.Set(ctx, "key2", "value2", time.Minute))
		require.NoError(t, cache.Delete(ctx, "key1", "key2"))
		metrics := cache.GetMetrics()
		assert.Equal(t, int64(2), metrics.Deletes)
	})
}

// TestAdvancedCache_Exists 测试检查键是否存在
func TestAdvancedCache_Exists(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "test"})
	ctx := context.Background()

	t.Run("空keys", func(t *testing.T) {
		count, err := cache.Exists(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
	})

	t.Run("键存在", func(t *testing.T) {
		require.NoError(t, cache.Set(ctx, "exists1", "value1", time.Minute))
		require.NoError(t, cache.Set(ctx, "exists2", "value2", time.Minute))
		count, err := cache.Exists(ctx, "exists1", "exists2")
		require.NoError(t, err)
		assert.Equal(t, int64(2), count)
	})

	t.Run("键不存在", func(t *testing.T) {
		count, err := cache.Exists(ctx, "nonexistent")
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
	})
}

// TestAdvancedCache_TTL 测试获取键的TTL
func TestAdvancedCache_TTL(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "test"})
	ctx := context.Background()

	require.NoError(t, cache.Set(ctx, "key", "value", 30*time.Second))
	ttl, err := cache.TTL(ctx, "key")
	require.NoError(t, err)
	assert.True(t, ttl > 0 && ttl <= 30*time.Second)
}

// TestAdvancedCache_Expire 测试设置过期时间
func TestAdvancedCache_Expire(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "test"})
	ctx := context.Background()

	require.NoError(t, cache.Set(ctx, "key", "value", time.Hour))
	err := cache.Expire(ctx, "key", 10*time.Second)
	require.NoError(t, err)
	ttl, err := cache.TTL(ctx, "key")
	require.NoError(t, err)
	assert.True(t, ttl > 0 && ttl <= 10*time.Second)
}

// TestAdvancedCache_Keys 测试获取匹配模式的键
func TestAdvancedCache_Keys(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("匹配模式并移除前缀", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "keys"})
		ctx := context.Background()
		require.NoError(t, cache.Set(ctx, "user:1", "v1", time.Minute))
		require.NoError(t, cache.Set(ctx, "user:2", "v2", time.Minute))
		require.NoError(t, cache.Set(ctx, "post:1", "v3", time.Minute))

		keys, err := cache.Keys(ctx, "user:*")
		require.NoError(t, err)
		assert.Len(t, keys, 2)
		assert.Contains(t, keys, "user:1")
		assert.Contains(t, keys, "user:2")
	})

	t.Run("Redis错误", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "keys"})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := cache.Keys(ctx, "*")
		assert.Error(t, err)
	})
}

// TestAdvancedCache_Clear 测试清空缓存
func TestAdvancedCache_Clear(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("正常清空", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "clear"})
		ctx := context.Background()
		require.NoError(t, cache.Set(ctx, "key1", "v1", time.Minute))
		require.NoError(t, cache.Set(ctx, "key2", "v2", time.Minute))
		err := cache.Clear(ctx, "*")
		require.NoError(t, err)
		keys, err := cache.Keys(ctx, "*")
		require.NoError(t, err)
		assert.Empty(t, keys)
	})

	t.Run("无匹配键", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "empty"})
		ctx := context.Background()
		err := cache.Clear(ctx, "*")
		assert.NoError(t, err)
	})

	t.Run("Keys错误", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "clear"})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := cache.Clear(ctx, "*")
		assert.Error(t, err)
	})
}

// TestAdvancedCache_GetManagers 测试获取内部组件
func TestAdvancedCache_GetManagers(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	cache := NewAdvancedCache[string](client, AdvancedCacheConfig{})

	assert.NotNil(t, cache.GetQueue())
	assert.NotNil(t, cache.GetHotKeyManager())
	assert.NotNil(t, cache.GetLockManager())
}

// TestAdvancedCache_Metrics 测试指标统计
func TestAdvancedCache_Metrics(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("未启用metrics返回nil", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{EnableMetrics: false})
		assert.Nil(t, cache.GetMetrics())
	})

	t.Run("启用metrics返回快照", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:     "metrics",
			EnableMetrics: true,
		})
		ctx := context.Background()
		require.NoError(t, cache.Set(ctx, "key", "value", time.Minute))
		_, _, err := cache.Get(ctx, "key")
		require.NoError(t, err)
		_, _, err = cache.Get(ctx, "nonexistent")
		require.NoError(t, err)

		metrics := cache.GetMetrics()
		require.NotNil(t, metrics)
		assert.Equal(t, int64(1), metrics.Sets)
		assert.Equal(t, int64(1), metrics.Hits)
		assert.Equal(t, int64(1), metrics.Misses)
	})

	t.Run("重置metrics", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:     "reset",
			EnableMetrics: true,
		})
		ctx := context.Background()
		require.NoError(t, cache.Set(ctx, "key", "value", time.Minute))
		_, _, err := cache.Get(ctx, "key")
		require.NoError(t, err)

		// 验证metrics非零
		metrics := cache.GetMetrics()
		require.NotNil(t, metrics)
		assert.True(t, metrics.Hits > 0)

		// 重置
		cache.ResetMetrics()

		// 验证已重置
		metrics = cache.GetMetrics()
		require.NotNil(t, metrics)
		assert.Equal(t, int64(0), metrics.Hits)
		assert.Equal(t, int64(0), metrics.Misses)
		assert.Equal(t, int64(0), metrics.Sets)
		assert.Equal(t, int64(0), metrics.Deletes)
		assert.Equal(t, int64(0), metrics.Compressions)
		assert.Equal(t, int64(0), metrics.Decompressions)
		assert.Equal(t, int64(0), metrics.TotalSize)
	})

	t.Run("未启用metrics时ResetMetrics为noop", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{EnableMetrics: false})
		// 不应panic
		cache.ResetMetrics()
		assert.Nil(t, cache.GetMetrics())
	})
}

// TestAdvancedCache_Ping 测试连接
func TestAdvancedCache_Ping(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	cache := NewAdvancedCache[string](client, AdvancedCacheConfig{})
	err := cache.Ping(context.Background())
	assert.NoError(t, err)
}

// TestAdvancedCache_Close 测试关闭缓存
func TestAdvancedCache_Close(t *testing.T) {
	client := setupRedisClient(t)
	cache := NewAdvancedCache[string](client, AdvancedCacheConfig{})
	err := cache.Close()
	assert.NoError(t, err)
}

// TestAdvancedCache_Wrap 测试包装函数
func TestAdvancedCache_Wrap(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "wrap"})
	ctx := context.Background()

	callCount := int32(0)
	wrappedFn := cache.Wrap("mykey", func(ctx context.Context) (string, error) {
		atomic.AddInt32(&callCount, 1)
		return "result", nil
	}, time.Minute)

	// 第一次调用，fn应被执行
	result, err := wrappedFn(ctx)
	require.NoError(t, err)
	assert.Equal(t, "result", result)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 第二次调用，应从缓存获取
	result, err = wrappedFn(ctx)
	require.NoError(t, err)
	assert.Equal(t, "result", result)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount), "第二次应从缓存获取")
}

// TestAdvancedCache_BatchGet 测试批量获取
func TestAdvancedCache_BatchGet(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("空keys", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "batch"})
		result, err := cache.BatchGet(context.Background(), []string{})
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("正常批量获取", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "batch"})
		ctx := context.Background()
		require.NoError(t, cache.Set(ctx, "key1", "value1", time.Minute))
		require.NoError(t, cache.Set(ctx, "key2", "value2", time.Minute))
		result, err := cache.BatchGet(ctx, []string{"key1", "key2"})
		require.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "value1", result["key1"])
		assert.Equal(t, "value2", result["key2"])
	})

	t.Run("部分不存在", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "batch"})
		ctx := context.Background()
		require.NoError(t, cache.Set(ctx, "key1", "value1", time.Minute))
		result, err := cache.BatchGet(ctx, []string{"key1", "nonexistent"})
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "value1", result["key1"])
	})

	t.Run("Pipeline错误", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "batch"})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := cache.BatchGet(ctx, []string{"key1", "key2"})
		assert.Error(t, err)
	})

	t.Run("unmarshal失败跳过", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:   "batch",
			Compression: CompressionNone,
		})
		ctx := context.Background()
		// 设置有效数据
		require.NoError(t, cache.Set(ctx, "valid", "value", time.Minute))
		// 直接存储非JSON数据
		require.NoError(t, client.Set(ctx, "batch:invalid", "not-json", time.Minute).Err())
		result, err := cache.BatchGet(ctx, []string{"valid", "invalid"})
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "value", result["valid"])
	})

	t.Run("Gzip压缩模式批量获取", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:          "gzip",
			Compression:        CompressionGzip,
			MinSizeForCompress: 10,
		})
		ctx := context.Background()
		largeData := strings.Repeat("Hello, World! ", 100)
		require.NoError(t, cache.Set(ctx, "key1", largeData, time.Minute))
		require.NoError(t, cache.Set(ctx, "key2", largeData, time.Minute))
		result, err := cache.BatchGet(ctx, []string{"key1", "key2"})
		require.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, largeData, result["key1"])
		assert.Equal(t, largeData, result["key2"])
	})
}

// TestAdvancedCache_BatchSet 测试批量设置
func TestAdvancedCache_BatchSet(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("空items", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "batch"})
		err := cache.BatchSet(context.Background(), map[string]string{})
		assert.NoError(t, err)
	})

	t.Run("正常批量设置", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "batch"})
		ctx := context.Background()
		items := map[string]string{
			"key1": "value1",
			"key2": "value2",
		}
		err := cache.BatchSet(ctx, items, time.Minute)
		require.NoError(t, err)
		val, exists, err := cache.Get(ctx, "key1")
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, "value1", val)
	})

	t.Run("marshal失败跳过", func(t *testing.T) {
		cache := NewAdvancedCache[interface{}](client, AdvancedCacheConfig{Namespace: "batch"})
		ctx := context.Background()
		items := map[string]interface{}{
			"valid":   "hello",
			"invalid": make(chan int),
		}
		err := cache.BatchSet(ctx, items, time.Minute)
		require.NoError(t, err)
		// 有效键应该被设置
		val, exists, err := cache.Get(ctx, "valid")
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, "hello", val)
		// 无效键不应该被设置
		_, exists, err = cache.Get(ctx, "invalid")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("Pipeline错误", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{Namespace: "batch"})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		items := map[string]string{"key1": "value1"}
		err := cache.BatchSet(ctx, items, time.Minute)
		assert.Error(t, err)
	})

	t.Run("使用默认TTL", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:  "batch",
			DefaultTTL: 30 * time.Minute,
		})
		ctx := context.Background()
		items := map[string]string{"key1": "value1"}
		err := cache.BatchSet(ctx, items)
		require.NoError(t, err)
		ttl, err := cache.TTL(ctx, "key1")
		require.NoError(t, err)
		assert.True(t, ttl > 0)
	})

	t.Run("Gzip压缩模式批量设置", func(t *testing.T) {
		cache := NewAdvancedCache[string](client, AdvancedCacheConfig{
			Namespace:          "gzip",
			Compression:        CompressionGzip,
			MinSizeForCompress: 10,
		})
		ctx := context.Background()
		largeData := strings.Repeat("Hello, World! ", 100)
		items := map[string]string{
			"key1": largeData,
			"key2": largeData,
		}
		err := cache.BatchSet(ctx, items, time.Minute)
		require.NoError(t, err)
		// 验证数据被压缩
		rawData, err := client.Get(ctx, "gzip:key1").Bytes()
		require.NoError(t, err)
		assert.Less(t, len(rawData), len(largeData))
		assert.Equal(t, []byte{0x1f, 0x8b}, rawData[:2])
	})
}
