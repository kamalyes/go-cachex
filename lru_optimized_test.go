/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 22:30:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 22:30:00
 * @FilePath: \go-cachex\lru_optimized_test.go
 * @Description: LRU 优化版本的性能测试
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLRUOptimized(t *testing.T) {
	h := NewLRUOptimizedHandler(100)
	defer h.Close()
	
	// 基础操作测试
	t.Run("Set and Get", func(t *testing.T) {
		key := []byte("test_key")
		value := []byte("test_value")
		
		require.NoError(t, h.Set(key, value))
		result, err := h.Get(key)
		require.NoError(t, err)
		assert.Equal(t, value, result)
	})
	
	// TTL测试
	t.Run("TTL Operations", func(t *testing.T) {
		key := []byte("ttl_key")
		value := []byte("ttl_value")
		
		// 设置100ms TTL
		require.NoError(t, h.SetWithTTL(key, value, 100*time.Millisecond))
		
		// 立即获取应该成功
		result, err := h.Get(key)
		require.NoError(t, err)
		assert.Equal(t, value, result)
		
		// 等待过期
		time.Sleep(150 * time.Millisecond)
		_, err = h.Get(key)
		assert.Error(t, err)
	})
	
	// 容量限制测试
	t.Run("Capacity Limit", func(t *testing.T) {
		h2 := NewLRUOptimizedHandler(3)
		defer h2.Close()
		
		// 插入3个元素
		for i := 0; i < 3; i++ {
			key := []byte(fmt.Sprintf("key%d", i))
			value := []byte(fmt.Sprintf("value%d", i))
			require.NoError(t, h2.Set(key, value))
		}
		
		// 插入第4个元素，应该驱逐第一个
		key4 := []byte("key3")
		value4 := []byte("value3")
		require.NoError(t, h2.Set(key4, value4))
		
		// key0应该被驱逐
		_, err := h2.Get([]byte("key0"))
		assert.Error(t, err)
	})
}

func TestLRUOptimizedSpecific(t *testing.T) {
	t.Run("BatchGet", func(t *testing.T) {
		h := NewLRUOptimizedHandler(100)
		defer h.Close()
		
		// 设置测试数据
		keys := make([][]byte, 10)
		values := make([][]byte, 10)
		for i := 0; i < 10; i++ {
			keys[i] = []byte(fmt.Sprintf("key%d", i))
			values[i] = []byte(fmt.Sprintf("value%d", i))
			require.NoError(t, h.Set(keys[i], values[i]))
		}
		
		// 批量获取
		results, errors := h.BatchGet(keys)
		
		assert.Len(t, results, 10)
		assert.Len(t, errors, 10)
		
		for i := 0; i < 10; i++ {
			assert.NoError(t, errors[i])
			assert.Equal(t, values[i], results[i])
		}
	})
	
	t.Run("Stats", func(t *testing.T) {
		h := NewLRUOptimizedHandler(100)
		defer h.Close()
		
		// 添加一些数据
		for i := 0; i < 10; i++ {
			key := []byte(fmt.Sprintf("key%d", i))
			value := []byte(fmt.Sprintf("value%d", i))
			require.NoError(t, h.Set(key, value))
		}
		
		stats := h.Stats()
		assert.Equal(t, 10, stats["entries"])
		assert.Equal(t, 100, stats["max_entries"])
		assert.Equal(t, false, stats["closed"])
	})
	
	t.Run("Object Pool", func(t *testing.T) {
		h := NewLRUOptimizedHandler(10)
		defer h.Close()
		
		// 测试对象池的重用
		for i := 0; i < 100; i++ {
			key := []byte(fmt.Sprintf("key%d", i))
			value := []byte(fmt.Sprintf("value%d", i))
			require.NoError(t, h.Set(key, value))
		}
		
		// 由于容量限制，应该只有最后10个
		stats := h.Stats()
		assert.Equal(t, 10, stats["entries"])
	})
}

// 基准测试 - 与原始LRU对比
func BenchmarkLRUOptimizedVsOriginal(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			// 测试优化版本
			b.Run("Optimized", func(b *testing.B) {
				benchmarkHandler(b, NewLRUOptimizedHandler(size))
			})
			
			// 测试原始版本
			b.Run("Original", func(b *testing.B) {
				benchmarkHandler(b, NewLRUHandler(size))
			})
		})
	}
}

func benchmarkHandler(b *testing.B, h Handler) {
	defer h.Close()
	
	keys := make([][]byte, 1000)
	values := make([][]byte, 1000)
	for i := 0; i < 1000; i++ {
		keys[i] = []byte(fmt.Sprintf("key%d", i))
		values[i] = make([]byte, 100) // 100字节数据
	}
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			idx := i % 1000
			if i%2 == 0 {
				h.Set(keys[idx], values[idx])
			} else {
				h.Get(keys[idx])
			}
			i++
		}
	})
}

// 专项基准测试
func BenchmarkLRUOptimizedSet(b *testing.B) {
	h := NewLRUOptimizedHandler(10000)
	defer h.Close()
	
	key := []byte("benchmark_key")
	value := make([]byte, 100)
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		h.Set(key, value)
	}
}

func BenchmarkLRUOptimizedGet(b *testing.B) {
	h := NewLRUOptimizedHandler(10000)
	defer h.Close()
	
	key := []byte("benchmark_key")
	value := make([]byte, 100)
	h.Set(key, value)
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		h.Get(key)
	}
}

func BenchmarkLRUOptimizedConcurrent(b *testing.B) {
	h := NewLRUOptimizedHandler(10000)
	defer h.Close()
	
	keys := make([][]byte, 1000)
	values := make([][]byte, 1000)
	for i := 0; i < 1000; i++ {
		keys[i] = []byte(fmt.Sprintf("concurrent_key_%d", i))
		values[i] = make([]byte, 100)
		h.Set(keys[i], values[i])
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			idx := i % 1000
			if i%3 == 0 {
				h.Set(keys[idx], values[idx])
			} else {
				h.Get(keys[idx])
			}
			i++
		}
	})
}

func BenchmarkLRUOptimizedBatchGet(b *testing.B) {
	h := NewLRUOptimizedHandler(10000)
	defer h.Close()
	
	keys := make([][]byte, 100)
	for i := 0; i < 100; i++ {
		keys[i] = []byte(fmt.Sprintf("batch_key_%d", i))
		value := make([]byte, 50)
		h.Set(keys[i], value)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		h.BatchGet(keys)
	}
}

func BenchmarkLRUOptimizedMemoryUsage(b *testing.B) {
	b.Run("SmallData", func(b *testing.B) {
		benchmarkMemoryUsage(b, 10, 1000)
	})
	
	b.Run("MediumData", func(b *testing.B) {
		benchmarkMemoryUsage(b, 100, 1000)
	})
	
	b.Run("LargeData", func(b *testing.B) {
		benchmarkMemoryUsage(b, 1000, 1000)
	})
}

func benchmarkMemoryUsage(b *testing.B, valueSize, numKeys int) {
	var m1, m2 runtime.MemStats
	
	runtime.GC()
	runtime.ReadMemStats(&m1)
	
	h := NewLRUOptimizedHandler(numKeys * 2)
	defer h.Close()
	
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("memory_key_%d", i))
		value := make([]byte, valueSize)
		h.Set(key, value)
	}
	
	runtime.GC()
	runtime.ReadMemStats(&m2)
	
	bytesPerEntry := (m2.Alloc - m1.Alloc) / uint64(numKeys)
	b.ReportMetric(float64(bytesPerEntry), "bytes/entry")
}

// 性能对比测试
func TestLRUOptimizedPerformanceComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance comparison in short mode")
	}
	
	sizes := []int{1000, 10000}
	numOps := 100000
	
	for _, size := range sizes {
		t.Run(fmt.Sprintf("Size_%d", size), func(t *testing.T) {
			// 测试原始LRU
			originalTime := benchmarkLRUPerformance(t, NewLRUHandler(size), numOps)
			
			// 测试优化LRU
			optimizedTime := benchmarkLRUPerformance(t, NewLRUOptimizedHandler(size), numOps)
			
			improvement := float64(originalTime-optimizedTime) / float64(originalTime) * 100
			
			t.Logf("Cache Size: %d", size)
			t.Logf("Original LRU:   %v", originalTime)
			t.Logf("Optimized LRU:  %v", optimizedTime)
			t.Logf("Improvement:    %.1f%%", improvement)
			
			// 期望有一定的性能提升
			assert.True(t, optimizedTime <= originalTime, 
				"Optimized version should be faster or equal")
		})
	}
}

func benchmarkLRUPerformance(t *testing.T, h Handler, numOps int) time.Duration {
	defer h.Close()
	
	keys := make([][]byte, 1000)
	values := make([][]byte, 1000)
	for i := 0; i < 1000; i++ {
		keys[i] = []byte(fmt.Sprintf("perf_key_%d", i))
		values[i] = make([]byte, 64)
	}
	
	start := time.Now()
	
	var wg sync.WaitGroup
	numWorkers := runtime.GOMAXPROCS(0)
	opsPerWorker := numOps / numWorkers
	
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				idx := i % 1000
				if i%2 == 0 {
					h.Set(keys[idx], values[idx])
				} else {
					h.Get(keys[idx])
				}
			}
		}()
	}
	
	wg.Wait()
	return time.Since(start)
}

// 内存效率测试
func TestLRUOptimizedMemoryEfficiency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping memory efficiency test in short mode")
	}
	
	numEntries := 10000
	valueSize := 100
	
	// 测试原始LRU内存使用
	originalMemory := measureMemoryUsage(func() Handler {
		return NewLRUHandler(numEntries * 2)
	}, numEntries, valueSize)
	
	// 测试优化LRU内存使用
	optimizedMemory := measureMemoryUsage(func() Handler {
		return NewLRUOptimizedHandler(numEntries * 2)
	}, numEntries, valueSize)
	
	memoryReduction := float64(originalMemory-optimizedMemory) / float64(originalMemory) * 100
	
	t.Logf("Original LRU memory:   %d bytes (%d bytes/entry)", 
		originalMemory, originalMemory/uint64(numEntries))
	t.Logf("Optimized LRU memory:  %d bytes (%d bytes/entry)", 
		optimizedMemory, optimizedMemory/uint64(numEntries))
	t.Logf("Memory reduction:      %.1f%%", memoryReduction)
	
	// 期望内存使用减少或相当
	assert.True(t, optimizedMemory <= originalMemory*30/100, // 允许30%的误差
		"Optimized version should use similar or less memory")
}

func measureMemoryUsage(createHandler func() Handler, numEntries, valueSize int) uint64 {
	var m1, m2 runtime.MemStats
	
	runtime.GC()
	runtime.ReadMemStats(&m1)
	
	h := createHandler()
	defer h.Close()
	
	for i := 0; i < numEntries; i++ {
		key := []byte(fmt.Sprintf("memory_test_%d", i))
		value := make([]byte, valueSize)
		h.Set(key, value)
	}
	
	runtime.GC()
	runtime.ReadMemStats(&m2)
	
	return m2.Alloc - m1.Alloc
}