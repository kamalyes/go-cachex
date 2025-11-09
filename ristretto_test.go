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
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"testing"
	"time"

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
					value := []byte("value" + strconv.Itoa(base + j))
					
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
		{NumCounters: 1e3, MaxCost: 1 << 10, BufferItems: 64},  // Small
		{NumCounters: 1e4, MaxCost: 1 << 20, BufferItems: 64},  // Medium
		{NumCounters: 1e6, MaxCost: 1 << 30, BufferItems: 64},  // Large
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