/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 23:23:11
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 21:15:00
 * @FilePath: \go-cachex\ristretto_test.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"testing"
	"time"

	ristretto "github.com/dgraph-io/ristretto/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRistrettoHandler(t *testing.T) {
	t.Run("Basic Operations", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create default RistrettoHandler")
		defer handler.Close()

		key := []byte("testKey")
		value := []byte("testValue")

		// Set and Get
		assert.NoError(handler.Set(key, value), "Set should succeed")
		got, err := handler.Get(key)
		assert.NoError(err, "Get should succeed")
		assert.Equal(value, got, "Get should return set value")

		// GetTTL
		ttl, err := handler.GetTTL(key)
		assert.NoError(err, "GetTTL should succeed")
		assert.Equal(time.Duration(0), ttl, "TTL should be 0 for non-TTL set")

		// Delete
		assert.NoError(handler.Del(key), "Del should succeed")
		_, err = handler.Get(key)
		assert.ErrorIs(err, ErrNotFound, "Get deleted key should return ErrNotFound")
	})

	t.Run("Empty Key/Value", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create handler")
		defer handler.Close()

		// Empty key
		value := []byte("value")
		assert.NoError(handler.Set([]byte{}, value), "Set empty key should succeed")
		got, err := handler.Get([]byte{})
		assert.NoError(err, "Get empty key should succeed")
		assert.Equal(value, got, "Get empty key should return correct value")

		// Empty value
		key := []byte("key")
		assert.NoError(handler.Set(key, []byte{}), "Set empty value should succeed")
		got, err = handler.Get(key)
		assert.NoError(err, "Get key with empty value should succeed")
		assert.Empty(got, "Get should return empty value")
	})

	t.Run("Value Updates", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create handler")
		defer handler.Close()

		key := []byte("key")
		value1 := []byte("value1")
		value2 := []byte("value2")

		// First set
		assert.NoError(handler.Set(key, value1), "First set should succeed")
		got, err := handler.Get(key)
		assert.NoError(err, "Get after first set should succeed")
		assert.Equal(value1, got, "Should get first value")

		// Update
		assert.NoError(handler.Set(key, value2), "Update should succeed")
		got, err = handler.Get(key)
		assert.NoError(err, "Get after update should succeed")
		assert.Equal(value2, got, "Should get updated value")
	})

	t.Run("Non-existent Keys", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create handler")
		defer handler.Close()

		// Get non-existent
		_, err = handler.Get([]byte("non-existent"))
		assert.ErrorIs(err, ErrNotFound, "Get non-existent key should return ErrNotFound")

		// Delete non-existent
		assert.NoError(handler.Del([]byte("non-existent")), "Del non-existent key should succeed")
	})
}

func TestRistrettoHandlerWithTTL(t *testing.T) {
	t.Run("TTL Operations", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create default RistrettoHandler")
		defer handler.Close()

		key := []byte("testKeyWithTTL")
		value := []byte("testValueWithTTL")
		ttl := 5 * time.Second

		assert.NoError(handler.SetWithTTL(key, value, ttl), "SetWithTTL should succeed")

		got, err := handler.Get(key)
		assert.NoError(err, "Get should succeed")
		assert.Equal(value, got, "Get should return set value")

		gotTTL, err := handler.GetTTL(key)
		assert.NoError(err, "GetTTL should succeed")
		assert.True(gotTTL <= ttl && gotTTL > 0, "TTL should be between 0 and 5s")
	})

	t.Run("TTL Expiration", func(t *testing.T) {
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create default RistrettoHandler")
		defer handler.Close()

		key := []byte("expireKey")
		value := []byte("expireValue")
		ttl := 1 * time.Second

		assert.NoError(handler.SetWithTTL(key, value, ttl), "SetWithTTL should succeed")
		time.Sleep(2 * time.Second) // Wait for expiration

		_, err = handler.Get(key)
		assert.ErrorIs(err, ErrNotFound, "Get after TTL expiration should return ErrNotFound")
	})

	t.Run("TTL Edge Cases", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create handler")
		defer handler.Close()

		// Zero TTL (should expire immediately)
		assert.NoError(handler.SetWithTTL([]byte("zero"), []byte("value"), 0), "Zero TTL should succeed")
		time.Sleep(10 * time.Millisecond) // 给Ristretto一点时间来处理过期
		_, err = handler.Get([]byte("zero"))
		assert.ErrorIs(err, ErrNotFound, "Zero TTL key should be expired")

		// -1 TTL (should never expire)
		assert.NoError(handler.SetWithTTL([]byte("forever"), []byte("value"), -1), "-1 TTL should succeed")
		val, err := handler.Get([]byte("forever"))
		assert.NoError(err, "Forever key should exist")
		assert.Equal([]byte("value"), val, "Forever key should have correct value")

		// Invalid TTL (less than -1)
		err = handler.SetWithTTL([]byte("invalid"), []byte("value"), -2*time.Second)
		assert.ErrorIs(err, ErrInvalidTTL, "TTL less than -1 should return error")
	})
}

func TestRistrettoLargeData(t *testing.T) {
	t.Run("Large Data Operations", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create default RistrettoHandler")
		defer handler.Close()

		const numItems = 10000
		const checkInterval = 1000

		// Batch set
		for i := 0; i < numItems; i++ {
			key := []byte("key" + strconv.Itoa(i))
			value := []byte("value" + strconv.Itoa(i))
			assert.NoError(handler.Set(key, value), "Set should succeed for item %d", i)
		}

		// Sample verification
		for i := 0; i < numItems; i += checkInterval {
			key := []byte("key" + strconv.Itoa(i))
			expectedValue := []byte("value" + strconv.Itoa(i))

			got, err := handler.Get(key)
			if assert.NoError(err, "Get should succeed for item %d", i) {
				assert.Equal(expectedValue, got, "Values should match for item %d", i)
			}
		}
	})

	t.Run("Concurrent Access", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		assert := assert.New(t)

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(err, "Should create default RistrettoHandler")
		defer handler.Close()

		const numGoroutines = 10
		const numOpsPerGoroutine = 1000
		done := make(chan bool, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func(routineID int) {
				base := routineID * numOpsPerGoroutine
				for j := 0; j < numOpsPerGoroutine; j++ {
					key := []byte(strconv.Itoa(base + j))
					value := []byte("value" + strconv.Itoa(base+j))

					assert.NoError(handler.Set(key, value), "Concurrent Set should succeed")

					if got, err := handler.Get(key); assert.NoError(err) {
						assert.Equal(value, got, "Concurrent Get should return correct value")
					}
				}
				done <- true
			}(i)
		}

		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			<-done
		}
	})
}

// 详细测试用例
func TestRistretto_DetailedOperations(t *testing.T) {
	t.Run("Custom Configuration", func(t *testing.T) {
		config := &RistrettoConfig{
			NumCounters: 1e4,
			MaxCost:     1 << 20, // 1MB
			BufferItems: 64,
		}

		handler, err := NewRistrettoHandler(config)
		require.NoError(t, err)
		defer handler.Close()

		// Test with custom config
		key := []byte("custom-test")
		value := []byte("custom-value")

		assert.NoError(t, handler.Set(key, value))
		got, err := handler.Get(key)
		assert.NoError(t, err)
		assert.Equal(t, value, got)
	})

	t.Run("Large Value Storage", func(t *testing.T) {
		handler, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer handler.Close()

		// Test with large values (10KB each)
		largeValue := make([]byte, 10*1024)
		for i := range largeValue {
			largeValue[i] = byte(i % 256)
		}

		key := []byte("large-value-key")
		assert.NoError(t, handler.Set(key, largeValue))

		got, err := handler.Get(key)
		assert.NoError(t, err)
		assert.Equal(t, largeValue, got)
	})

	t.Run("Stress Test with Mixed Operations", func(t *testing.T) {
		if testing.Short() {
			t.Skip("Skipping stress test in short mode")
		}

		handler, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer handler.Close()

		const numOperations = 50000
		var wg sync.WaitGroup

		// Perform mixed operations concurrently
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for j := 0; j < numOperations/5; j++ {
					key := []byte(fmt.Sprintf("stress-%d-%d", workerID, j))
					value := []byte(fmt.Sprintf("value-%d-%d", workerID, j))

					// Set
					handler.Set(key, value)

					// Get
					handler.Get(key)

					// Update
					newValue := []byte(fmt.Sprintf("updated-%d-%d", workerID, j))
					handler.Set(key, newValue)

					// Delete occasionally
					if j%10 == 0 {
						handler.Del(key)
					}
				}
			}(i)
		}

		wg.Wait()
	})
}

// 性能基准测试
func BenchmarkRistretto_Set(b *testing.B) {
	handler, err := NewDefaultRistrettoHandler()
	if err != nil {
		b.Fatal(err)
	}
	defer handler.Close()

	keys := make([][]byte, b.N)
	values := make([][]byte, b.N)

	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%d", i))
		values[i] = []byte(fmt.Sprintf("value-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Set(keys[i], values[i])
	}
}

func BenchmarkRistretto_Get(b *testing.B) {
	handler, err := NewDefaultRistrettoHandler()
	if err != nil {
		b.Fatal(err)
	}
	defer handler.Close()

	// Prepopulate
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		handler.Set(key, value)
	}

	keys := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%d", i%1000))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Get(keys[i])
	}
}

func BenchmarkRistretto_SetWithTTL(b *testing.B) {
	handler, err := NewDefaultRistrettoHandler()
	if err != nil {
		b.Fatal(err)
	}
	defer handler.Close()

	keys := make([][]byte, b.N)
	values := make([][]byte, b.N)

	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%d", i))
		values[i] = []byte(fmt.Sprintf("value-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.SetWithTTL(keys[i], values[i], time.Hour)
	}
}

func BenchmarkRistretto_Mixed(b *testing.B) {
	handler, err := NewDefaultRistrettoHandler()
	if err != nil {
		b.Fatal(err)
	}
	defer handler.Close()

	keys := make([][]byte, 1000)
	values := make([][]byte, 1000)

	for i := 0; i < 1000; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%d", i))
		values[i] = []byte(fmt.Sprintf("value-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % 1000
		switch i % 3 {
		case 0: // Set
			handler.Set(keys[idx], values[idx])
		case 1: // Get
			handler.Get(keys[idx])
		case 2: // Del
			handler.Del(keys[idx])
		}
	}
}

func BenchmarkRistretto_ConcurrentAccess(b *testing.B) {
	handler, err := NewDefaultRistrettoHandler()
	if err != nil {
		b.Fatal(err)
	}
	defer handler.Close()

	// Prepopulate
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		handler.Set(key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			idx := rand.Intn(100)
			key := []byte(fmt.Sprintf("key-%d", idx))

			switch rand.Intn(2) {
			case 0: // Read
				handler.Get(key)
			case 1: // Write
				value := []byte(fmt.Sprintf("value-%d", rand.Intn(1000)))
				handler.Set(key, value)
			}
		}
	})
}

func BenchmarkRistretto_LargeValues(b *testing.B) {
	handler, err := NewDefaultRistrettoHandler()
	if err != nil {
		b.Fatal(err)
	}
	defer handler.Close()

	// Create large values (1KB each)
	largeValue := make([]byte, 1024)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	keys := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.Set(keys[i], largeValue)
	}
}

// 内存和性能对比测试
func BenchmarkRistretto_ConfigComparison(b *testing.B) {
	configs := []*RistrettoConfig{
		{NumCounters: 1e3, MaxCost: 1 << 10, BufferItems: 64}, // Small
		{NumCounters: 1e4, MaxCost: 1 << 20, BufferItems: 64}, // Medium
		{NumCounters: 1e6, MaxCost: 1 << 30, BufferItems: 64}, // Large
	}

	for i, config := range configs {
		b.Run(fmt.Sprintf("Config-%d", i), func(b *testing.B) {
			handler, err := NewRistrettoHandler(config)
			if err != nil {
				b.Fatal(err)
			}
			defer handler.Close()

			for i := 0; i < b.N; i++ {
				key := []byte(fmt.Sprintf("key-%d", i))
				value := []byte(fmt.Sprintf("value-%d", i))
				handler.Set(key, value)
			}
		})
	}
}

// 补充缺失的测试以提升覆盖率
func TestRistrettoConfig_Setters(t *testing.T) {
	config := NewDefaultRistrettoConfig()

	t.Run("SetNumCounters", func(t *testing.T) {
		config.SetNumCounters(2000)
		assert.Equal(t, int64(2000), config.NumCounters)
	})

	t.Run("SetMaxCost", func(t *testing.T) {
		config.SetMaxCost(1 << 25)
		assert.Equal(t, int64(1<<25), config.MaxCost)
	})

	t.Run("SetBufferItems", func(t *testing.T) {
		config.SetBufferItems(128)
		assert.Equal(t, int64(128), config.BufferItems)
	})

	t.Run("EnableMetrics", func(t *testing.T) {
		config.EnableMetrics()
		assert.True(t, config.Metrics)
	})

	t.Run("SetOnEvict", func(t *testing.T) {
		config.SetOnEvict(func(item *ristretto.Item[[]byte]) {
			// Callback for eviction
		})
		assert.NotNil(t, config.OnEvict)
	})

	t.Run("SetOnReject", func(t *testing.T) {
		config.SetOnReject(func(item *ristretto.Item[[]byte]) {
			// Callback for rejection
		})
		assert.NotNil(t, config.OnReject)
	})

	t.Run("SetOnExit", func(t *testing.T) {
		config.SetOnExit(func(val []byte) {
			// Callback for exit
		})
		assert.NotNil(t, config.OnExit)
	})

	t.Run("SetShouldUpdate", func(t *testing.T) {
		config.SetShouldUpdate(func(cur, prev []byte) bool {
			return true
		})
		assert.NotNil(t, config.ShouldUpdate)
	})

	t.Run("SetKeyToHash", func(t *testing.T) {
		config.SetKeyToHash(func(key []byte) (uint64, uint64) {
			return 0, 0
		})
		assert.NotNil(t, config.KeyToHash)
	})

	t.Run("SetCost", func(t *testing.T) {
		config.SetCost(func(value []byte) int64 {
			return 1
		})
		assert.NotNil(t, config.Cost)
	})

	t.Run("SetIgnoreInternalCost", func(t *testing.T) {
		config.SetIgnoreInternalCost(true)
		assert.True(t, config.IgnoreInternalCost)
	})

	t.Run("SetTtlTickerDurationInSec", func(t *testing.T) {
		config.SetTtlTickerDurationInSec(10)
		assert.Equal(t, int64(10), config.TtlTickerDurationInSec)
	})
}

func TestRistrettoHandler_MissingMethods(t *testing.T) {
	handler, err := NewDefaultRistrettoHandler()
	require.NoError(t, err)
	defer handler.Close()

	t.Run("BatchGet", func(t *testing.T) {
		handler.Set([]byte("batch1"), []byte("value1"))
		handler.Set([]byte("batch2"), []byte("value2"))

		keys := [][]byte{[]byte("batch1"), []byte("batch2"), []byte("nonexistent")}
		results, errs := handler.BatchGet(keys)
		assert.Len(t, results, 3)
		assert.Len(t, errs, 3)
		// 前两个应该成功
		assert.NoError(t, errs[0])
		assert.NoError(t, errs[1])
		// 第三个不存在应该有错误
		assert.Error(t, errs[2])
	})

	t.Run("Stats", func(t *testing.T) {
		stats := handler.Stats()
		assert.NotNil(t, stats)
	})

	t.Run("GetOrCompute", func(t *testing.T) {
		callCount := 0
		compute := func() ([]byte, error) {
			callCount++
			return []byte("computed"), nil
		}

		val, err := handler.GetOrCompute([]byte("compute_key"), 1*time.Hour, compute)
		assert.NoError(t, err)
		assert.Equal(t, []byte("computed"), val)
		assert.Equal(t, 1, callCount)

		// 第二次不应该调用compute
		val2, err := handler.GetOrCompute([]byte("compute_key"), 1*time.Hour, compute)
		assert.NoError(t, err)
		assert.Equal(t, []byte("computed"), val2)
		assert.Equal(t, 1, callCount)
	})
}

// TestRistrettoHandler_NilCacheBranches 测试所有方法的 nil cache 分支
func TestRistrettoHandler_NilCacheBranches(t *testing.T) {
	nilHandler := &RistrettoHandler{cache: nil}
	ctx := context.Background()

	t.Run("GetWithCtx nil cache", func(t *testing.T) {
		_, err := nilHandler.GetWithCtx(ctx, []byte("key"))
		assert.ErrorIs(t, err, ErrNotInitialized)
	})

	t.Run("GetTTLWithCtx nil cache", func(t *testing.T) {
		_, err := nilHandler.GetTTLWithCtx(ctx, []byte("key"))
		assert.ErrorIs(t, err, ErrNotInitialized)
	})

	t.Run("SetWithCtx nil cache", func(t *testing.T) {
		err := nilHandler.SetWithCtx(ctx, []byte("key"), []byte("val"))
		assert.ErrorIs(t, err, ErrNotInitialized)
	})

	t.Run("SetWithTTLAndCtx nil cache", func(t *testing.T) {
		err := nilHandler.SetWithTTLAndCtx(ctx, []byte("key"), []byte("val"), time.Second)
		assert.ErrorIs(t, err, ErrNotInitialized)
	})

	t.Run("DelWithCtx nil cache", func(t *testing.T) {
		err := nilHandler.DelWithCtx(ctx, []byte("key"))
		assert.ErrorIs(t, err, ErrNotInitialized)
	})

	t.Run("BatchGetWithCtx nil cache", func(t *testing.T) {
		results, errs := nilHandler.BatchGetWithCtx(ctx, [][]byte{[]byte("key1"), []byte("key2")})
		assert.Len(t, results, 2)
		assert.Len(t, errs, 2)
		assert.ErrorIs(t, errs[0], ErrNotInitialized)
		assert.ErrorIs(t, errs[1], ErrNotInitialized)
	})

	t.Run("GetOrComputeWithCtx nil cache", func(t *testing.T) {
		_, err := nilHandler.GetOrComputeWithCtx(ctx, []byte("key"), time.Second, func(ctx context.Context) ([]byte, error) {
			return []byte("val"), nil
		})
		assert.ErrorIs(t, err, ErrNotInitialized)
	})

	t.Run("Stats nil cache", func(t *testing.T) {
		stats := nilHandler.Stats()
		assert.NotNil(t, stats)
		assert.Equal(t, false, stats["initialized"])
	})

	t.Run("Close nil cache", func(t *testing.T) {
		err := nilHandler.Close()
		assert.ErrorIs(t, err, ErrNotInitialized)
	})
}

// TestRistrettoHandler_NilKeyBranches 测试 nil key 分支
func TestRistrettoHandler_NilKeyBranches(t *testing.T) {
	handler, err := NewDefaultRistrettoHandler()
	require.NoError(t, err)
	defer handler.Close()

	ctx := context.Background()

	t.Run("GetWithCtx nil key", func(t *testing.T) {
		_, err := handler.GetWithCtx(ctx, nil)
		assert.ErrorIs(t, err, ErrInvalidKey)
	})

	t.Run("GetTTLWithCtx nil key", func(t *testing.T) {
		_, err := handler.GetTTLWithCtx(ctx, nil)
		assert.ErrorIs(t, err, ErrInvalidKey)
	})

	t.Run("SetWithCtx nil key", func(t *testing.T) {
		err := handler.SetWithCtx(ctx, nil, []byte("val"))
		assert.ErrorIs(t, err, ErrInvalidKey)
	})

	t.Run("SetWithTTLAndCtx nil key", func(t *testing.T) {
		err := handler.SetWithTTLAndCtx(ctx, nil, []byte("val"), time.Second)
		assert.ErrorIs(t, err, ErrInvalidKey)
	})

	t.Run("DelWithCtx nil key", func(t *testing.T) {
		err := handler.DelWithCtx(ctx, nil)
		assert.ErrorIs(t, err, ErrInvalidKey)
	})

	t.Run("GetOrComputeWithCtx nil key", func(t *testing.T) {
		_, err := handler.GetOrComputeWithCtx(ctx, nil, time.Second, func(ctx context.Context) ([]byte, error) {
			return []byte("val"), nil
		})
		assert.ErrorIs(t, err, ErrInvalidKey)
	})
}

// TestRistrettoHandler_NewRistrettoHandler_NilConfig 测试 NewRistrettoHandler 的 nil config 分支
func TestRistrettoHandler_NewRistrettoHandler_NilConfig(t *testing.T) {
	handler, err := NewRistrettoHandler(nil)
	require.NoError(t, err)
	defer handler.Close()

	// 验证 handler 正常工作
	assert.NoError(t, handler.Set([]byte("test"), []byte("value")))
	val, err := handler.Get([]byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("value"), val)
}

// TestRistrettoHandler_NewRistrettoHandler_InvalidConfig 测试 NewRistrettoHandler 的无效 config 分支
func TestRistrettoHandler_NewRistrettoHandler_InvalidConfig(t *testing.T) {
	// NumCounters 为 0 会导致 ristretto.NewCache 返回错误
	config := &RistrettoConfig{
		NumCounters: 0,
		MaxCost:     1 << 20,
		BufferItems: 64,
	}
	_, err := NewRistrettoHandler(config)
	assert.Error(t, err)
}

// TestRistrettoHandler_NewDefaultRistrettoHandler_Error 测试 NewDefaultRistrettoHandler 的错误分支
func TestRistrettoHandler_NewDefaultRistrettoHandler_Error(t *testing.T) {
	// 通过先创建一个默认配置然后修改为无效来测试错误分支
	// 由于 NewDefaultRistrettoHandler 内部使用固定配置，我们需要间接测试
	// 直接测试 createCache 的错误分支
	config := &RistrettoConfig{
		NumCounters: -1,
		MaxCost:     1 << 20,
		BufferItems: 64,
	}
	_, err := createCache(config)
	assert.Error(t, err)
}

// TestRistrettoHandler_GetTTLWithCtx_NotFound 测试 GetTTLWithCtx 的 !ok 分支
func TestRistrettoHandler_GetTTLWithCtx_NotFound(t *testing.T) {
	handler, err := NewDefaultRistrettoHandler()
	require.NoError(t, err)
	defer handler.Close()

	// 获取不存在的 key 的 TTL
	_, err = handler.GetTTLWithCtx(context.Background(), []byte("nonexistent-key"))
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestRistrettoHandler_SetWithCtx_CapacityExceeded 测试 SetWithCtx 的 !ok 分支
func TestRistrettoHandler_SetWithCtx_CapacityExceeded(t *testing.T) {
	// 创建一个缓存并直接关闭它（不通过 handler），使 cache 非 nil 但已关闭
	config := NewDefaultRistrettoConfig()
	cache, err := createCache(config)
	require.NoError(t, err)
	cache.Close()

	handler := &RistrettoHandler{cache: cache}
	err = handler.SetWithCtx(context.Background(), []byte("key"), []byte("val"))
	assert.ErrorIs(t, err, ErrCapacityExceeded)
}

// TestRistrettoHandler_SetWithTTLAndCtx_CapacityExceeded 测试 SetWithTTLAndCtx 的 !ok 分支
func TestRistrettoHandler_SetWithTTLAndCtx_CapacityExceeded(t *testing.T) {
	// 创建一个缓存并直接关闭它（不通过 handler），使 cache 非 nil 但已关闭
	config := NewDefaultRistrettoConfig()
	cache, err := createCache(config)
	require.NoError(t, err)
	cache.Close()

	handler := &RistrettoHandler{cache: cache}
	err = handler.SetWithTTLAndCtx(context.Background(), []byte("key"), []byte("val"), time.Second)
	assert.ErrorIs(t, err, ErrCapacityExceeded)
}

// TestRistrettoHandler_BatchGetWithCtx_EmptyKeys 测试 BatchGetWithCtx 的空 keys 分支
func TestRistrettoHandler_BatchGetWithCtx_EmptyKeys(t *testing.T) {
	handler, err := NewDefaultRistrettoHandler()
	require.NoError(t, err)
	defer handler.Close()

	results, errs := handler.BatchGetWithCtx(context.Background(), [][]byte{})
	assert.Nil(t, results)
	assert.Nil(t, errs)
}

// TestRistrettoHandler_BatchGetWithCtx_EmptyKeyInBatch 测试 BatchGetWithCtx 的空 key 分支
func TestRistrettoHandler_BatchGetWithCtx_EmptyKeyInBatch(t *testing.T) {
	handler, err := NewDefaultRistrettoHandler()
	require.NoError(t, err)
	defer handler.Close()

	// 设置一个有效值
	handler.Set([]byte("valid-key"), []byte("valid-value"))

	// 批量中包含空 key 和有效 key
	results, errs := handler.BatchGetWithCtx(context.Background(), [][]byte{
		[]byte("valid-key"),
		[]byte{}, // 空 key
		nil,      // nil key
		[]byte("nonexistent"),
	})
	assert.Len(t, results, 4)
	assert.Len(t, errs, 4)
	assert.NoError(t, errs[0])
	assert.Equal(t, []byte("valid-value"), results[0])
	assert.ErrorIs(t, errs[1], ErrInvalidKey)
	assert.ErrorIs(t, errs[2], ErrInvalidKey)
	assert.ErrorIs(t, errs[3], ErrNotFound)
}

// TestRistrettoHandler_GetOrComputeWithCtx_CtxCancelled 测试 GetOrComputeWithCtx 的 ctx 取消分支
func TestRistrettoHandler_GetOrComputeWithCtx_CtxCancelled(t *testing.T) {
	handler, err := NewDefaultRistrettoHandler()
	require.NoError(t, err)
	defer handler.Close()

	// 创建已取消的上下文
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = handler.GetOrComputeWithCtx(ctx, []byte("cancelled-key"), time.Second, func(ctx context.Context) ([]byte, error) {
		return []byte("should not be computed"), nil
	})
	assert.ErrorIs(t, err, context.Canceled)
}

// TestRistrettoHandler_GetOrComputeWithCtx_LoaderError 测试 GetOrComputeWithCtx 的 loader 错误分支
func TestRistrettoHandler_GetOrComputeWithCtx_LoaderError(t *testing.T) {
	handler, err := NewDefaultRistrettoHandler()
	require.NoError(t, err)
	defer handler.Close()

	testErr := errors.New("loader failed")
	_, err = handler.GetOrComputeWithCtx(context.Background(), []byte("error-key"), time.Second, func(ctx context.Context) ([]byte, error) {
		return nil, testErr
	})
	assert.ErrorIs(t, err, testErr)
}

// TestRistrettoHandler_GetOrComputeWithCtx_TTLZero 测试 GetOrComputeWithCtx 的 ttl<=0 分支
func TestRistrettoHandler_GetOrComputeWithCtx_TTLZero(t *testing.T) {
	handler, err := NewDefaultRistrettoHandler()
	require.NoError(t, err)
	defer handler.Close()

	result, err := handler.GetOrComputeWithCtx(context.Background(), []byte("zero-ttl-key"), 0, func(ctx context.Context) ([]byte, error) {
		return []byte("computed-zero-ttl"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("computed-zero-ttl"), result)

	// 验证值已缓存（使用 Set 而非 SetWithTTL）
	cached, err := handler.Get([]byte("zero-ttl-key"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("computed-zero-ttl"), cached)
}

// TestRistrettoHandler_GetOrComputeWithCtx_AllTTLBranches 测试 GetOrComputeWithCtx 的所有 TTL 分支
func TestRistrettoHandler_GetOrComputeWithCtx_AllTTLBranches(t *testing.T) {
	t.Run("negative TTL (never expire)", func(t *testing.T) {
		handler, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer handler.Close()

		// ttl = -1 表示永不过期
		result, err := handler.GetOrComputeWithCtx(context.Background(), []byte("neg-ttl-key"), -1, func(ctx context.Context) ([]byte, error) {
			return []byte("never-expires"), nil
		})
		assert.NoError(t, err)
		assert.Equal(t, []byte("never-expires"), result)
	})

	t.Run("positive TTL", func(t *testing.T) {
		handler, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer handler.Close()

		result, err := handler.GetOrComputeWithCtx(context.Background(), []byte("pos-ttl-key"), time.Minute, func(ctx context.Context) ([]byte, error) {
			return []byte("with-ttl"), nil
		})
		assert.NoError(t, err)
		assert.Equal(t, []byte("with-ttl"), result)
	})
}

// TestRistrettoHandler_Stats_WithMetrics 测试 Stats 启用 metrics 的分支
func TestRistrettoHandler_Stats_WithMetrics(t *testing.T) {
	config := NewDefaultRistrettoConfig()
	config.EnableMetrics()

	handler, err := NewRistrettoHandler(config)
	require.NoError(t, err)
	defer handler.Close()

	// 执行一些操作以产生统计
	handler.Set([]byte("stats-key"), []byte("value"))
	handler.Get([]byte("stats-key"))
	handler.Get([]byte("nonexistent"))

	stats := handler.Stats()
	assert.NotNil(t, stats)
	assert.Equal(t, true, stats["initialized"])
	// 验证统计字段存在
	assert.Contains(t, stats, "hits")
	assert.Contains(t, stats, "misses")
}

// TestRistrettoHandler_SetWithTTLAndCtx_AllBranches 测试 SetWithTTLAndCtx 的所有 TTL 分支
func TestRistrettoHandler_SetWithTTLAndCtx_AllBranches(t *testing.T) {
	handler, err := NewDefaultRistrettoHandler()
	require.NoError(t, err)
	defer handler.Close()

	ctx := context.Background()

	t.Run("TTL = -1 (never expire)", func(t *testing.T) {
		err := handler.SetWithTTLAndCtx(ctx, []byte("forever-key"), []byte("forever"), -1)
		assert.NoError(t, err)
		val, err := handler.Get([]byte("forever-key"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("forever"), val)
	})

	t.Run("TTL = 0 (immediate expire)", func(t *testing.T) {
		err := handler.SetWithTTLAndCtx(ctx, []byte("immediate-key"), []byte("immediate"), 0)
		assert.NoError(t, err)
		// 等待一小段时间让过期生效
		time.Sleep(10 * time.Millisecond)
		_, err = handler.Get([]byte("immediate-key"))
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("TTL = positive", func(t *testing.T) {
		err := handler.SetWithTTLAndCtx(ctx, []byte("ttl-key"), []byte("ttl-value"), time.Hour)
		assert.NoError(t, err)
		val, err := handler.Get([]byte("ttl-key"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("ttl-value"), val)
	})

	t.Run("Invalid TTL (< -1)", func(t *testing.T) {
		err := handler.SetWithTTLAndCtx(ctx, []byte("invalid"), []byte("val"), -2*time.Second)
		assert.ErrorIs(t, err, ErrInvalidTTL)
	})
}

// TestRistrettoHandler_Close_Twice 测试 Close 后再 Close
func TestRistrettoHandler_Close_Twice(t *testing.T) {
	handler, err := NewDefaultRistrettoHandler()
	require.NoError(t, err)

	// 第一次 Close
	err = handler.Close()
	assert.NoError(t, err)

	// 第二次 Close（cache 为 nil）
	err = handler.Close()
	assert.ErrorIs(t, err, ErrNotInitialized)
}
