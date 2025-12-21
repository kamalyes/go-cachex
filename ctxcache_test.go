/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 23:27:58
 * @FilePath: \go-cachex\ctxcache_test.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCtxCache_DetailedOperations(t *testing.T) {
	t.Run("Basic Operations", func(t *testing.T) {
		rh, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer rh.Close()

		c := NewCtxCache(rh)
		ctx := context.Background()

		// 测试 Set 和 Get
		err = c.Set(ctx, []byte("key1"), []byte("value1"))
		assert.NoError(t, err)

		val, err := c.Get(ctx, []byte("key1"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("value1"), val)

		// 测试 SetWithTTL
		err = c.SetWithTTL(ctx, []byte("key2"), []byte("value2"), 100*time.Millisecond)
		assert.NoError(t, err)

		val, err = c.Get(ctx, []byte("key2"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("value2"), val)

		// 测试 Del
		err = c.Del(ctx, []byte("key1"))
		assert.NoError(t, err)

		_, err = c.Get(ctx, []byte("key1"))
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("Context Operations", func(t *testing.T) {
		rh, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer rh.Close()

		c := NewCtxCache(rh)

		// 测试取消的 context
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // 立即取消

		err = c.Set(ctx, []byte("key"), []byte("value"))
		assert.ErrorIs(t, err, context.Canceled)

		_, err = c.Get(ctx, []byte("key"))
		assert.ErrorIs(t, err, context.Canceled)

		err = c.Del(ctx, []byte("key"))
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("Context With Values", func(t *testing.T) {
		rh, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer rh.Close()

		c := NewCtxCache(rh)

		// 测试 WithCache 和 FromContext
		ctx := context.Background()
		ctxWithCache := WithCache(ctx, c)

		retrievedCache := FromContext(ctxWithCache)
		assert.NotNil(t, retrievedCache)
		assert.Equal(t, c, retrievedCache)

		// 测试没有缓存的 context
		emptyCache := FromContext(context.Background())
		assert.Nil(t, emptyCache)
	})

	t.Run("GetOrCompute Basic", func(t *testing.T) {
		rh, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer rh.Close()

		c := NewCtxCache(rh)
		ctx := context.Background()

		var callCount int32
		loader := func(ctx context.Context) ([]byte, error) {
			atomic.AddInt32(&callCount, 1)
			return []byte("computed_value"), nil
		}

		// 第一次调用应该触发 loader
		val, err := c.GetOrCompute(ctx, []byte("compute_key"), 0, loader)
		assert.NoError(t, err)
		assert.Equal(t, []byte("computed_value"), val)
		assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

		// 第二次调用应该从缓存获取
		val, err = c.GetOrCompute(ctx, []byte("compute_key"), 0, loader)
		assert.NoError(t, err)
		assert.Equal(t, []byte("computed_value"), val)
		assert.Equal(t, int32(1), atomic.LoadInt32(&callCount)) // 计数器不变
	})

	t.Run("Input Validation", func(t *testing.T) {
		rh, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer rh.Close()

		c := NewCtxCache(rh)
		ctx := context.Background()

		// 测试 nil 键
		_, err = c.Get(ctx, nil)
		assert.ErrorIs(t, err, ErrInvalidKey)

		err = c.Set(ctx, nil, []byte("value"))
		assert.ErrorIs(t, err, ErrInvalidKey)

		// 测试 nil 值
		err = c.Set(ctx, []byte("key"), nil)
		assert.ErrorIs(t, err, ErrInvalidValue)

		// 测试无效 TTL
		err = c.SetWithTTL(ctx, []byte("key"), []byte("value"), -2*time.Second)
		assert.ErrorIs(t, err, ErrInvalidTTL)

		// 测试 nil loader
		_, err = c.GetOrCompute(ctx, []byte("key"), 0, nil)
		assert.ErrorIs(t, err, ErrInvalidValue)
	})
}

func TestCtxCache_GetOrComputeAdvanced(t *testing.T) {
	t.Run("Singleflight", func(t *testing.T) {
		rh, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer rh.Close()

		c := NewCtxCache(rh)
		ctx := context.Background()

		var calls int32
		loader := func(ctx context.Context) ([]byte, error) {
			atomic.AddInt32(&calls, 1)
			// 模拟慢计算
			time.Sleep(100 * time.Millisecond)
			return []byte("singleflight_value"), nil
		}

		concurrency := 10
		var wg sync.WaitGroup
		wg.Add(concurrency)

		results := make([][]byte, concurrency)
		errors := make([]error, concurrency)

		for i := 0; i < concurrency; i++ {
			go func(index int) {
				defer wg.Done()
				val, err := c.GetOrCompute(ctx, []byte("sf_key"), 0, loader)
				results[index] = val
				errors[index] = err
			}(i)
		}

		wg.Wait()

		// 验证所有调用都成功且返回相同值
		for i := 0; i < concurrency; i++ {
			assert.NoError(t, errors[i], "Call %d should succeed", i)
			assert.Equal(t, []byte("singleflight_value"), results[i], "Call %d should return correct value", i)
		}

		// 验证 loader 只被调用一次
		assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
	})

	t.Run("Context Cancellation", func(t *testing.T) {
		rh, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer rh.Close()

		c := NewCtxCache(rh)

		loader := func(ctx context.Context) ([]byte, error) {
			select {
			case <-time.After(500 * time.Millisecond):
				return []byte("slow_value"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		// 测试 timeout
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, err = c.GetOrCompute(ctx, []byte("cancel_key"), 0, loader)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))

		// 测试手动取消
		ctx2, cancel2 := context.WithCancel(context.Background())

		var wg sync.WaitGroup
		wg.Add(1)
		var resultErr error

		go func() {
			defer wg.Done()
			_, resultErr = c.GetOrCompute(ctx2, []byte("manual_cancel"), 0, loader)
		}()

		time.Sleep(50 * time.Millisecond)
		cancel2()
		wg.Wait()

		assert.Error(t, resultErr)
		assert.ErrorIs(t, resultErr, context.Canceled)
	})

	t.Run("TTL Expiration", func(t *testing.T) {
		rh, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer rh.Close()

		c := NewCtxCache(rh)
		ctx := context.Background()

		var calls int32
		loader := func(ctx context.Context) ([]byte, error) {
			count := atomic.AddInt32(&calls, 1)
			return []byte(fmt.Sprintf("value_%d", count)), nil
		}

		// 第一次加载，设置短 TTL
		val1, err := c.GetOrCompute(ctx, []byte("ttl_key"), 100*time.Millisecond, loader)
		assert.NoError(t, err)
		assert.Equal(t, []byte("value_1"), val1)

		// 立即再次访问，应该命中缓存
		val2, err := c.GetOrCompute(ctx, []byte("ttl_key"), 100*time.Millisecond, loader)
		assert.NoError(t, err)
		assert.Equal(t, []byte("value_1"), val2)
		assert.Equal(t, int32(1), atomic.LoadInt32(&calls))

		// 等待 TTL 过期
		time.Sleep(150 * time.Millisecond)

		// 再次访问，应该重新加载
		val3, err := c.GetOrCompute(ctx, []byte("ttl_key"), 100*time.Millisecond, loader)
		assert.NoError(t, err)
		assert.Equal(t, []byte("value_2"), val3)
		assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	})

	t.Run("Loader Error Handling", func(t *testing.T) {
		rh, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer rh.Close()

		c := NewCtxCache(rh)
		ctx := context.Background()

		loaderErr := errors.New("loader failed")
		loader := func(ctx context.Context) ([]byte, error) {
			return nil, loaderErr
		}

		// 测试 loader 返回错误
		_, err = c.GetOrCompute(ctx, []byte("error_key"), 0, loader)
		assert.ErrorIs(t, err, loaderErr)

		// 错误不应该被缓存，再次调用应该重试
		var calls int32
		retryLoader := func(ctx context.Context) ([]byte, error) {
			count := atomic.AddInt32(&calls, 1)
			if count == 1 {
				return nil, errors.New("first call fails")
			}
			return []byte("success"), nil
		}

		_, err = c.GetOrCompute(ctx, []byte("retry_key"), 0, retryLoader)
		assert.Error(t, err)

		val, err := c.GetOrCompute(ctx, []byte("retry_key"), 0, retryLoader)
		assert.NoError(t, err)
		assert.Equal(t, []byte("success"), val)
		assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	})

	t.Run("Concurrent Different Keys", func(t *testing.T) {
		rh, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer rh.Close()

		c := NewCtxCache(rh)
		ctx := context.Background()

		var totalCalls int32
		loader := func(key string) func(context.Context) ([]byte, error) {
			return func(ctx context.Context) ([]byte, error) {
				atomic.AddInt32(&totalCalls, 1)
				time.Sleep(50 * time.Millisecond)
				return []byte(fmt.Sprintf("value_for_%s", key)), nil
			}
		}

		const numKeys = 5
		const concurrent = 3
		var wg sync.WaitGroup

		for i := 0; i < numKeys; i++ {
			for j := 0; j < concurrent; j++ {
				wg.Add(1)
				go func(keyIndex, goroutineIndex int) {
					defer wg.Done()
					key := []byte(fmt.Sprintf("key_%d", keyIndex))
					val, err := c.GetOrCompute(ctx, key, 0, loader(fmt.Sprintf("key_%d", keyIndex)))
					assert.NoError(t, err)
					expected := fmt.Sprintf("value_for_key_%d", keyIndex)
					assert.Equal(t, []byte(expected), val)
				}(i, j)
			}
		}

		wg.Wait()

		// 每个键应该只调用一次 loader
		assert.Equal(t, int32(numKeys), atomic.LoadInt32(&totalCalls))
	})
}

func TestCtxCache_StressTesting(t *testing.T) {
	t.Run("High Concurrency Operations", func(t *testing.T) {
		rh, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer rh.Close()

		c := NewCtxCache(rh)
		ctx := context.Background()

		const workers = 50
		const operations = 100
		var wg sync.WaitGroup

		// 并发读写测试
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for op := 0; op < operations; op++ {
					key := []byte(fmt.Sprintf("stress_%d_%d", workerID, op))
					value := []byte(fmt.Sprintf("value_%d_%d", workerID, op))

					// 写入
					err := c.Set(ctx, key, value)
					assert.NoError(t, err)

					// 读取
					val, err := c.Get(ctx, key)
					assert.NoError(t, err)
					assert.Equal(t, value, val)

					// 删除
					if op%10 == 0 {
						err = c.Del(ctx, key)
						assert.NoError(t, err)
					}
				}
			}(w)
		}

		wg.Wait()
	})

	t.Run("Mixed Concurrent GetOrCompute", func(t *testing.T) {
		rh, err := NewDefaultRistrettoHandler()
		require.NoError(t, err)
		defer rh.Close()

		c := NewCtxCache(rh)
		ctx := context.Background()

		var computeCalls int32
		loader := func(ctx context.Context) ([]byte, error) {
			atomic.AddInt32(&computeCalls, 1)
			time.Sleep(10 * time.Millisecond) // 模拟计算时间
			return []byte(fmt.Sprintf("computed_%d", time.Now().UnixNano())), nil
		}

		const workers = 20
		const keyPool = 5
		var wg sync.WaitGroup

		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for i := 0; i < 10; i++ {
					key := []byte(fmt.Sprintf("mixed_key_%d", i%keyPool))
					_, err := c.GetOrCompute(ctx, key, 100*time.Millisecond, loader)
					assert.NoError(t, err)
				}
			}(w)
		}

		wg.Wait()

		// 验证计算调用次数应该远小于总请求数
		totalRequests := workers * 10
		actualCalls := atomic.LoadInt32(&computeCalls)
		t.Logf("Total requests: %d, Actual compute calls: %d", totalRequests, actualCalls)
		assert.True(t, actualCalls <= keyPool*2, "Too many compute calls, singleflight not working effectively")
	})
}

// ====================== Benchmark Tests ======================

func BenchmarkCtxCache_Set(b *testing.B) {
	rh, err := NewDefaultRistrettoHandler()
	require.NoError(b, err)
	defer rh.Close()

	c := NewCtxCache(rh)
	ctx := context.Background()
	value := make([]byte, 100)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("bench_set_%d", i))
			c.Set(ctx, key, value)
			i++
		}
	})
}

func BenchmarkCtxCache_Get(b *testing.B) {
	rh, err := NewDefaultRistrettoHandler()
	require.NoError(b, err)
	defer rh.Close()

	c := NewCtxCache(rh)
	ctx := context.Background()
	value := make([]byte, 100)

	// 预填充数据
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("bench_get_%d", i))
		c.Set(ctx, key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("bench_get_%d", i%1000))
			c.Get(ctx, key)
			i++
		}
	})
}

func BenchmarkCtxCache_GetOrCompute_Hit(b *testing.B) {
	rh, err := NewDefaultRistrettoHandler()
	require.NoError(b, err)
	defer rh.Close()

	c := NewCtxCache(rh)
	ctx := context.Background()

	loader := func(ctx context.Context) ([]byte, error) {
		return []byte("computed_value"), nil
	}

	// 预填充缓存
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("hit_key_%d", i))
		c.GetOrCompute(ctx, key, 0, loader)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("hit_key_%d", i%100))
			c.GetOrCompute(ctx, key, 0, loader)
			i++
		}
	})
}

func BenchmarkCtxCache_GetOrCompute_Miss(b *testing.B) {
	rh, err := NewDefaultRistrettoHandler()
	require.NoError(b, err)
	defer rh.Close()

	c := NewCtxCache(rh)
	ctx := context.Background()

	loader := func(ctx context.Context) ([]byte, error) {
		return []byte("computed_value"), nil
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("miss_key_%d", i))
			c.GetOrCompute(ctx, key, 0, loader)
			i++
		}
	})
}

func BenchmarkCtxCache_GetOrCompute_Singleflight(b *testing.B) {
	rh, err := NewDefaultRistrettoHandler()
	require.NoError(b, err)
	defer rh.Close()

	c := NewCtxCache(rh)
	ctx := context.Background()

	var computeCount int64
	loader := func(ctx context.Context) ([]byte, error) {
		atomic.AddInt64(&computeCount, 1)
		time.Sleep(10 * time.Millisecond) // 模拟昂贵计算
		return []byte("expensive_computation"), nil
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// 所有goroutine都尝试计算相同的键
			c.GetOrCompute(ctx, []byte("singleflight_key"), 100*time.Millisecond, loader)
		}
	})

	b.StopTimer()
	b.Logf("Total compute calls: %d for %d operations", atomic.LoadInt64(&computeCount), b.N)
}

func BenchmarkCtxCache_Mixed_Operations(b *testing.B) {
	rh, err := NewDefaultRistrettoHandler()
	require.NoError(b, err)
	defer rh.Close()

	c := NewCtxCache(rh)
	ctx := context.Background()
	value := make([]byte, 100)

	loader := func(ctx context.Context) ([]byte, error) {
		return []byte("computed"), nil
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("mixed_%d", i%1000))
			op := i % 4
			switch op {
			case 0, 1: // 50% Get operations
				c.Get(ctx, key)
			case 2: // 25% Set operations
				c.Set(ctx, key, value)
			case 3: // 25% GetOrCompute operations
				c.GetOrCompute(ctx, key, 100*time.Millisecond, loader)
			}
			i++
		}
	})
}

func BenchmarkCtxCache_Different_TTL(b *testing.B) {
	rh, err := NewDefaultRistrettoHandler()
	require.NoError(b, err)
	defer rh.Close()

	c := NewCtxCache(rh)
	ctx := context.Background()
	value := make([]byte, 100)

	ttls := []time.Duration{
		0,                      // No TTL
		100 * time.Millisecond, // Short TTL
		time.Second,            // Medium TTL
		-1,                     // Never expire
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("ttl_%d", i))
			ttl := ttls[i%len(ttls)]
			c.SetWithTTL(ctx, key, value, ttl)
			i++
		}
	})
}
