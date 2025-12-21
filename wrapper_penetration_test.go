/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-20 22:20:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-12-20 22:25:00
 * @FilePath: \go-cachex\wrapper_penetration_test.go
 * @Description: 缓存穿透保护测试
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestCachePenetrationStringDefaultValue 测试字符串类型的缓存穿透保护
func TestCachePenetrationStringDefaultValue(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test_penetration_string"
	callCount := int32(0)

	// 模拟查询不存在的数据
	dataLoader := func(ctx context.Context) (*string, error) {
		atomic.AddInt32(&callCount, 1)
		return nil, nil // 返回 nil 表示数据不存在
	}

	defaultValue := "default_value"
	cachedLoader := CacheWrapper(
		client, key, dataLoader, time.Minute,
		WithCachePenetration(&defaultValue),
	)

	// 第一次调用 - 会查询数据库，返回默认值并缓存
	result1, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result1)
	assert.Equal(t, "default_value", *result1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 第二次调用 - 应该从缓存获取默认值，不再查询数据库
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result2)
	assert.Equal(t, "default_value", *result2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount)) // 调用次数不变

	// 第三次调用 - 再次验证缓存有效
	result3, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result3)
	assert.Equal(t, "default_value", *result3)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount)) // 调用次数仍不变
}

// TestCachePenetrationStructDefaultValue 测试结构体类型的缓存穿透保护
func TestCachePenetrationStructDefaultValue(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	type User struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	ctx := context.Background()
	key := "test_penetration_struct"
	callCount := int32(0)

	// 模拟查询不存在的用户
	dataLoader := func(ctx context.Context) (*User, error) {
		atomic.AddInt32(&callCount, 1)
		return nil, nil // 用户不存在
	}

	defaultUser := &User{ID: -1, Name: "unknown"}
	cachedLoader := CacheWrapper(
		client, key, dataLoader, time.Minute,
		WithCachePenetration(defaultUser),
	)

	// 第一次调用
	result1, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result1)
	assert.Equal(t, -1, result1.ID)
	assert.Equal(t, "unknown", result1.Name)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 第二次调用 - 从缓存获取
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result2)
	assert.Equal(t, -1, result2.ID)
	assert.Equal(t, "unknown", result2.Name)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
}

// TestCachePenetrationCustomTTL 测试自定义默认值TTL（使用WithTTL组合）
func TestCachePenetrationCustomTTL(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test_penetration_custom_ttl"
	callCount := int32(0)

	dataLoader := func(ctx context.Context) (*string, error) {
		atomic.AddInt32(&callCount, 1)
		return nil, nil
	}

	defaultValue := "default"
	shortTTL := 2 * time.Second

	cachedLoader := CacheWrapper(
		client, key, dataLoader, shortTTL,
		WithCachePenetration(&defaultValue),
	)

	// 第一次调用
	result1, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result1)
	assert.Equal(t, "default", *result1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 等待 TTL 过期
	time.Sleep(3 * time.Second)

	// TTL 过期后再次调用，应该重新查询数据库
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result2)
	assert.Equal(t, "default", *result2)
	assert.Equal(t, int32(2), atomic.LoadInt32(&callCount)) // 调用次数增加
}

// TestCachePenetrationNormalDataNotAffected 测试正常数据不受穿透保护影响
func TestCachePenetrationNormalDataNotAffected(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test_penetration_normal_data"
	callCount := int32(0)

	// 返回正常数据
	dataLoader := func(ctx context.Context) (*string, error) {
		atomic.AddInt32(&callCount, 1)
		value := "real_data"
		return &value, nil
	}

	defaultValue := "default_value"
	cachedLoader := CacheWrapper(
		client, key, dataLoader, time.Minute,
		WithCachePenetration(&defaultValue),
	)

	// 第一次调用 - 返回真实数据
	result1, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result1)
	assert.Equal(t, "real_data", *result1) // 返回真实数据，不是默认值
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 第二次调用 - 从缓存获取真实数据
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result2)
	assert.Equal(t, "real_data", *result2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
}

// TestCachePenetrationConcurrentAccess 测试并发访问时的缓存穿透保护（无分布式锁）
// 注意：当前实现在首次并发访问时，多个请求可能同时查询DB
func TestCachePenetrationConcurrentAccess(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test_penetration_concurrent"
	callCount := int32(0)

	dataLoader := func(ctx context.Context) (*string, error) {
		count := atomic.AddInt32(&callCount, 1)
		time.Sleep(50 * time.Millisecond) // 模拟慢查询
		t.Logf("DB call #%d", count)
		return nil, nil
	}

	defaultValue := "default"
	cachedLoader := CacheWrapper(
		client, key, dataLoader, time.Minute,
		WithCachePenetration(&defaultValue),
	)

	// 第一阶段：测试首次并发访问（缓存未命中）
	t.Log("Phase 1: First concurrent access (cache miss)")
	concurrency := 10
	done := make(chan bool, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			result, err := cachedLoader(ctx)
			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, "default", *result)
			done <- true
		}()
	}

	// 等待所有协程完成
	for i := 0; i < concurrency; i++ {
		<-done
	}

	firstPhaseCount := atomic.LoadInt32(&callCount)
	t.Logf("Phase 1 DB calls: %d (concurrency: %d)", firstPhaseCount, concurrency)

	// 第一阶段可能有多次DB查询（没有singleflight机制）
	// 但验证确实有查询发生
	assert.Greater(t, firstPhaseCount, int32(0), "应该至少有一次DB查询")

	// 等待缓存稳定（延迟双删完成）
	time.Sleep(200 * time.Millisecond)

	// 第二阶段：测试缓存命中后的并发访问
	t.Log("Phase 2: Concurrent access with cache hit")

	for i := 0; i < concurrency; i++ {
		go func() {
			result, err := cachedLoader(ctx)
			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, "default", *result)
			done <- true
		}()
	}

	// 等待所有协程完成
	for i := 0; i < concurrency; i++ {
		<-done
	}

	finalCount := atomic.LoadInt32(&callCount)
	t.Logf("Phase 2 DB calls: %d (should not increase)", finalCount)

	// 第二阶段不应该有新的DB查询（都从缓存获取）
	assert.Equal(t, firstPhaseCount, finalCount, "缓存命中后不应该再有DB查询")
}

// TestCachePenetrationWithoutProtection 测试未启用穿透保护的情况
func TestCachePenetrationWithoutProtection(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test_no_penetration"
	callCount := int32(0)

	dataLoader := func(ctx context.Context) (*string, error) {
		atomic.AddInt32(&callCount, 1)
		return nil, nil // 返回 nil
	}

	// 不启用穿透保护
	cachedLoader := CacheWrapper(client, key, dataLoader, time.Minute)

	// 第一次调用
	result1, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Nil(t, result1) // 返回 nil
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 第二次调用 - nil 会被正常缓存（因为延迟双删策略）
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Nil(t, result2)
	// 由于延迟双删会缓存nil结果，第二次调用不会再查询DB
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount)) // 调用次数不变
}

// TestCachePenetrationIntSliceDefaultValue 测试切片类型的缓存穿透保护
func TestCachePenetrationIntSliceDefaultValue(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test_penetration_slice"
	callCount := int32(0)

	dataLoader := func(ctx context.Context) ([]int, error) {
		atomic.AddInt32(&callCount, 1)
		return nil, nil // 返回 nil 切片
	}

	defaultSlice := []int{-1}
	cachedLoader := CacheWrapper(
		client, key, dataLoader, time.Minute,
		WithCachePenetration(defaultSlice),
	)

	// 第一次调用
	result1, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result1)
	assert.Equal(t, []int{-1}, result1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 第二次调用 - 从缓存获取
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result2)
	assert.Equal(t, []int{-1}, result2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
}

// TestCachePenetrationMapDefaultValue 测试 map 类型的缓存穿透保护
func TestCachePenetrationMapDefaultValue(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test_penetration_map"
	callCount := int32(0)

	dataLoader := func(ctx context.Context) (map[string]int, error) {
		atomic.AddInt32(&callCount, 1)
		return nil, nil
	}

	defaultMap := map[string]int{"default": -1}
	cachedLoader := CacheWrapper(
		client, key, dataLoader, time.Minute,
		WithCachePenetration(defaultMap),
	)

	// 第一次调用
	result1, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result1)
	assert.Equal(t, map[string]int{"default": -1}, result1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 第二次调用
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result2)
	assert.Equal(t, map[string]int{"default": -1}, result2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
}

// TestCachePenetrationWithDistributedLock 测试带分布式锁的缓存穿透保护
// 验证在高并发场景下，分布式锁能有效防止缓存击穿
func TestCachePenetrationWithDistributedLock(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test_penetration_with_lock"
	callCount := int32(0)

	dataLoader := func(ctx context.Context) (*string, error) {
		count := atomic.AddInt32(&callCount, 1)
		time.Sleep(100 * time.Millisecond) // 模拟慢查询
		t.Logf("DB call #%d", count)
		return nil, nil
	}

	defaultValue := "default"
	cachedLoader := CacheWrapper(
		client, key, dataLoader, time.Minute,
		WithCachePenetration(&defaultValue),
		WithDistributedLock(nil), // 启用分布式锁（使用默认配置）
	)

	// 第一阶段：测试首次并发访问（有分布式锁保护）
	t.Log("Phase 1: First concurrent access with distributed lock")
	concurrency := 20
	done := make(chan bool, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(id int) {
			result, err := cachedLoader(ctx)
			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, "default", *result)
			t.Logf("Goroutine #%d completed", id)
			done <- true
		}(i)
	}

	// 等待所有协程完成
	for i := 0; i < concurrency; i++ {
		<-done
	}

	firstPhaseCount := atomic.LoadInt32(&callCount)
	t.Logf("Phase 1 DB calls: %d (concurrency: %d)", firstPhaseCount, concurrency)

	// 验证分布式锁生效：DB查询次数应该很少（理想情况下只有1次）
	// 考虑到锁竞争，允许少量额外查询（2-3次）
	assert.LessOrEqual(t, firstPhaseCount, int32(3), "分布式锁应该限制DB查询次数")

	// 等待缓存稳定（延迟双删完成）
	time.Sleep(200 * time.Millisecond)

	// 第二阶段：测试缓存命中后的并发访问
	t.Log("Phase 2: Concurrent access with cache hit")

	for i := 0; i < concurrency; i++ {
		go func() {
			result, err := cachedLoader(ctx)
			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, "default", *result)
			done <- true
		}()
	}

	for i := 0; i < concurrency; i++ {
		<-done
	}

	secondPhaseCount := atomic.LoadInt32(&callCount)
	t.Logf("Phase 2 DB calls: %d (total: %d)", secondPhaseCount-firstPhaseCount, secondPhaseCount)

	// 第二阶段不应该有新的DB查询（全部从缓存读取）
	assert.Equal(t, firstPhaseCount, secondPhaseCount, "缓存命中时不应该查询DB")
}

// TestDistributedLockWithRealData 测试分布式锁在真实数据场景下的效果
// 验证在不使用缓存穿透保护的情况下，分布式锁能否防止并发查询DB
func TestDistributedLockWithRealData(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test_lock_real_data"
	callCount := int32(0)

	// 返回真实数据的 loader（不返回 nil）
	dataLoader := func(ctx context.Context) (*string, error) {
		count := atomic.AddInt32(&callCount, 1)
		time.Sleep(100 * time.Millisecond) // 模拟慢查询
		t.Logf("DB call #%d", count)
		result := fmt.Sprintf("data_%d", count)
		return &result, nil
	}

	// 启用分布式锁，不启用缓存穿透保护
	cachedLoader := CacheWrapper(
		client, key, dataLoader, time.Minute,
		WithDistributedLock(nil),
	)

	t.Log("Testing concurrent access with distributed lock (real data)")
	concurrency := 20
	done := make(chan bool, concurrency)
	results := make([]string, concurrency)

	// 并发请求
	for i := 0; i < concurrency; i++ {
		go func(index int) {
			result, err := cachedLoader(ctx)
			assert.NoError(t, err)
			assert.NotNil(t, result)
			results[index] = *result
			t.Logf("Goroutine #%d got: %s", index, *result)
			done <- true
		}(i)
	}

	// 等待所有协程完成
	for i := 0; i < concurrency; i++ {
		<-done
	}

	dbCalls := atomic.LoadInt32(&callCount)
	t.Logf("Total DB calls: %d (concurrency: %d)", dbCalls, concurrency)

	// 验证分布式锁生效：DB查询次数应该很少（理想情况1次，允许2-5次由于锁竞争）
	assert.LessOrEqual(t, dbCalls, int32(5), "分布式锁应该限制DB查询次数")

	// 验证大部分 goroutine 得到的数据一致
	// 由于并发和锁的特性，允许少量不一致（但应该是data_1或data_2）
	firstResult := results[0]
	consistentCount := 0
	for _, result := range results {
		if result == firstResult {
			consistentCount++
		}
	}
	// 至少70%的结果应该一致
	assert.GreaterOrEqual(t, consistentCount, concurrency*7/10, "大部分goroutine应该得到一致的结果")
}
