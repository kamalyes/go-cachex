/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 21:35:15
 * @FilePath: \go-cachex\twolevel_test.go
 * @Description: TwoLevel Cache 详细测试
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwoLevel_BasicOperations(t *testing.T) {
	t.Run("WriteThrough Mode", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := NewExpiringHandler(10 * time.Millisecond)
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// Test Set - should write to both L1 and L2
		err := two.Set([]byte("key1"), []byte("value1"))
		assert.NoError(t, err)

		// Verify both L1 and L2 have the value
		val1, err1 := l1.Get([]byte("key1"))
		assert.NoError(t, err1)
		assert.Equal(t, []byte("value1"), val1)

		val2, err2 := l2.Get([]byte("key1"))
		assert.NoError(t, err2)
		assert.Equal(t, []byte("value1"), val2)

		// Test Get from L1 (should hit L1)
		val, err := two.Get([]byte("key1"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("value1"), val)
	})

	t.Run("Async Mode", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := NewExpiringHandler(10 * time.Millisecond)
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, false)
		defer two.Close()

		// Test Set - should write to L1 immediately, L2 asynchronously
		err := two.Set([]byte("key1"), []byte("value1"))
		assert.NoError(t, err)

		// L1 should have the value immediately
		val1, err1 := l1.Get([]byte("key1"))
		assert.NoError(t, err1)
		assert.Equal(t, []byte("value1"), val1)

		// Give some time for async write to L2
		time.Sleep(10 * time.Millisecond)

		// L2 should eventually have the value
		val2, err2 := l2.Get([]byte("key1"))
		assert.NoError(t, err2)
		assert.Equal(t, []byte("value1"), val2)
	})

	t.Run("Basic CRUD Operations", func(t *testing.T) {
		l1 := NewLRUHandler(5)
		l2 := NewLRUHandler(20)
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// Test Set
		err := two.Set([]byte("test_key"), []byte("test_value"))
		assert.NoError(t, err)

		// Test Get
		val, err := two.Get([]byte("test_key"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("test_value"), val)

		// Test SetWithTTL
		err = two.SetWithTTL([]byte("ttl_key"), []byte("ttl_value"), 50*time.Millisecond)
		assert.NoError(t, err)

		val, err = two.Get([]byte("ttl_key"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("ttl_value"), val)

		// Test GetTTL
		ttl, err := two.GetTTL([]byte("ttl_key"))
		assert.NoError(t, err)
		assert.True(t, ttl > 0 && ttl <= 50*time.Millisecond)

		// Test Del
		err = two.Del([]byte("test_key"))
		assert.NoError(t, err)

		_, err = two.Get([]byte("test_key"))
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("Error Handling", func(t *testing.T) {
		l1 := NewLRUHandler(5)
		l2 := NewLRUHandler(20)
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// Test getting non-existent key
		_, err := two.Get([]byte("nonexistent"))
		assert.ErrorIs(t, err, ErrNotFound)

		// Test TTL for non-existent key
		_, err = two.GetTTL([]byte("nonexistent"))
		assert.ErrorIs(t, err, ErrNotFound)

		// Test input validation
		err = two.Set(nil, []byte("value"))
		assert.ErrorIs(t, err, ErrInvalidKey)

		err = two.Set([]byte("key"), nil)
		assert.ErrorIs(t, err, ErrInvalidValue)
	})
}

func TestTwoLevel_GetFallbackAndPromote(t *testing.T) {
	l1 := NewLRUHandler(1)      // very small L1
	l2 := NewExpiringHandler(0) // L2 holds more
	defer l1.Close()
	defer l2.Close()

	// put value only in L2
	if err := l2.Set([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("l2 set err: %v", err)
	}

	two := NewTwoLevelHandler(l1, l2, true)
	defer two.Close()

	// Get should return from L2 and promote to L1
	v, err := two.Get([]byte("k"))
	if err != nil || string(v) != "v" {
		t.Fatalf("unexpected get: %v %v", v, err)
	}

	// Now L1 should have it
	if v2, err := l1.Get([]byte("k")); err != nil || string(v2) != "v" {
		t.Fatalf("L1 should be promoted, got: %v %v", v2, err)
	}
}

func TestTwoLevel_DetailedOperations(t *testing.T) {
	t.Run("L1 Miss L2 Hit Promotion", func(t *testing.T) {
		l1 := NewLRUHandler(2)  // Small L1 capacity
		l2 := NewLRUHandler(10) // Larger L2 capacity
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// Store data only in L2
		err := l2.Set([]byte("l2_only"), []byte("l2_value"))
		require.NoError(t, err)

		// Verify L1 doesn't have it
		_, err = l1.Get([]byte("l2_only"))
		assert.ErrorIs(t, err, ErrNotFound)

		// Get through TwoLevel should promote to L1
		val, err := two.Get([]byte("l2_only"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("l2_value"), val)

		// Now L1 should have it
		val1, err := l1.Get([]byte("l2_only"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("l2_value"), val1)
	})

	t.Run("L1 Eviction and L2 Fallback", func(t *testing.T) {
		l1 := NewLRUHandler(2) // Very small L1
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// Fill L1 beyond capacity
		for i := 0; i < 5; i++ {
			key := []byte(fmt.Sprintf("key_%d", i))
			value := []byte(fmt.Sprintf("value_%d", i))
			err := two.Set(key, value)
			assert.NoError(t, err)
		}

		// Early keys should be evicted from L1 but still in L2
		_, err := l1.Get([]byte("key_0"))
		assert.ErrorIs(t, err, ErrNotFound, "key_0 should be evicted from L1")

		// But should still be accessible through TwoLevel (from L2)
		val, err := two.Get([]byte("key_0"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("value_0"), val, "key_0 should be retrievable from L2")
	})

	t.Run("TTL Propagation", func(t *testing.T) {
		l1 := NewExpiringHandler(10 * time.Millisecond)
		l2 := NewExpiringHandler(10 * time.Millisecond)
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// Set with TTL
		err := two.SetWithTTL([]byte("ttl_test"), []byte("ttl_value"), 100*time.Millisecond)
		assert.NoError(t, err)

		// Both levels should have TTL
		ttl1, err1 := l1.GetTTL([]byte("ttl_test"))
		assert.NoError(t, err1)
		assert.True(t, ttl1 > 0 && ttl1 <= 100*time.Millisecond)

		ttl2, err2 := l2.GetTTL([]byte("ttl_test"))
		assert.NoError(t, err2)
		assert.True(t, ttl2 > 0 && ttl2 <= 100*time.Millisecond)

		// TwoLevel should return consistent TTL
		ttl, err := two.GetTTL([]byte("ttl_test"))
		assert.NoError(t, err)
		assert.True(t, ttl > 0 && ttl <= 100*time.Millisecond)

		// Wait for expiration
		time.Sleep(150 * time.Millisecond)

		// Should be expired from both levels
		_, err = two.Get([]byte("ttl_test"))
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("TTL Promotion with Remaining Time", func(t *testing.T) {
		l1 := NewExpiringHandler(10 * time.Millisecond)
		l2 := NewExpiringHandler(10 * time.Millisecond)
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// Set data with TTL only in L2
		err := l2.SetWithTTL([]byte("promote_ttl"), []byte("promote_value"), 200*time.Millisecond)
		assert.NoError(t, err)

		// Wait a bit, then access through TwoLevel
		time.Sleep(50 * time.Millisecond)

		val, err := two.Get([]byte("promote_ttl"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("promote_value"), val)

		// L1 should now have it with remaining TTL
		ttl1, err := l1.GetTTL([]byte("promote_ttl"))
		assert.NoError(t, err)
		assert.True(t, ttl1 > 0 && ttl1 < 200*time.Millisecond,
			"Promoted TTL should be less than original")
	})

	t.Run("Delete Operations", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// Set data
		err := two.Set([]byte("del_test"), []byte("del_value"))
		assert.NoError(t, err)

		// Verify both levels have it
		_, err1 := l1.Get([]byte("del_test"))
		assert.NoError(t, err1)
		_, err2 := l2.Get([]byte("del_test"))
		assert.NoError(t, err2)

		// Delete through TwoLevel
		err = two.Del([]byte("del_test"))
		assert.NoError(t, err)

		// Both levels should not have it
		_, err1 = l1.Get([]byte("del_test"))
		assert.ErrorIs(t, err1, ErrNotFound)
		_, err2 = l2.Get([]byte("del_test"))
		assert.ErrorIs(t, err2, ErrNotFound)

		// TwoLevel should also not have it
		_, err = two.Get([]byte("del_test"))
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("Different Backend Combinations", func(t *testing.T) {
		combinations := []struct {
			name      string
			l1Factory func() Handler
			l2Factory func() Handler
		}{
			{
				name:      "LRU + LRU",
				l1Factory: func() Handler { return NewLRUHandler(5) },
				l2Factory: func() Handler { return NewLRUHandler(20) },
			},
			{
				name:      "LRU + Expiring",
				l1Factory: func() Handler { return NewLRUHandler(5) },
				l2Factory: func() Handler { return NewExpiringHandler(10 * time.Millisecond) },
			},
			{
				name:      "Expiring + LRU",
				l1Factory: func() Handler { return NewExpiringHandler(10 * time.Millisecond) },
				l2Factory: func() Handler { return NewLRUHandler(20) },
			},
			{
				name:      "LRU + Ristretto",
				l1Factory: func() Handler { return NewLRUHandler(5) },
				l2Factory: func() Handler {
					rist, _ := NewRistrettoHandler(&RistrettoConfig{
						NumCounters: 100,
						MaxCost:     10 << 20,
						BufferItems: 6,
					})
					return rist
				},
			},
		}

		for _, combo := range combinations {
			t.Run(combo.name, func(t *testing.T) {
				l1 := combo.l1Factory()
				l2 := combo.l2Factory()
				defer l1.Close()
				defer l2.Close()

				two := NewTwoLevelHandler(l1, l2, true)
				defer two.Close()

				// Basic operations should work with any combination
				err := two.Set([]byte("combo_test"), []byte("combo_value"))
				assert.NoError(t, err)

				val, err := two.Get([]byte("combo_test"))
				assert.NoError(t, err)
				assert.Equal(t, []byte("combo_value"), val)

				err = two.Del([]byte("combo_test"))
				assert.NoError(t, err)

				_, err = two.Get([]byte("combo_test"))
				assert.ErrorIs(t, err, ErrNotFound)
			})
		}
	})
}

func TestTwoLevel_ConcurrencyAndPerformance(t *testing.T) {
	t.Run("Concurrent Operations", func(t *testing.T) {
		l1 := NewLRUHandler(100)
		l2 := NewLRUHandler(1000)
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		const workers = 20
		const operations = 100

		var wg sync.WaitGroup
		errCh := make(chan error, workers*operations)

		// Concurrent writes
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for op := 0; op < operations; op++ {
					key := []byte(fmt.Sprintf("worker_%d_op_%d", workerID, op))
					value := []byte(fmt.Sprintf("value_%d_%d", workerID, op))

					if err := two.Set(key, value); err != nil {
						errCh <- err
						return
					}
				}
			}(w)
		}

		wg.Wait()
		close(errCh)

		// Check for errors
		var errors []error
		for err := range errCh {
			errors = append(errors, err)
		}
		assert.Empty(t, errors, "Should have no errors during concurrent writes")

		// Concurrent reads - verify the data we just wrote
		wg = sync.WaitGroup{}
		var readErrors []error
		var mu sync.Mutex

		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for op := 0; op < operations; op++ {
					key := []byte(fmt.Sprintf("worker_%d_op_%d", workerID, op))
					expectedValue := []byte(fmt.Sprintf("value_%d_%d", workerID, op))

					val, err := two.Get(key)
					if err != nil {
						mu.Lock()
						readErrors = append(readErrors, err)
						mu.Unlock()
						continue
					}
					if string(val) != string(expectedValue) {
						mu.Lock()
						readErrors = append(readErrors, fmt.Errorf("value mismatch: expected %s, got %s", expectedValue, val))
						mu.Unlock()
					}
				}
			}(w)
		}

		wg.Wait()

		// Allow for some cache evictions, but most data should be readable
		readErrorCount := len(readErrors)
		totalOperations := workers * operations
		successRate := float64(totalOperations-readErrorCount) / float64(totalOperations)
		assert.True(t, successRate >= 0.5, "At least 50%% of reads should succeed (got %.1f%%)", successRate*100)
	})

	t.Run("Performance Comparison", func(t *testing.T) {
		// Compare single level vs two level performance
		const testOps = 10000

		// Single Level (LRU only)
		singleLevel := NewLRUHandler(1000)
		defer singleLevel.Close()

		start := time.Now()
		for i := 0; i < testOps; i++ {
			key := []byte(fmt.Sprintf("single_%d", i))
			value := []byte(fmt.Sprintf("value_%d", i))
			singleLevel.Set(key, value)
		}
		singleTime := time.Since(start)

		// Two Level (LRU + LRU)
		l1 := NewLRUHandler(100)  // Smaller L1
		l2 := NewLRUHandler(2000) // Larger L2
		defer l1.Close()
		defer l2.Close()

		twoLevel := NewTwoLevelHandler(l1, l2, true)
		defer twoLevel.Close()

		start = time.Now()
		for i := 0; i < testOps; i++ {
			key := []byte(fmt.Sprintf("two_%d", i))
			value := []byte(fmt.Sprintf("value_%d", i))
			twoLevel.Set(key, value)
		}
		twoTime := time.Since(start)

		t.Logf("Single level write time: %v", singleTime)
		t.Logf("Two level write time: %v", twoTime)

		// Two level should be reasonably close (within 3x)
		ratio := float64(twoTime) / float64(singleTime)
		assert.True(t, ratio < 5.0, "Two level shouldn't be more than 5x slower")

		// Test read performance with different hit ratios
		// L1 hits - access recent data that should be in L1
		start = time.Now()
		for i := 0; i < 1000; i++ {
			key := []byte(fmt.Sprintf("two_%d", testOps-100+i%100)) // Recent keys in L1
			twoLevel.Get(key)
		}
		l1HitTime := time.Since(start)

		// L2 hits - access older data by evicting L1 first, then accessing old data
		// First, fill L1 with new data to evict old data
		for i := 0; i < 200; i++ {
			key := []byte(fmt.Sprintf("evict_%d", i))
			value := []byte(fmt.Sprintf("evict_value_%d", i))
			twoLevel.Set(key, value)
		}

		// Now access old data which should be in L2 only
		start = time.Now()
		for i := 0; i < 1000; i++ {
			key := []byte(fmt.Sprintf("two_%d", i%1000)) // Older keys likely in L2 only
			twoLevel.Get(key)
		}
		l2HitTime := time.Since(start)

		t.Logf("L1 hit time (1000 ops): %v", l1HitTime)
		t.Logf("L2 hit time (1000 ops): %v", l2HitTime)

		// Note: The timing difference might be small, so we just verify both work
		// In practice, L1 should be faster, but the difference depends on implementation details
		if l1HitTime < l2HitTime {
			t.Logf("L1 hits are faster than L2 hits as expected")
		} else {
			t.Logf("L2 hits were faster, possibly due to cache warming or other factors")
		}
	})

	t.Run("Memory Efficiency", func(t *testing.T) {
		// Test that L1 eviction doesn't cause data loss when L2 is available
		l1 := NewLRUHandler(5)   // Very small L1
		l2 := NewLRUHandler(100) // Much larger L2
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// Write many items to force L1 evictions
		const numItems = 50
		for i := 0; i < numItems; i++ {
			key := []byte(fmt.Sprintf("item_%d", i))
			value := []byte(fmt.Sprintf("value_%d", i))
			err := two.Set(key, value)
			assert.NoError(t, err)
		}

		// All items should still be accessible (from L2)
		for i := 0; i < numItems; i++ {
			key := []byte(fmt.Sprintf("item_%d", i))
			expectedValue := []byte(fmt.Sprintf("value_%d", i))

			val, err := two.Get(key)
			assert.NoError(t, err)
			assert.Equal(t, expectedValue, val, "Item %d should be accessible", i)
		}

		// Recent items should be promoted back to L1
		recentKeys := []string{"item_45", "item_46", "item_47", "item_48", "item_49"}
		for _, keyStr := range recentKeys {
			key := []byte(keyStr)
			// Access again to ensure promotion
			two.Get(key)

			// Should now be in L1
			_, err := l1.Get(key)
			assert.NoError(t, err, "%s should be in L1 after promotion", keyStr)
		}
	})
}

// errTTLHandler 嵌入 *LRUHandler 并覆盖 GetTTLWithCtx/GetTTL，
// 用于覆盖 TwoLevelHandler.GetWithCtx 中 L2.GetTTLWithCtx 返回错误的分支
type errTTLHandler struct {
	*LRUHandler
	ttlErr error
}

func (e *errTTLHandler) GetTTLWithCtx(ctx context.Context, key []byte) (time.Duration, error) {
	return 0, e.ttlErr
}

func (e *errTTLHandler) GetTTL(key []byte) (time.Duration, error) {
	return 0, e.ttlErr
}

// ============================================================
// BatchGet / BatchGetWithCtx 测试
// ============================================================

func TestTwoLevel_BatchGet(t *testing.T) {
	t.Run("空 keys 返回 nil", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// 简化版
		results, errs := two.BatchGet(nil)
		assert.Nil(t, results)
		assert.Nil(t, errs)

		// 带 context 版
		results, errs = two.BatchGetWithCtx(context.Background(), nil)
		assert.Nil(t, results)
		assert.Nil(t, errs)
	})

	t.Run("L1 全命中", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		require.NoError(t, two.Set([]byte("a"), []byte("va")))
		require.NoError(t, two.Set([]byte("b"), []byte("vb")))

		results, errs := two.BatchGet([][]byte{[]byte("a"), []byte("b")})
		assert.Len(t, results, 2)
		assert.Len(t, errs, 2)
		assert.Equal(t, []byte("va"), results[0])
		assert.Equal(t, []byte("vb"), results[1])
		assert.NoError(t, errs[0])
		assert.NoError(t, errs[1])
	})

	t.Run("L1 未命中 L2 命中并异步提升", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// 仅在 L2 中写入
		require.NoError(t, l2.Set([]byte("only_l2"), []byte("l2_val")))

		results, errs := two.BatchGet([][]byte{[]byte("only_l2")})
		assert.Len(t, results, 1)
		assert.Equal(t, []byte("l2_val"), results[0])
		assert.Len(t, errs, 1)
		assert.NoError(t, errs[0])

		// 等待异步提升到 L1
		time.Sleep(20 * time.Millisecond)
		val, err := l1.Get([]byte("only_l2"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("l2_val"), val)
	})

	t.Run("L1 和 L2 都未命中", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		results, errs := two.BatchGet([][]byte{[]byte("missing")})
		assert.Len(t, results, 1)
		assert.Nil(t, results[0])
		assert.Len(t, errs, 1)
		assert.ErrorIs(t, errs[0], ErrNotFound)
	})

	t.Run("混合命中场景", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// a 在 L1 和 L2 都有（通过 two.Set 同步写入）
		require.NoError(t, two.Set([]byte("a"), []byte("va")))
		// b 仅在 L2
		require.NoError(t, l2.Set([]byte("b"), []byte("vb")))
		// c 不存在

		results, errs := two.BatchGet([][]byte{[]byte("a"), []byte("b"), []byte("c")})
		assert.Len(t, results, 3)
		assert.Equal(t, []byte("va"), results[0])
		assert.Equal(t, []byte("vb"), results[1])
		assert.Nil(t, results[2])
		assert.NoError(t, errs[0])
		assert.NoError(t, errs[1])
		assert.ErrorIs(t, errs[2], ErrNotFound)
	})
}

// ============================================================
// Stats 测试
// ============================================================

func TestTwoLevel_Stats(t *testing.T) {
	l1 := NewLRUHandler(10)
	l2 := NewLRUHandler(20)
	defer l1.Close()
	defer l2.Close()

	two := NewTwoLevelHandler(l1, l2, true)
	defer two.Close()

	require.NoError(t, two.Set([]byte("k"), []byte("v")))

	stats := two.Stats()
	assert.Equal(t, "two_level", stats["cache_type"])
	assert.NotNil(t, stats["l1_cache"])
	assert.NotNil(t, stats["l2_cache"])

	// 验证 L1 和 L2 统计是各自 handler 返回的 map
	l1Stats := stats["l1_cache"].(map[string]interface{})
	l2Stats := stats["l2_cache"].(map[string]interface{})
	assert.Contains(t, l1Stats, "entries")
	assert.Contains(t, l2Stats, "entries")
}

// ============================================================
// GetOrCompute / GetOrComputeWithCtx 测试
// ============================================================

func TestTwoLevel_GetOrCompute(t *testing.T) {
	t.Run("L1 命中", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		require.NoError(t, two.Set([]byte("k"), []byte("v")))

		// loader 不应被调用
		called := false
		val, err := two.GetOrCompute([]byte("k"), 0, func() ([]byte, error) {
			called = true
			return nil, nil
		})
		assert.NoError(t, err)
		assert.Equal(t, []byte("v"), val)
		assert.False(t, called)
	})

	t.Run("L1 未命中 L2 命中 ttl<=0 异步提升", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// 仅在 L2 写入
		require.NoError(t, l2.Set([]byte("l2_only"), []byte("l2_val")))

		val, err := two.GetOrComputeWithCtx(context.Background(), []byte("l2_only"), 0, func(ctx context.Context) ([]byte, error) {
			t.Fatal("loader 不应被调用")
			return nil, nil
		})
		assert.NoError(t, err)
		assert.Equal(t, []byte("l2_val"), val)

		// 等待异步提升
		time.Sleep(20 * time.Millisecond)
		v, err := l1.Get([]byte("l2_only"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("l2_val"), v)
	})

	t.Run("L1 未命中 L2 命中 ttl>0 异步提升", func(t *testing.T) {
		l1 := NewExpiringHandler(10 * time.Millisecond)
		l2 := NewExpiringHandler(10 * time.Millisecond)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		// 仅在 L2 写入（无 TTL）
		require.NoError(t, l2.Set([]byte("l2_only"), []byte("l2_val")))

		val, err := two.GetOrComputeWithCtx(context.Background(), []byte("l2_only"), 100*time.Millisecond, func(ctx context.Context) ([]byte, error) {
			t.Fatal("loader 不应被调用")
			return nil, nil
		})
		assert.NoError(t, err)
		assert.Equal(t, []byte("l2_val"), val)

		// 等待异步提升（带 TTL）
		time.Sleep(20 * time.Millisecond)
		v, err := l1.Get([]byte("l2_only"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("l2_val"), v)
	})

	t.Run("两级都未命中 loader 成功 ttl<=0", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		val, err := two.GetOrCompute([]byte("compute"), 0, func() ([]byte, error) {
			return []byte("computed"), nil
		})
		assert.NoError(t, err)
		assert.Equal(t, []byte("computed"), val)

		// 两级缓存都应写入
		v1, err := l1.Get([]byte("compute"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("computed"), v1)

		v2, err := l2.Get([]byte("compute"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("computed"), v2)
	})

	t.Run("两级都未命中 loader 成功 ttl>0", func(t *testing.T) {
		l1 := NewExpiringHandler(10 * time.Millisecond)
		l2 := NewExpiringHandler(10 * time.Millisecond)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		val, err := two.GetOrComputeWithCtx(context.Background(), []byte("compute"), 100*time.Millisecond, func(ctx context.Context) ([]byte, error) {
			return []byte("computed"), nil
		})
		assert.NoError(t, err)
		assert.Equal(t, []byte("computed"), val)

		// 两级缓存都应写入（带 TTL）
		v1, err := l1.Get([]byte("compute"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("computed"), v1)

		v2, err := l2.Get([]byte("compute"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("computed"), v2)
	})

	t.Run("两级都未命中 loader 错误", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		loadErr := errors.New("loader failed")
		val, err := two.GetOrCompute([]byte("fail"), 0, func() ([]byte, error) {
			return nil, loadErr
		})
		assert.Nil(t, val)
		assert.ErrorIs(t, err, loadErr)

		// 缓存不应有值
		_, err = l1.Get([]byte("fail"))
		assert.ErrorIs(t, err, ErrNotFound)
		_, err = l2.Get([]byte("fail"))
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

// ============================================================
// Close 错误分支测试
// ============================================================

func TestTwoLevel_CloseError(t *testing.T) {
	t.Run("L2 Close 返回错误", func(t *testing.T) {
		l1 := NewLRUHandler(10)
		l2 := &errCloseHandler{LRUHandler: NewLRUHandler(10), closeErr: errors.New("l2 close error")}
		two := NewTwoLevelHandler(l1, l2, true)

		err := two.Close()
		assert.Error(t, err)
		assert.Equal(t, "l2 close error", err.Error())
	})

	t.Run("L1 和 L2 Close 都返回错误", func(t *testing.T) {
		l1 := &errCloseHandler{LRUHandler: NewLRUHandler(10), closeErr: errors.New("l1 close error")}
		l2 := &errCloseHandler{LRUHandler: NewLRUHandler(10), closeErr: errors.New("l2 close error")}
		two := NewTwoLevelHandler(l1, l2, true)

		err := two.Close()
		assert.Error(t, err)
		// lastErr 应为 L2 的错误（后赋值覆盖）
		assert.Equal(t, "l2 close error", err.Error())
	})
}

// ============================================================
// SetWithCtx / SetWithTTLAndCtx 边界分支测试
// ============================================================

func TestTwoLevel_SetEdgeCases(t *testing.T) {
	t.Run("SetWithCtx 异步模式 L1 错误", func(t *testing.T) {
		l1 := NewLRUHandler(5)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, false) // 异步模式
		defer two.Close()

		// nil key 触发 L1.SetWithCtx 错误
		err := two.Set(nil, []byte("v"))
		assert.ErrorIs(t, err, ErrInvalidKey)
	})

	t.Run("SetWithTTLAndCtx 同步模式 L1 错误", func(t *testing.T) {
		l1 := NewLRUHandler(5)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, true) // 同步模式
		defer two.Close()

		// nil key 触发 L1.SetWithTTLAndCtx 错误
		err := two.SetWithTTL(nil, []byte("v"), time.Second)
		assert.ErrorIs(t, err, ErrInvalidKey)
	})

	t.Run("SetWithTTLAndCtx 异步模式成功", func(t *testing.T) {
		l1 := NewLRUHandler(5)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, false) // 异步模式
		defer two.Close()

		err := two.SetWithTTL([]byte("k"), []byte("v"), 100*time.Millisecond)
		assert.NoError(t, err)

		// L1 应立即可用
		val, err := l1.Get([]byte("k"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("v"), val)

		// 等待异步写入 L2
		time.Sleep(20 * time.Millisecond)
		val2, err := l2.Get([]byte("k"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("v"), val2)
	})

	t.Run("SetWithTTLAndCtx 异步模式 L1 错误", func(t *testing.T) {
		l1 := NewLRUHandler(5)
		l2 := NewLRUHandler(10)
		defer l1.Close()
		defer l2.Close()
		two := NewTwoLevelHandler(l1, l2, false) // 异步模式
		defer two.Close()

		// nil key 触发 L1.SetWithTTLAndCtx 错误
		err := two.SetWithTTL(nil, []byte("v"), time.Second)
		assert.ErrorIs(t, err, ErrInvalidKey)
	})
}

// ============================================================
// GetWithCtx 中 L2.GetTTL 返回错误的分支测试
// ============================================================

func TestTwoLevel_GetWithCtxTTLError(t *testing.T) {
	l1 := NewLRUHandler(10)
	l2 := &errTTLHandler{
		LRUHandler: NewLRUHandler(10),
		ttlErr:     errors.New("ttl read error"),
	}
	defer l1.Close()
	defer l2.Close()

	two := NewTwoLevelHandler(l1, l2, true)
	defer two.Close()

	// 仅在 L2 写入值（GetWithCtx 成功，但 GetTTLWithCtx 返回错误）
	require.NoError(t, l2.Set([]byte("k"), []byte("v")))

	// TwoLevel.Get 应从 L2 获取值，并走 GetTTL 错误的 else 分支提升到 L1
	val, err := two.Get([]byte("k"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("v"), val)

	// L1 应通过 SetWithCtx（else 分支）被提升
	val1, err := l1.Get([]byte("k"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("v"), val1)
}

// ====================== Benchmark Tests ======================

func BenchmarkTwoLevel_Set_WriteThrough(b *testing.B) {
	l1 := NewLRUHandler(1000)
	l2 := NewLRUHandler(10000)
	defer l1.Close()
	defer l2.Close()

	two := NewTwoLevelHandler(l1, l2, true)
	defer two.Close()

	value := make([]byte, 100)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("bench_set_wt_%d", i))
			two.Set(key, value)
			i++
		}
	})
}

func BenchmarkTwoLevel_Set_Async(b *testing.B) {
	l1 := NewLRUHandler(1000)
	l2 := NewLRUHandler(10000)
	defer l1.Close()
	defer l2.Close()

	two := NewTwoLevelHandler(l1, l2, false)
	defer two.Close()

	value := make([]byte, 100)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("bench_set_async_%d", i))
			two.Set(key, value)
			i++
		}
	})
}

func BenchmarkTwoLevel_Get_L1Hit(b *testing.B) {
	l1 := NewLRUHandler(1000)
	l2 := NewLRUHandler(10000)
	defer l1.Close()
	defer l2.Close()

	two := NewTwoLevelHandler(l1, l2, true)
	defer two.Close()

	value := make([]byte, 100)

	// Pre-fill L1
	for i := 0; i < 500; i++ {
		key := []byte(fmt.Sprintf("bench_get_l1_%d", i))
		two.Set(key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("bench_get_l1_%d", i%500))
			two.Get(key)
			i++
		}
	})
}

func BenchmarkTwoLevel_Get_L2Hit(b *testing.B) {
	l1 := NewLRUHandler(10)    // Very small L1
	l2 := NewLRUHandler(10000) // Large L2
	defer l1.Close()
	defer l2.Close()

	two := NewTwoLevelHandler(l1, l2, true)
	defer two.Close()

	value := make([]byte, 100)

	// Pre-fill L2 and ensure L1 is evicted
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("bench_get_l2_%d", i))
		two.Set(key, value)
	}

	// Force L1 evictions by writing more data
	for i := 1000; i < 1100; i++ {
		key := []byte(fmt.Sprintf("bench_get_l2_evict_%d", i))
		two.Set(key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			// Access old keys that should be in L2 only
			key := []byte(fmt.Sprintf("bench_get_l2_%d", i%1000))
			two.Get(key)
			i++
		}
	})
}

func BenchmarkTwoLevel_Mixed(b *testing.B) {
	l1 := NewLRUHandler(500)
	l2 := NewLRUHandler(5000)
	defer l1.Close()
	defer l2.Close()

	two := NewTwoLevelHandler(l1, l2, true)
	defer two.Close()

	value := make([]byte, 100)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("bench_mixed_%d", i))
			if i%4 == 0 {
				// 25% writes
				two.Set(key, value)
			} else {
				// 75% reads
				two.Get(key)
			}
			i++
		}
	})
}

func BenchmarkTwoLevel_vs_Single(b *testing.B) {
	value := make([]byte, 100)

	b.Run("Single_LRU", func(b *testing.B) {
		single := NewLRUHandler(10000)
		defer single.Close()

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := []byte(fmt.Sprintf("single_%d", i))
				single.Set(key, value)
				i++
			}
		})
	})

	b.Run("TwoLevel_LRU_LRU", func(b *testing.B) {
		l1 := NewLRUHandler(1000)
		l2 := NewLRUHandler(10000)
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, true)
		defer two.Close()

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := []byte(fmt.Sprintf("two_%d", i))
				two.Set(key, value)
				i++
			}
		})
	})

	b.Run("TwoLevel_Async", func(b *testing.B) {
		l1 := NewLRUHandler(1000)
		l2 := NewLRUHandler(10000)
		defer l1.Close()
		defer l2.Close()

		two := NewTwoLevelHandler(l1, l2, false)
		defer two.Close()

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := []byte(fmt.Sprintf("async_%d", i))
				two.Set(key, value)
				i++
			}
		})
	})
}

func BenchmarkTwoLevel_DifferentBackends(b *testing.B) {
	value := make([]byte, 100)

	backends := []struct {
		name      string
		l1Factory func() Handler
		l2Factory func() Handler
	}{
		{
			name:      "LRU_LRU",
			l1Factory: func() Handler { return NewLRUHandler(1000) },
			l2Factory: func() Handler { return NewLRUHandler(10000) },
		},
		{
			name:      "LRU_Expiring",
			l1Factory: func() Handler { return NewLRUHandler(1000) },
			l2Factory: func() Handler { return NewExpiringHandler(10 * time.Millisecond) },
		},
		{
			name:      "LRU_Ristretto",
			l1Factory: func() Handler { return NewLRUHandler(1000) },
			l2Factory: func() Handler {
				rist, _ := NewRistrettoHandler(&RistrettoConfig{
					NumCounters: 10000,
					MaxCost:     100 << 20,
					BufferItems: 6,
				})
				return rist
			},
		},
	}

	for _, backend := range backends {
		b.Run(backend.name, func(b *testing.B) {
			l1 := backend.l1Factory()
			l2 := backend.l2Factory()
			defer l1.Close()
			defer l2.Close()

			two := NewTwoLevelHandler(l1, l2, true)
			defer two.Close()

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					key := []byte(fmt.Sprintf("backend_%d", i%5000))
					if i%3 == 0 {
						two.Set(key, value)
					} else {
						two.Get(key)
					}
					i++
				}
			})
		})
	}
}
