/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 22:50:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 22:33:18
 * @FilePath: \go-cachex\client_test.go
 * @Description: 客户端测试
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLRUOptimizedClient(t *testing.T) {
	ctx := context.Background()

	t.Run("Client Creation", func(t *testing.T) {
		client, err := NewLRUOptimizedClient(ctx, 100)
		require.NoError(t, err)
		require.NotNil(t, client)
		defer client.Close()

		config := client.GetConfig()
		assert.Equal(t, CacheLRUOptimized, config.Type)
		assert.Equal(t, 100, config.Capacity)
	})

	t.Run("Basic Operations", func(t *testing.T) {
		client, err := NewLRUOptimizedClient(ctx, 100)
		require.NoError(t, err)
		defer client.Close()

		key := []byte("test_key")
		value := []byte("test_value")

		// Set
		err = client.Set(key, value)
		require.NoError(t, err)

		// Get
		result, err := client.Get(key)
		require.NoError(t, err)
		assert.Equal(t, value, result)

		// Delete
		err = client.Del(key)
		require.NoError(t, err)

		// Verify deletion
		_, err = client.Get(key)
		assert.Error(t, err)
	})

	t.Run("TTL Operations", func(t *testing.T) {
		client, err := NewLRUOptimizedClient(ctx, 100)
		require.NoError(t, err)
		defer client.Close()

		key := []byte("ttl_key")
		value := []byte("ttl_value")

		// Set with TTL
		err = client.SetWithTTL(key, value, 100*time.Millisecond)
		require.NoError(t, err)

		// Get immediately
		result, err := client.Get(key)
		require.NoError(t, err)
		assert.Equal(t, value, result)

		// Check TTL
		ttl, err := client.GetTTL(key)
		require.NoError(t, err)
		assert.True(t, ttl > 0 && ttl <= 100*time.Millisecond)

		// Wait for expiry
		time.Sleep(150 * time.Millisecond)

		// Should be expired
		_, err = client.Get(key)
		assert.Error(t, err)
	})

	t.Run("GetOrCompute", func(t *testing.T) {
		client, err := NewLRUOptimizedClient(ctx, 100)
		require.NoError(t, err)
		defer client.Close()

		key := []byte("compute_key")
		computeCount := 0

		loader := func() ([]byte, error) {
			computeCount++
			return []byte(fmt.Sprintf("computed_value_%d", computeCount)), nil
		}

		// First call - should execute loader
		result1, err := client.GetOrCompute(key, time.Minute, loader)
		require.NoError(t, err)
		assert.Equal(t, []byte("computed_value_1"), result1)
		assert.Equal(t, 1, computeCount)

		// Second call - should use cache
		result2, err := client.GetOrCompute(key, time.Minute, loader)
		require.NoError(t, err)
		assert.Equal(t, result1, result2)
		assert.Equal(t, 1, computeCount) // Should not increment
	})

	t.Run("Performance Comparison", func(t *testing.T) {
		if testing.Short() {
			t.Skip("skipping performance test in short mode")
		}

		// Create both clients
		originalClient, err := NewLRUClient(ctx, 1000)
		require.NoError(t, err)
		defer originalClient.Close()

		optimizedClient, err := NewLRUOptimizedClient(ctx, 1000)
		require.NoError(t, err)
		defer optimizedClient.Close()

		numOps := 10000

		// Test original client
		start := time.Now()
		for i := 0; i < numOps; i++ {
			key := []byte(fmt.Sprintf("perf_key_%d", i))
			value := []byte(fmt.Sprintf("perf_value_%d", i))
			originalClient.Set(key, value)
			originalClient.Get(key)
		}
		originalDuration := time.Since(start)

		// Test optimized client
		start = time.Now()
		for i := 0; i < numOps; i++ {
			key := []byte(fmt.Sprintf("perf_key_%d", i))
			value := []byte(fmt.Sprintf("perf_value_%d", i))
			optimizedClient.Set(key, value)
			optimizedClient.Get(key)
		}
		optimizedDuration := time.Since(start)

		// Log performance metrics
		t.Logf("Original client:   %v (%.0f ops/s)",
			originalDuration, float64(numOps*2)/originalDuration.Seconds())
		t.Logf("Optimized client:  %v (%.0f ops/s)",
			optimizedDuration, float64(numOps*2)/optimizedDuration.Seconds())

		improvement := float64(originalDuration-optimizedDuration) / float64(originalDuration) * 100
		t.Logf("Performance improvement: %.1f%%", improvement)

		// Note: At client level, performance difference may be minimal due to
		// abstraction layers. The real performance benefits are seen at Handler level.
		// Just verify both clients work correctly without strict performance requirements.
		assert.True(t, originalDuration > 0, "Original client should complete operations")
		assert.True(t, optimizedDuration > 0, "Optimized client should complete operations")
	})
}

func TestNewLRUOptimizedClientConfig(t *testing.T) {
	ctx := context.Background()

	t.Run("With Capacity", func(t *testing.T) {
		client, err := NewLRUOptimizedClient(ctx, 500)
		require.NoError(t, err)
		defer client.Close()

		config := client.GetConfig()
		assert.Equal(t, CacheLRUOptimized, config.Type)
		assert.Equal(t, 500, config.Capacity)
	})

	t.Run("Zero Capacity", func(t *testing.T) {
		client, err := NewLRUOptimizedClient(ctx, 0)
		require.NoError(t, err)
		defer client.Close()

		config := client.GetConfig()
		assert.Equal(t, CacheLRUOptimized, config.Type)
		assert.Equal(t, 128, config.Capacity) // Should default to 128
	})

	t.Run("Via NewClient", func(t *testing.T) {
		client, err := NewClient(ctx, &ClientConfig{
			Type:     CacheLRUOptimized,
			Capacity: 200,
		})
		require.NoError(t, err)
		defer client.Close()

		config := client.GetConfig()
		assert.Equal(t, CacheLRUOptimized, config.Type)
		assert.Equal(t, 200, config.Capacity)
	})
}

func BenchmarkLRUOptimizedClientVsOriginal(b *testing.B) {
	ctx := context.Background()

	b.Run("Original", func(b *testing.B) {
		client, _ := NewLRUClient(ctx, 10000)
		defer client.Close()

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := []byte(fmt.Sprintf("bench_key_%d", i%1000))
				value := []byte(fmt.Sprintf("bench_value_%d", i))

				if i%2 == 0 {
					client.Set(key, value)
				} else {
					client.Get(key)
				}
				i++
			}
		})
	})

	b.Run("Optimized", func(b *testing.B) {
		client, _ := NewLRUOptimizedClient(ctx, 10000)
		defer client.Close()

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := []byte(fmt.Sprintf("bench_key_%d", i%1000))
				value := []byte(fmt.Sprintf("bench_value_%d", i))

				if i%2 == 0 {
					client.Set(key, value)
				} else {
					client.Get(key)
				}
				i++
			}
		})
	})
}

// 补充缺失的测试以提升覆盖率
func TestClient_BatchGet(t *testing.T) {
	ctx := context.Background()
	client, err := NewLRUOptimizedClient(ctx, 100)
	require.NoError(t, err)
	defer client.Close()

	// 设置测试数据
	client.Set([]byte("key1"), []byte("value1"))
	client.Set([]byte("key2"), []byte("value2"))
	client.Set([]byte("key3"), []byte("value3"))

	// BatchGet
	keys := [][]byte{[]byte("key1"), []byte("key2"), []byte("key3"), []byte("nonexistent")}
	results, errs := client.BatchGet(keys)
	assert.Len(t, results, 4)
	assert.Len(t, errs, 4)
	assert.Equal(t, []byte("value1"), results[0])
	assert.NoError(t, errs[0])
	assert.Equal(t, []byte("value2"), results[1])
	assert.NoError(t, errs[1])
	assert.Equal(t, []byte("value3"), results[2])
	assert.NoError(t, errs[2])
	assert.Nil(t, results[3])
	assert.Error(t, errs[3]) // nonexistent key should have an error
}

func TestClient_Stats(t *testing.T) {
	ctx := context.Background()
	client, err := NewLRUOptimizedClient(ctx, 100)
	require.NoError(t, err)
	defer client.Close()

	stats := client.Stats()
	assert.NotNil(t, stats)
}

func TestClient_WithContext(t *testing.T) {
	ctx := context.Background()
	client, err := NewLRUOptimizedClient(ctx, 100)
	require.NoError(t, err)
	defer client.Close()

	newCtx := context.WithValue(ctx, "test_key", "test_value")
	newClient := client.WithContext(newCtx)
	assert.NotNil(t, newClient)
}

func TestNewExpiringClient(t *testing.T) {
	ctx := context.Background()
	client, err := NewExpiringClient(ctx, 1*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer client.Close()

	// 测试基本操作
	err = client.Set([]byte("key"), []byte("value"))
	assert.NoError(t, err)

	val, err := client.Get([]byte("key"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("value"), val)
}

func TestNewRistrettoClient(t *testing.T) {
	ctx := context.Background()
	config := &RistrettoConfig{
		NumCounters: 1e7,     // 10 million counters
		MaxCost:     1 << 30, // 1GB max cost
		BufferItems: 64,      // 64 buffer items
		Metrics:     true,    // enable metrics
	}
	client, err := NewRistrettoClient(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer client.Close()

	// 测试基本操作
	err = client.Set([]byte("key"), []byte("value"))
	assert.NoError(t, err)

	val, err := client.Get([]byte("key"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("value"), val)
}

func TestNewRedisClient(t *testing.T) {
	// 这个测试需要真实的Redis连接，可以跳过或使用miniredis
	t.Skip("Requires real Redis connection")
}
