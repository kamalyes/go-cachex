/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-06 21:15:15
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 21:25:00
 * @FilePath: \go-cachex\expiring_test.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExpiring_SetGet(t *testing.T) {
	assert := assert.New(t)
	h := NewExpiringHandler(20 * time.Millisecond)
	defer h.Close()

	// 测试基本的 Set/Get
	err := h.Set([]byte("a"), []byte("1"))
	assert.NoError(err, "Set should succeed")

	v, err := h.Get([]byte("a"))
	assert.NoError(err, "Get should succeed")
	assert.Equal("1", string(v), "Get should return the value we set")

	// 测试不存在的键
	_, err = h.Get([]byte("non-existent"))
	assert.ErrorIs(err, ErrNotFound, "Get non-existent key should return ErrNotFound")
}

func TestExpiring_TTL(t *testing.T) {
	assert := assert.New(t)
	h := NewExpiringHandler(10 * time.Millisecond)
	defer h.Close()

	// 设置带 TTL 的键值对
	err := h.SetWithTTL([]byte("b"), []byte("2"), 30*time.Millisecond)
	assert.NoError(err, "SetWithTTL should succeed")

	// 获取 TTL
	ttl, err := h.GetTTL([]byte("b"))
	assert.NoError(err, "GetTTL should succeed")
	assert.True(ttl > 0 && ttl <= 30*time.Millisecond, "TTL should be positive and not exceed set value")

	// TTL 到期前应该能获取值
	v, err := h.Get([]byte("b"))
	assert.NoError(err, "Get before expiry should succeed")
	assert.Equal("2", string(v), "Get should return correct value before expiry")

	// 等待过期
	time.Sleep(50 * time.Millisecond)

	// TTL 到期后应该返回 ErrNotFound
	_, err = h.Get([]byte("b"))
	assert.ErrorIs(err, ErrNotFound, "Get after expiry should return ErrNotFound")

	// TTL 到期后 GetTTL 也应该返回 ErrNotFound
	_, err = h.GetTTL([]byte("b"))
	assert.ErrorIs(err, ErrNotFound, "GetTTL after expiry should return ErrNotFound")
}

func TestExpiring_Delete(t *testing.T) {
	assert := assert.New(t)
	h := NewExpiringHandler(10 * time.Millisecond)
	defer h.Close()

	// 设置一个键值对
	err := h.Set([]byte("c"), []byte("3"))
	assert.NoError(err, "Set should succeed")

	// 删除键
	err = h.Del([]byte("c"))
	assert.NoError(err, "Del should succeed")

	// 获取已删除的键应该返回 ErrNotFound
	_, err = h.Get([]byte("c"))
	assert.ErrorIs(err, ErrNotFound, "Get after delete should return ErrNotFound")
}

func TestExpiring_Close(t *testing.T) {
	assert := assert.New(t)
	h := NewExpiringHandler(10 * time.Millisecond)

	// 设置一些数据
	err := h.Set([]byte("d"), []byte("4"))
	assert.NoError(err, "Set should succeed")

	// 关闭缓存
	err = h.Close()
	assert.NoError(err, "Close should succeed")

	// 关闭后的操作应该返回错误
	err = h.Set([]byte("e"), []byte("5"))
	assert.Error(err, "Set after close should fail")

	_, err = h.Get([]byte("d"))
	assert.Error(err, "Get after close should fail")

	// 重复关闭应该是安全的
	err = h.Close()
	assert.NoError(err, "Second close should be safe")
}

func TestExpiring_Concurrency(t *testing.T) {
	assert := assert.New(t)
	h := NewExpiringHandler(10 * time.Millisecond)
	defer h.Close()

	// 并发写入不同的键
	for i := 0; i < 100; i++ {
		key := []byte(time.Now().String())
		err := h.Set(key, []byte("value"))
		assert.NoError(err, "Concurrent Set should succeed")

		// 立即读取
		v, err := h.Get(key)
		assert.NoError(err, "Concurrent Get should succeed")
		assert.Equal("value", string(v), "Concurrent Get should return correct value")
	}
}

// 详细测试用例
func TestExpiring_DetailedOperations(t *testing.T) {
	t.Run("Empty Key and Value", func(t *testing.T) {
		h := NewExpiringHandler(10 * time.Millisecond)
		defer h.Close()
		
		// 测试空键
		err := h.Set([]byte{}, []byte("value"))
		assert.NoError(t, err)
		
		val, err := h.Get([]byte{})
		assert.NoError(t, err)
		assert.Equal(t, "value", string(val))
		
		// 测试空值
		err = h.Set([]byte("key"), []byte{})
		assert.NoError(t, err)
		
		val, err = h.Get([]byte("key"))
		assert.NoError(t, err)
		assert.Empty(t, val)
	})

	t.Run("Value Updates", func(t *testing.T) {
		h := NewExpiringHandler(10 * time.Millisecond)
		defer h.Close()
		
		key := []byte("key")
		
		// 设置初始值
		err := h.Set(key, []byte("value1"))
		assert.NoError(t, err)
		
		val, err := h.Get(key)
		assert.NoError(t, err)
		assert.Equal(t, "value1", string(val))
		
		// 更新值
		err = h.Set(key, []byte("value2"))
		assert.NoError(t, err)
		
		val, err = h.Get(key)
		assert.NoError(t, err)
		assert.Equal(t, "value2", string(val))
	})

	t.Run("TTL Edge Cases", func(t *testing.T) {
		h := NewExpiringHandler(10 * time.Millisecond)
		defer h.Close()
		
		// 测试零 TTL（应该立即过期）
		err := h.SetWithTTL([]byte("zero"), []byte("value"), 0)
		assert.NoError(t, err)
		
		// 立即检查应该已经过期
		_, err = h.Get([]byte("zero"))
		assert.ErrorIs(t, err, ErrNotFound)
		
		// 测试 -1 TTL（永不过期）
		err = h.SetWithTTL([]byte("forever"), []byte("value"), -1)
		assert.NoError(t, err)
		
		// 应该能立即获取到
		val, err := h.Get([]byte("forever"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("value"), val)
		
		// 过一段时间后仍应该存在  
		time.Sleep(200 * time.Millisecond) // 等待超过清理间隔
		val, err = h.Get([]byte("forever"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("value"), val)
		
		// 测试小于 -1 的TTL（应该返回错误）
		err = h.SetWithTTL([]byte("invalid"), []byte("value"), -2*time.Second)
		assert.ErrorIs(t, err, ErrInvalidTTL, "TTL less than -1 should return error")
	})

	t.Run("Auto Cleanup Verification", func(t *testing.T) {
		h := NewExpiringHandler(5 * time.Millisecond) // 短清理间隔
		defer h.Close()
		
		// 设置多个短期键
		for i := 0; i < 10; i++ {
			key := []byte(fmt.Sprintf("cleanup-%d", i))
			err := h.SetWithTTL(key, []byte("value"), 10*time.Millisecond)
			assert.NoError(t, err)
		}
		
		// 验证键存在
		for i := 0; i < 10; i++ {
			key := []byte(fmt.Sprintf("cleanup-%d", i))
			_, err := h.Get(key)
			assert.NoError(t, err, "Key should exist before expiry")
		}
		
		// 等待过期和清理
		time.Sleep(50 * time.Millisecond)
		
		// 验证键已被清理
		for i := 0; i < 10; i++ {
			key := []byte(fmt.Sprintf("cleanup-%d", i))
			_, err := h.Get(key)
			assert.ErrorIs(t, err, ErrNotFound, "Key should be cleaned up after expiry")
		}
	})

	t.Run("Mixed TTL and Non-TTL", func(t *testing.T) {
		h := NewExpiringHandler(10 * time.Millisecond)
		defer h.Close()
		
		// 设置永久键
		err := h.Set([]byte("permanent"), []byte("permanent-value"))
		assert.NoError(t, err)
		
		// 设置临时键
		err = h.SetWithTTL([]byte("temporary"), []byte("temp-value"), 20*time.Millisecond)
		assert.NoError(t, err)
		
		// 等待临时键过期
		time.Sleep(40 * time.Millisecond)
		
		// 永久键应该仍然存在
		val, err := h.Get([]byte("permanent"))
		assert.NoError(t, err)
		assert.Equal(t, "permanent-value", string(val))
		
		// 临时键应该已过期
		_, err = h.Get([]byte("temporary"))
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestExpiring_ConcurrentOperations(t *testing.T) {
	t.Run("Concurrent Reads and Writes", func(t *testing.T) {
		h := NewExpiringHandler(20 * time.Millisecond)
		defer h.Close()
		
		const numGoroutines = 10
		const numOpsPerGoroutine = 100
		
		var wg sync.WaitGroup
		
		// 并发写入
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < numOpsPerGoroutine; j++ {
					key := fmt.Sprintf("key-%d-%d", id, j)
					value := fmt.Sprintf("value-%d-%d", id, j)
					err := h.Set([]byte(key), []byte(value))
					assert.NoError(t, err)
				}
			}(i)
		}
		
		// 并发读取
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < numOpsPerGoroutine; j++ {
					key := fmt.Sprintf("key-%d-%d", id, j%10)
					h.Get([]byte(key)) // 忽略错误，因为可能不存在
				}
			}(i)
		}
		
		wg.Wait()
	})

	t.Run("Concurrent TTL Operations", func(t *testing.T) {
		h := NewExpiringHandler(5 * time.Millisecond)
		defer h.Close()
		
		const numGoroutines = 5
		const numKeysPerGoroutine = 20
		
		var wg sync.WaitGroup
		
		// 并发设置带 TTL 的键
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < numKeysPerGoroutine; j++ {
					key := fmt.Sprintf("ttl-key-%d-%d", id, j)
					value := fmt.Sprintf("ttl-value-%d-%d", id, j)
					ttl := time.Duration(rand.Intn(50)+10) * time.Millisecond
					err := h.SetWithTTL([]byte(key), []byte(value), ttl)
					assert.NoError(t, err)
				}
			}(i)
		}
		
		wg.Wait()
		
		// 等待一些键过期
		time.Sleep(100 * time.Millisecond)
		
		// 检查剩余的键
		var remainingKeys int
		for i := 0; i < numGoroutines; i++ {
			for j := 0; j < numKeysPerGoroutine; j++ {
				key := fmt.Sprintf("ttl-key-%d-%d", i, j)
				if _, err := h.Get([]byte(key)); err == nil {
					remainingKeys++
				}
			}
		}
		
		t.Logf("Remaining keys after expiry: %d/%d", remainingKeys, numGoroutines*numKeysPerGoroutine)
		assert.True(t, remainingKeys < numGoroutines*numKeysPerGoroutine, "Some keys should have expired")
	})
}

func TestExpiring_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}
	
	h := NewExpiringHandler(10 * time.Millisecond)
	defer h.Close()
	
	const numItems = 10000
	
	// 批量插入
	for i := 0; i < numItems; i++ {
		key := []byte(fmt.Sprintf("stress-key-%d", i))
		value := []byte(fmt.Sprintf("stress-value-%d", i))
		
		if i%2 == 0 {
			// 一半使用 TTL
			err := h.SetWithTTL(key, value, time.Duration(rand.Intn(100)+50)*time.Millisecond)
			assert.NoError(t, err)
		} else {
			// 一半不使用 TTL
			err := h.Set(key, value)
			assert.NoError(t, err)
		}
	}
	
	// 随机读取
	var successfulReads int
	for i := 0; i < 1000; i++ {
		idx := rand.Intn(numItems)
		key := []byte(fmt.Sprintf("stress-key-%d", idx))
		if _, err := h.Get(key); err == nil {
			successfulReads++
		}
	}
	
	t.Logf("Successful reads: %d/1000", successfulReads)
	assert.True(t, successfulReads > 0, "Should have some successful reads")
}

// 性能基准测试
func BenchmarkExpiring_Set(b *testing.B) {
	h := NewExpiringHandler(100 * time.Millisecond)
	defer h.Close()
	
	keys := make([][]byte, b.N)
	values := make([][]byte, b.N)
	
	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%d", i))
		values[i] = []byte(fmt.Sprintf("value-%d", i))
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Set(keys[i], values[i])
	}
}

func BenchmarkExpiring_Get(b *testing.B) {
	h := NewExpiringHandler(100 * time.Millisecond)
	defer h.Close()
	
	// 预填充数据
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		h.Set(key, value)
	}
	
	keys := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%d", i%1000))
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Get(keys[i])
	}
}

func BenchmarkExpiring_SetWithTTL(b *testing.B) {
	h := NewExpiringHandler(100 * time.Millisecond)
	defer h.Close()
	
	keys := make([][]byte, b.N)
	values := make([][]byte, b.N)
	
	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%d", i))
		values[i] = []byte(fmt.Sprintf("value-%d", i))
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.SetWithTTL(keys[i], values[i], time.Hour)
	}
}

func BenchmarkExpiring_Mixed(b *testing.B) {
	h := NewExpiringHandler(100 * time.Millisecond)
	defer h.Close()
	
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
			h.Set(keys[idx], values[idx])
		case 1: // Get
			h.Get(keys[idx])
		case 2: // Del
			h.Del(keys[idx])
		}
	}
}

func BenchmarkExpiring_ConcurrentAccess(b *testing.B) {
	h := NewExpiringHandler(100 * time.Millisecond)
	defer h.Close()
	
	// 预填充一些数据
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		h.Set(key, value)
	}
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			idx := rand.Intn(100)
			key := []byte(fmt.Sprintf("key-%d", idx))
			
			switch rand.Intn(2) {
			case 0: // Read
				h.Get(key)
			case 1: // Write
				value := []byte(fmt.Sprintf("value-%d", rand.Intn(1000)))
				h.Set(key, value)
			}
		}
	})
}

func BenchmarkExpiring_TTLOperations(b *testing.B) {
	h := NewExpiringHandler(100 * time.Millisecond)
	defer h.Close()
	
	keys := make([][]byte, b.N)
	values := make([][]byte, b.N)
	ttls := make([]time.Duration, b.N)
	
	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%d", i))
		values[i] = []byte(fmt.Sprintf("value-%d", i))
		ttls[i] = time.Duration(rand.Intn(3600)+60) * time.Second
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.SetWithTTL(keys[i], values[i], ttls[i])
	}
}

func BenchmarkExpiring_CleanupInterval(b *testing.B) {
	intervals := []time.Duration{
		10 * time.Millisecond,
		50 * time.Millisecond,
		100 * time.Millisecond,
		500 * time.Millisecond,
	}
	
	for _, interval := range intervals {
		b.Run(fmt.Sprintf("Interval-%v", interval), func(b *testing.B) {
			h := NewExpiringHandler(interval)
			defer h.Close()
			
			for i := 0; i < b.N; i++ {
				key := []byte(fmt.Sprintf("key-%d", i))
				value := []byte(fmt.Sprintf("value-%d", i))
				h.SetWithTTL(key, value, 2*interval)
			}
		})
	}
}
