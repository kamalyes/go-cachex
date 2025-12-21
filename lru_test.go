/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 21:00:00
 * @FilePath: \go-cachex\lru_test.go
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

func TestLRU_SetGet(t *testing.T) {
	assert := assert.New(t)
	h := NewLRUHandler(2)
	defer h.Close()

	// 设置两个键值对
	err := h.Set([]byte("k1"), []byte("v1"))
	assert.NoError(err, "First Set should succeed")

	err = h.Set([]byte("k2"), []byte("v2"))
	assert.NoError(err, "Second Set should succeed")

	// 验证键存在且值正确
	v, err := h.Get([]byte("k1"))
	assert.NoError(err, "Get k1 should succeed")
	assert.Equal("v1", string(v), "k1 should have correct value")

	// 检查 k1 的访问是否更新了其位置（使其更新）
	v, err = h.Get([]byte("k1"))
	assert.NoError(err, "Second Get k1 should succeed")
	assert.Equal("v1", string(v), "k1 should still have correct value")

	// 添加第三个键，由于 k1 最近被访问过，应该驱逐 k2
	err = h.Set([]byte("k3"), []byte("v3"))
	assert.NoError(err, "Third Set should succeed")

	// 验证 k2 已被驱逐
	_, err = h.Get([]byte("k2"))
	assert.ErrorIs(err, ErrNotFound, "k2 should be evicted")

	// 验证 k1 和 k3 仍然存在
	v, err = h.Get([]byte("k1"))
	assert.NoError(err, "k1 should still exist")
	assert.Equal("v1", string(v), "k1 should have correct value")

	v, err = h.Get([]byte("k3"))
	assert.NoError(err, "k3 should exist")
	assert.Equal("v3", string(v), "k3 should have correct value")
}

func TestLRU_TTL(t *testing.T) {
	assert := assert.New(t)
	h := NewLRUHandler(10)
	defer h.Close()

	// 设置带 TTL 的键值对
	err := h.SetWithTTL([]byte("kt"), []byte("vt"), 50*time.Millisecond)
	assert.NoError(err, "SetWithTTL should succeed")

	// 验证 TTL
	ttl, err := h.GetTTL([]byte("kt"))
	assert.NoError(err, "GetTTL should succeed")
	assert.True(ttl > 0 && ttl <= 50*time.Millisecond, "TTL should be positive and not exceed set value")

	// TTL 到期前应该能获取值
	v, err := h.Get([]byte("kt"))
	assert.NoError(err, "Get before expiry should succeed")
	assert.Equal("vt", string(v), "Should get correct value before expiry")

	// 等待过期
	time.Sleep(80 * time.Millisecond)

	// 验证过期后无法获取
	_, err = h.Get([]byte("kt"))
	assert.ErrorIs(err, ErrNotFound, "Key should be expired")

	// 验证过期后 TTL 也返回错误
	_, err = h.GetTTL([]byte("kt"))
	assert.ErrorIs(err, ErrNotFound, "GetTTL after expiry should return ErrNotFound")
}

func TestLRU_Delete(t *testing.T) {
	assert := assert.New(t)
	h := NewLRUHandler(10)
	defer h.Close()

	// 设置一些键值对
	err := h.Set([]byte("k1"), []byte("v1"))
	assert.NoError(err, "Set should succeed")

	// 删除键
	err = h.Del([]byte("k1"))
	assert.NoError(err, "Del should succeed")

	// 验证键已被删除
	_, err = h.Get([]byte("k1"))
	assert.ErrorIs(err, ErrNotFound, "Key should be deleted")

	// 删除不存在的键也应该成功
	err = h.Del([]byte("non-existent"))
	assert.NoError(err, "Del non-existent key should succeed")
}

func TestLRU_Capacity(t *testing.T) {
	assert := assert.New(t)
	h := NewLRUHandler(3)
	defer h.Close()

	// 添加到容量上限
	for i := 0; i < 3; i++ {
		key := []byte(string(rune('a' + i)))
		err := h.Set(key, []byte("value"))
		assert.NoError(err, "Set within capacity should succeed")
	}

	// 所有键都应该存在
	for i := 0; i < 3; i++ {
		key := []byte(string(rune('a' + i)))
		_, err := h.Get(key)
		assert.NoError(err, "All keys within capacity should exist")
	}

	// 添加第四个键（应该驱逐最老的键 'a'）
	err := h.Set([]byte("d"), []byte("value"))
	assert.NoError(err, "Set beyond capacity should succeed with eviction")

	// 最早的键应该被驱逐
	_, err = h.Get([]byte("a"))
	assert.ErrorIs(err, ErrNotFound, "Oldest key should be evicted")
}

// 详细测试用例

func TestLRU_DetailedOperations(t *testing.T) {
	t.Run("Empty Key and Value", func(t *testing.T) {
		h := NewLRUHandler(10)
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

	t.Run("LRU Order Verification", func(t *testing.T) {
		h := NewLRUHandler(3)
		defer h.Close()

		// 添加三个项目
		h.Set([]byte("a"), []byte("1"))
		h.Set([]byte("b"), []byte("2"))
		h.Set([]byte("c"), []byte("3"))

		// 访问 'a' 使其成为最近使用的
		h.Get([]byte("a"))

		// 添加新项目，'b' 应该被驱逐（因为 'a' 被访问了）
		h.Set([]byte("d"), []byte("4"))

		// 验证 'b' 被驱逐
		_, err := h.Get([]byte("b"))
		assert.ErrorIs(t, err, ErrNotFound)

		// 验证其他键存在
		_, err = h.Get([]byte("a"))
		assert.NoError(t, err)
		_, err = h.Get([]byte("c"))
		assert.NoError(t, err)
		_, err = h.Get([]byte("d"))
		assert.NoError(t, err)
	})

	t.Run("Value Updates", func(t *testing.T) {
		h := NewLRUHandler(10)
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
		h := NewLRUHandler(10)
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
		time.Sleep(10 * time.Millisecond)
		val, err = h.Get([]byte("forever"))
		assert.NoError(t, err)
		assert.Equal(t, []byte("value"), val)

		// 测试小于 -1 的TTL（应该返回错误）
		err = h.SetWithTTL([]byte("invalid"), []byte("value"), -2*time.Second)
		assert.ErrorIs(t, err, ErrInvalidTTL, "TTL less than -1 should return error")
	})
}

func TestLRU_ConcurrentAccess(t *testing.T) {
	h := NewLRUHandler(1000)
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
				key := fmt.Sprintf("key-%d-%d", id, j%10) // 读取一些可能存在的键
				h.Get([]byte(key))                        // 忽略错误，因为可能不存在
			}
		}(i)
	}

	wg.Wait()
}

func TestLRU_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	h := NewLRUHandler(1000)
	defer h.Close()

	// 大量数据插入
	const numItems = 10000
	for i := 0; i < numItems; i++ {
		key := []byte(fmt.Sprintf("stress-key-%d", i))
		value := []byte(fmt.Sprintf("stress-value-%d", i))
		err := h.Set(key, value)
		assert.NoError(t, err)
	}

	// 验证最近的 1000 个项目应该存在（容量限制）
	found := 0
	for i := numItems - 1000; i < numItems; i++ {
		key := []byte(fmt.Sprintf("stress-key-%d", i))
		if _, err := h.Get(key); err == nil {
			found++
		}
	}

	// 应该找到大部分最近的项目
	assert.True(t, found > 500, "Should find significant portion of recent items")
}

// 性能基准测试

func BenchmarkLRU_Set(b *testing.B) {
	h := NewLRUHandler(10000)
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

func BenchmarkLRU_Get(b *testing.B) {
	h := NewLRUHandler(10000)
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

func BenchmarkLRU_SetWithTTL(b *testing.B) {
	h := NewLRUHandler(10000)
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

func BenchmarkLRU_Mixed(b *testing.B) {
	h := NewLRUHandler(10000)
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

func BenchmarkLRU_ConcurrentAccess(b *testing.B) {
	h := NewLRUHandler(10000)
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

func BenchmarkLRU_EvictionPerformance(b *testing.B) {
	const capacity = 1000
	h := NewLRUHandler(capacity)
	defer h.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		h.Set(key, value)
	}
}

// 内存使用基准测试
func BenchmarkLRU_MemoryUsage(b *testing.B) {
	sizes := []int{100, 1000, 10000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size-%d", size), func(b *testing.B) {
			h := NewLRUHandler(size)
			defer h.Close()

			for i := 0; i < b.N && i < size; i++ {
				key := []byte(fmt.Sprintf("key-%d", i))
				value := make([]byte, 1024) // 1KB value
				copy(value, fmt.Sprintf("value-%d", i))
				h.Set(key, value)
			}
		})
	}
}
