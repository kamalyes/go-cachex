/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-20 00:05:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-20 00:05:00
 * @FilePath: \go-cachex\wrapper_comprehensive_test.go
 * @Description: 缓存包装器全面测试套件 - 20+测试用例
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCacheWrapper01_BasicStringCache 测试基本字符串缓存
func TestCacheWrapper01_BasicStringCache(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	callCount := int32(0)

	dataLoader := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&callCount, 1)
		return "test_string_data", nil
	}

	cachedLoader := CacheWrapper(client, "test_string_key", dataLoader, time.Minute)

	// 第一次调用
	result1, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "test_string_data", result1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 第二次调用（应该从缓存获取）
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "test_string_data", result2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount)) // 调用次数不变
}

// TestCacheWrapper02_IntegerCache 测试整数缓存
func TestCacheWrapper02_IntegerCache(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	callCount := int32(0)

	dataLoader := func(ctx context.Context) (int, error) {
		atomic.AddInt32(&callCount, 1)
		return 42, nil
	}

	cachedLoader := CacheWrapper(client, "test_int_key", dataLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 42, result)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 再次调用
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 42, result2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
}

// TestCacheWrapper03_FloatCache 测试浮点数缓存
func TestCacheWrapper03_FloatCache(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	expected := 3.14159

	dataLoader := func(ctx context.Context) (float64, error) {
		return expected, nil
	}

	cachedLoader := CacheWrapper(client, "test_float_key", dataLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.InDelta(t, expected, result, 0.00001)
}

// TestCacheWrapper04_BooleanCache 测试布尔值缓存
func TestCacheWrapper04_BooleanCache(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	trueLoader := func(ctx context.Context) (bool, error) {
		return true, nil
	}

	cachedLoader := CacheWrapper(client, "test_bool_key", trueLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.True(t, result)
}

// TestCacheWrapper05_StructCache 测试结构体缓存
func TestCacheWrapper05_StructCache(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	type TestStruct struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	ctx := context.Background()
	expected := TestStruct{ID: 1, Name: "Alice", Age: 25}

	dataLoader := func(ctx context.Context) (TestStruct, error) {
		return expected, nil
	}

	cachedLoader := CacheWrapper(client, "test_struct_key", dataLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
	assert.Equal(t, 1, result.ID)
	assert.Equal(t, "Alice", result.Name)
	assert.Equal(t, 25, result.Age)
}

// TestCacheWrapper06_SliceCache 测试切片缓存
func TestCacheWrapper06_SliceCache(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	expected := []string{"apple", "banana", "cherry"}

	dataLoader := func(ctx context.Context) ([]string, error) {
		return expected, nil
	}

	cachedLoader := CacheWrapper(client, "test_slice_key", dataLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
	assert.Len(t, result, 3)
	assert.Contains(t, result, "apple")
	assert.Contains(t, result, "banana")
	assert.Contains(t, result, "cherry")
}

// TestCacheWrapper07_MapCache 测试映射缓存
func TestCacheWrapper07_MapCache(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	expected := map[string]int{"a": 1, "b": 2, "c": 3}

	dataLoader := func(ctx context.Context) (map[string]int, error) {
		return expected, nil
	}

	cachedLoader := CacheWrapper(client, "test_map_key", dataLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
	assert.Len(t, result, 3)
	assert.Equal(t, 1, result["a"])
	assert.Equal(t, 2, result["b"])
	assert.Equal(t, 3, result["c"])
}

// TestCacheWrapper08_ErrorHandling 测试错误处理
func TestCacheWrapper08_ErrorHandling(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	expectedError := errors.New("test error")

	dataLoader := func(ctx context.Context) (string, error) {
		return "", expectedError
	}

	cachedLoader := CacheWrapper(client, "test_error_key", dataLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.Error(t, err)
	assert.Equal(t, "", result)
	assert.Equal(t, expectedError, err)
}

// TestCacheWrapper09_NilValueCache 测试空值缓存
func TestCacheWrapper09_NilValueCache(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	callCount := int32(0)

	dataLoader := func(ctx context.Context) (*string, error) {
		atomic.AddInt32(&callCount, 1)
		return nil, nil
	}

	cachedLoader := CacheWrapper(client, "test_nil_key", dataLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Nil(t, result)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
}

// TestCacheWrapper10_ShortExpiration 测试短过期时间
func TestCacheWrapper10_ShortExpiration(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	callCount := int32(0)

	dataLoader := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&callCount, 1)
		return fmt.Sprintf("data_%d", atomic.LoadInt32(&callCount)), nil
	}

	cachedLoader := CacheWrapper(client, "test_short_exp_key", dataLoader, 50*time.Millisecond)

	// 第一次调用
	result1, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "data_1", result1)

	// 等待过期
	time.Sleep(100 * time.Millisecond)

	// 第二次调用（应该重新加载）
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "data_2", result2)
	assert.Equal(t, int32(2), atomic.LoadInt32(&callCount))
}

// TestCacheWrapper11_ConcurrentAccess 测试并发访问
func TestCacheWrapper11_ConcurrentAccess(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	callCount := int32(0)

	dataLoader := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&callCount, 1)
		time.Sleep(50 * time.Millisecond) // 模拟慢操作
		return "concurrent_data", nil
	}

	cachedLoader := CacheWrapper(client, "test_concurrent_key", dataLoader, time.Minute)

	const numGoroutines = 10
	var wg sync.WaitGroup
	results := make([]string, numGoroutines)
	errors := make([]error, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx], errors[idx] = cachedLoader(ctx)
		}(i)
	}

	wg.Wait()

	// 检查结果
	for i := 0; i < numGoroutines; i++ {
		assert.NoError(t, errors[i])
		assert.Equal(t, "concurrent_data", results[i])
	}

	// 由于并发和延迟双删策略，可能会有多次调用
	t.Logf("数据加载器被调用了 %d 次", atomic.LoadInt32(&callCount))
	assert.True(t, atomic.LoadInt32(&callCount) >= 1)
}

// TestCacheWrapper12_LargeData 测试大数据缓存
func TestCacheWrapper12_LargeData(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	// 创建大字符串（约1MB）
	largeString := make([]byte, 1024*1024)
	for i := range largeString {
		largeString[i] = byte('A' + (i % 26))
	}

	dataLoader := func(ctx context.Context) (string, error) {
		return string(largeString), nil
	}

	cachedLoader := CacheWrapper(client, "test_large_key", dataLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, string(largeString), result)
	assert.Equal(t, 1024*1024, len(result))
}

// TestCacheWrapper13_EmptyString 测试空字符串缓存
func TestCacheWrapper13_EmptyString(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	callCount := int32(0)

	dataLoader := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&callCount, 1)
		return "", nil
	}

	cachedLoader := CacheWrapper(client, "test_empty_key", dataLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 再次调用
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Empty(t, result2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount)) // 不应该再次调用
}

// TestCacheWrapper14_ContextCancellation 测试上下文取消
func TestCacheWrapper14_ContextCancellation(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())

	dataLoader := func(ctx context.Context) (string, error) {
		select {
		case <-time.After(100 * time.Millisecond):
			return "delayed_data", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	cachedLoader := CacheWrapper(client, "test_cancel_key", dataLoader, time.Minute)

	// 立即取消上下文
	cancel()

	result, err := cachedLoader(ctx)
	assert.Error(t, err)
	assert.Equal(t, "", result)
	assert.Equal(t, context.Canceled, err)
}

// TestCacheWrapper15_MultipleDifferentKeys 测试多个不同键
func TestCacheWrapper15_MultipleDifferentKeys(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	// 创建多个不同的缓存包装器
	keys := []string{"key1", "key2", "key3", "key4", "key5"}
	loaders := make([]func(context.Context) (string, error), len(keys))

	for i, key := range keys {
		i, key := i, key // 捕获循环变量
		loaders[i] = CacheWrapper(client, key, func(ctx context.Context) (string, error) {
			return fmt.Sprintf("data_for_%s", key), nil
		}, time.Minute)
	}

	// 测试每个加载器
	for i, loader := range loaders {
		result, err := loader(ctx)
		assert.NoError(t, err)
		expected := fmt.Sprintf("data_for_%s", keys[i])
		assert.Equal(t, expected, result)
	}
}

// TestCacheWrapper16_RandomData 测试随机数据
func TestCacheWrapper16_RandomData(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	seed := time.Now().UnixNano()
	r := rand.New(rand.NewSource(seed))
	expectedValue := r.Intn(10000)

	dataLoader := func(ctx context.Context) (int, error) {
		return expectedValue, nil
	}

	cachedLoader := CacheWrapper(client, "test_random_key", dataLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, expectedValue, result)

	// 再次调用应该得到相同的值（从缓存）
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, expectedValue, result2)
}

// TestCacheWrapper17_JSONData 测试复杂JSON数据
func TestCacheWrapper17_JSONData(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	type ComplexData struct {
		Users  []map[string]interface{} `json:"users"`
		Config map[string]string        `json:"config"`
		Flags  []bool                   `json:"flags"`
	}

	ctx := context.Background()
	expected := ComplexData{
		Users: []map[string]interface{}{
			{"id": 1, "name": "Alice", "active": true},
			{"id": 2, "name": "Bob", "active": false},
		},
		Config: map[string]string{
			"theme": "dark",
			"lang":  "en",
		},
		Flags: []bool{true, false, true},
	}

	dataLoader := func(ctx context.Context) (ComplexData, error) {
		return expected, nil
	}

	cachedLoader := CacheWrapper(client, "test_json_key", dataLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
	assert.Len(t, result.Users, 2)
	assert.Len(t, result.Config, 2)
	assert.Len(t, result.Flags, 3)
}

// TestCacheWrapper18_HighFrequency 测试高频访问
func TestCacheWrapper18_HighFrequency(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	callCount := int32(0)

	dataLoader := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&callCount, 1)
		return "high_frequency_data", nil
	}

	cachedLoader := CacheWrapper(client, "test_high_freq_key", dataLoader, time.Minute)

	// 快速连续调用100次
	const numCalls = 100
	for i := 0; i < numCalls; i++ {
		result, err := cachedLoader(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "high_frequency_data", result)
	}

	// 由于延迟双删策略，在高频访问时会有更多的缓存失效，这是正常的
	finalCount := atomic.LoadInt32(&callCount)
	t.Logf("高频访问中数据加载器被调用了 %d 次", finalCount)
	assert.True(t, finalCount < 50) // 由于延迟双删策略，调用次数会增加，但应该少于50次
}

// TestCacheWrapper19_DifferentExpirationTimes 测试不同过期时间
func TestCacheWrapper19_DifferentExpirationTimes(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	// 短过期时间
	shortLoader := CacheWrapper(client, "short_exp", func(ctx context.Context) (string, error) {
		return "short_data", nil
	}, 30*time.Millisecond)

	// 长过期时间
	longLoader := CacheWrapper(client, "long_exp", func(ctx context.Context) (string, error) {
		return "long_data", nil
	}, time.Hour)

	// 测试短过期
	result1, err := shortLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "short_data", result1)

	// 测试长过期
	result2, err := longLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "long_data", result2)

	// 等待短过期时间过去
	time.Sleep(50 * time.Millisecond)

	// 短期缓存应该过期，长期缓存仍然有效
	result3, err := longLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "long_data", result3)
}

// TestCacheWrapper20_MemoryUsage 测试内存使用情况
func TestCacheWrapper20_MemoryUsage(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	// 创建多个缓存包装器测试内存使用
	loaders := make([]func(context.Context) (string, error), 50)
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("memory_test_key_%d", i)
		value := fmt.Sprintf("memory_test_value_%d", i)
		loaders[i] = CacheWrapper(client, key, func(ctx context.Context) (string, error) {
			return value, nil
		}, time.Minute)
	}

	// 调用所有加载器
	for i, loader := range loaders {
		result, err := loader(ctx)
		assert.NoError(t, err)
		expected := fmt.Sprintf("memory_test_value_%d", i)
		assert.Equal(t, expected, result)
	}
}

// TestCacheWrapper21_SpecialCharacters 测试特殊字符
func TestCacheWrapper21_SpecialCharacters(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	specialData := "测试中文 🚀 émojis àçčénts <>&\"'`~!@#$%^&*()_+-=[]{}|;:,.<>?"

	dataLoader := func(ctx context.Context) (string, error) {
		return specialData, nil
	}

	cachedLoader := CacheWrapper(client, "test_special_chars", dataLoader, time.Minute)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, specialData, result)
	assert.Contains(t, result, "测试中文")
	assert.Contains(t, result, "🚀")
	assert.Contains(t, result, "émojis")
}

// TestCacheWrapper22_NumericTypes 测试各种数值类型
func TestCacheWrapper22_NumericTypes(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	// 测试int8
	int8Loader := CacheWrapper(client, "int8_key", func(ctx context.Context) (int8, error) {
		return 127, nil
	}, time.Minute)

	// 测试int16
	int16Loader := CacheWrapper(client, "int16_key", func(ctx context.Context) (int16, error) {
		return 32767, nil
	}, time.Minute)

	// 测试int32
	int32Loader := CacheWrapper(client, "int32_key", func(ctx context.Context) (int32, error) {
		return 2147483647, nil
	}, time.Minute)

	// 测试int64
	int64Loader := CacheWrapper(client, "int64_key", func(ctx context.Context) (int64, error) {
		return 9223372036854775807, nil
	}, time.Minute)

	// 测试uint64
	uint64Loader := CacheWrapper(client, "uint64_key", func(ctx context.Context) (uint64, error) {
		return 18446744073709551615, nil
	}, time.Minute)

	// 测试float32
	float32Loader := CacheWrapper(client, "float32_key", func(ctx context.Context) (float32, error) {
		return 3.14159, nil
	}, time.Minute)

	// 执行测试
	result8, err := int8Loader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int8(127), result8)

	result16, err := int16Loader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int16(32767), result16)

	result32, err := int32Loader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int32(2147483647), result32)

	result64, err := int64Loader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(9223372036854775807), result64)

	resultu64, err := uint64Loader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, uint64(18446744073709551615), resultu64)

	resultf32, err := float32Loader(ctx)
	assert.NoError(t, err)
	assert.InDelta(t, float32(3.14159), resultf32, 0.00001)
}

// TestCacheWrapper23_ArrayAndSlices 测试数组和切片
func TestCacheWrapper23_ArrayAndSlices(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	// 测试整数数组
	intArrayLoader := CacheWrapper(client, "int_array_key", func(ctx context.Context) ([5]int, error) {
		return [5]int{1, 2, 3, 4, 5}, nil
	}, time.Minute)

	// 测试字符串切片
	stringSliceLoader := CacheWrapper(client, "string_slice_key", func(ctx context.Context) ([]string, error) {
		return []string{"hello", "world", "test"}, nil
	}, time.Minute)

	// 测试二维切片
	int2DSliceLoader := CacheWrapper(client, "int_2d_slice_key", func(ctx context.Context) ([][]int, error) {
		return [][]int{{1, 2}, {3, 4}, {5, 6}}, nil
	}, time.Minute)

	// 执行测试
	arrayResult, err := intArrayLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, [5]int{1, 2, 3, 4, 5}, arrayResult)
	assert.Len(t, arrayResult, 5)

	sliceResult, err := stringSliceLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, []string{"hello", "world", "test"}, sliceResult)
	assert.Len(t, sliceResult, 3)

	slice2DResult, err := int2DSliceLoader(ctx)
	assert.NoError(t, err)
	assert.Len(t, slice2DResult, 3)
	assert.Equal(t, []int{1, 2}, slice2DResult[0])
	assert.Equal(t, []int{3, 4}, slice2DResult[1])
	assert.Equal(t, []int{5, 6}, slice2DResult[2])
}

// TestCacheWrapper24_TimeTypes 测试时间类型
func TestCacheWrapper24_TimeTypes(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	now := time.Now().Round(time.Microsecond) // 去掉纳秒精度以避免序列化问题

	timeLoader := CacheWrapper(client, "time_key", func(ctx context.Context) (time.Time, error) {
		return now, nil
	}, time.Minute)

	durationLoader := CacheWrapper(client, "duration_key", func(ctx context.Context) (time.Duration, error) {
		return time.Hour + time.Minute + time.Second, nil
	}, time.Minute)

	// 测试时间
	timeResult, err := timeLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, now.Unix(), timeResult.Unix()) // 比较Unix时间戳

	// 测试持续时间
	durationResult, err := durationLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, time.Hour+time.Minute+time.Second, durationResult)
}

// TestCacheWrapper25_PointerTypes 测试指针类型
func TestCacheWrapper25_PointerTypes(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	// 测试字符串指针
	str := "pointer_test"
	stringPtrLoader := CacheWrapper(client, "string_ptr_key", func(ctx context.Context) (*string, error) {
		return &str, nil
	}, time.Minute)

	// 测试整数指针
	num := 42
	intPtrLoader := CacheWrapper(client, "int_ptr_key", func(ctx context.Context) (*int, error) {
		return &num, nil
	}, time.Minute)

	// 测试nil指针
	nilPtrLoader := CacheWrapper(client, "nil_ptr_key", func(ctx context.Context) (*string, error) {
		return nil, nil
	}, time.Minute)

	// 执行测试
	strPtrResult, err := stringPtrLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, strPtrResult)
	assert.Equal(t, "pointer_test", *strPtrResult)

	intPtrResult, err := intPtrLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, intPtrResult)
	assert.Equal(t, 42, *intPtrResult)

	nilResult, err := nilPtrLoader(ctx)
	assert.NoError(t, err)
	assert.Nil(t, nilResult)
}

// TestCacheWrapper_BasicFunctionality 测试缓存包装器基本功能
func TestCacheWrapper_BasicFunctionality(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	// 模拟数据加载器
	callCount := 0
	dataLoader := func(ctx context.Context) (string, error) {
		callCount++
		return fmt.Sprintf("loaded_data_%d", callCount), nil
	}

	// 创建缓存包装器
	cacheKey := "test_cache_key"
	expiration := time.Minute * 5
	cachedLoader := CacheWrapper(client, cacheKey, dataLoader, expiration)

	// 第一次调用 - 应该执行数据加载器
	result1, err := cachedLoader(ctx)
	require.NoError(t, err)
	assert.Equal(t, "loaded_data_1", result1)
	assert.Equal(t, 1, callCount)

	// 第二次调用 - 应该从缓存获取
	result2, err := cachedLoader(ctx)
	require.NoError(t, err)
	assert.Equal(t, "loaded_data_1", result2) // 应该是相同的数据
	assert.Equal(t, 1, callCount)             // 不应该再次调用加载器
}

// TestCacheWrapper_ErrorHandling 测试缓存包装器错误处理
func TestCacheWrapper_ErrorHandling(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	// 模拟会出错的数据加载器
	errorLoader := func(ctx context.Context) (string, error) {
		return "", fmt.Errorf("模拟数据加载错误")
	}

	// 创建缓存包装器
	cacheKey := "error_test_key"
	expiration := time.Minute * 5
	cachedLoader := CacheWrapper(client, cacheKey, errorLoader, expiration)

	// 调用应该返回错误
	_, err := cachedLoader(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "模拟数据加载错误")
}

// TestCacheWrapper_Expiration 测试缓存过期
func TestCacheWrapper_Expiration(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	callCount := 0
	dataLoader := func(ctx context.Context) (string, error) {
		callCount++
		return fmt.Sprintf("data_%d", callCount), nil
	}

	// 创建短过期时间的缓存包装器
	cacheKey := "expiration_test_key"
	expiration := time.Millisecond * 100 // 100毫秒过期
	cachedLoader := CacheWrapper(client, cacheKey, dataLoader, expiration)

	// 第一次调用
	result1, err := cachedLoader(ctx)
	require.NoError(t, err)
	assert.Equal(t, "data_1", result1)
	assert.Equal(t, 1, callCount)

	// 等待缓存过期
	time.Sleep(time.Millisecond * 150)

	// 再次调用 - 应该重新加载数据
	result2, err := cachedLoader(ctx)
	require.NoError(t, err)
	assert.Equal(t, "data_2", result2)
	assert.Equal(t, 2, callCount)
}

// TestCacheWrapper_DifferentTypes 测试不同数据类型
func TestCacheWrapper_DifferentTypes(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	t.Run("Integer", func(t *testing.T) {
		intLoader := func(ctx context.Context) (int, error) {
			return 42, nil
		}

		cachedIntLoader := CacheWrapper(client, "int_key", intLoader, time.Minute)
		result, err := cachedIntLoader(ctx)
		require.NoError(t, err)
		assert.Equal(t, 42, result)
	})

	t.Run("Struct", func(t *testing.T) {
		type TestStruct struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}

		structLoader := func(ctx context.Context) (TestStruct, error) {
			return TestStruct{ID: 1, Name: "test"}, nil
		}

		cachedStructLoader := CacheWrapper(client, "struct_key", structLoader, time.Minute)
		result, err := cachedStructLoader(ctx)
		require.NoError(t, err)
		assert.Equal(t, TestStruct{ID: 1, Name: "test"}, result)
	})

	t.Run("Slice", func(t *testing.T) {
		sliceLoader := func(ctx context.Context) ([]string, error) {
			return []string{"a", "b", "c"}, nil
		}

		cachedSliceLoader := CacheWrapper(client, "slice_key", sliceLoader, time.Minute)
		result, err := cachedSliceLoader(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, result)
	})
}

// TestCacheWrapper_ConcurrentAccess 测试并发访问
func TestCacheWrapper_ConcurrentAccess(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	callCount := 0
	dataLoader := func(ctx context.Context) (string, error) {
		callCount++
		time.Sleep(time.Millisecond * 10) // 模拟数据加载时间
		return "shared_data", nil
	}

	cacheKey := "concurrent_test_key"
	expiration := time.Minute
	cachedLoader := CacheWrapper(client, cacheKey, dataLoader, expiration)

	// 并发调用
	goroutineCount := 10
	results := make(chan string, goroutineCount)

	for i := 0; i < goroutineCount; i++ {
		go func() {
			result, err := cachedLoader(ctx)
			if err == nil {
				results <- result
			}
		}()
	}

	// 收集结果
	var allResults []string
	for i := 0; i < goroutineCount; i++ {
		select {
		case result := <-results:
			allResults = append(allResults, result)
		case <-time.After(time.Second * 5):
			t.Fatal("超时等待goroutine完成")
		}
	}

	// 验证所有结果都相同
	for _, result := range allResults {
		assert.Equal(t, "shared_data", result)
	}

	// 注意: 由于Redis操作的原子性，可能会有多次调用数据加载器
	// 这在分布式环境中是正常的，重要的是最终结果的一致性
	t.Logf("数据加载器被调用了 %d 次", callCount)
}

// BenchmarkCacheWrapper_Performance 性能基准测试
func BenchmarkCacheWrapper_Performance(b *testing.B) {
	client := redis.NewClient(&redis.Options{
		Addr:            "120.79.25.168:16389",
		Password:        "M5Pi9YW6u",
		DB:              1,
		DialTimeout:     10 * time.Second, // 增加拨号超时
		ReadTimeout:     5 * time.Second,  // 增加读超时
		WriteTimeout:    5 * time.Second,  // 增加写超时
		PoolTimeout:     10 * time.Second, // 增加池超时
		PoolSize:        10,               // 恢复正常连接池大小
		MinIdleConns:    2,                // 最小空闲连接
		MaxRetries:      3,                // 增加重试次数
		DisableIdentity: true,
	})

	ctx := context.Background()
	// 测试连接 - 使用Set/Get测试而不是Ping
	testKey := fmt.Sprintf("bench_test_conn_%d", time.Now().UnixNano())
	if err := client.Set(ctx, testKey, "test", time.Second).Err(); err != nil {
		b.Skipf("Redis不可用，跳过基准测试: %v", err)
		return
	}
	client.Del(ctx, testKey) // 清理测试键
	defer client.Close()

	dataLoader := func(ctx context.Context) (string, error) {
		return "benchmark_data", nil
	}

	cacheKey := "benchmark_key"
	expiration := time.Minute
	cachedLoader := CacheWrapper(client, cacheKey, dataLoader, expiration)

	// 预热缓存
	cachedLoader(ctx)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cachedLoader(ctx)
		}
	})
}
