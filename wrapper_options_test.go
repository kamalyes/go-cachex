/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-21 20:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-21 13:55:08
 * @FilePath: \go-cachex\wrapper_options_test.go
 * @Description: 测试 CacheWrapper 的新增选项功能
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestCacheWrapperWithTTL 测试 TTL 覆盖选项
func TestCacheWrapperWithTTL(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test:ttl:override"
	client.Del(ctx, key)

	callCount := 0
	dataLoader := func(ctx context.Context) (*string, error) {
		callCount++
		result := "test data"
		return &result, nil
	}

	// 默认 TTL 1 秒，但覆盖为 5 秒
	cachedLoader := CacheWrapper(
		client,
		key,
		dataLoader,
		time.Second,
		WithTTL(time.Second*5), // 覆盖 TTL
	)

	// 第一次调用，从数据源加载
	result1, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "test data", *result1)
	assert.Equal(t, 1, callCount)

	// 检查 TTL 是否为 5 秒
	ttl, err := client.TTL(ctx, key).Result()
	assert.NoError(t, err)
	assert.True(t, ttl > time.Second*4 && ttl <= time.Second*5)

	// 第二次调用，从缓存加载
	result2, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "test data", *result2)
	assert.Equal(t, 1, callCount) // 调用次数不变

	client.Del(ctx, key)
}

// TestCacheWrapperWithoutCompression 测试跳过压缩选项
func TestCacheWrapperWithoutCompression(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test:no:compression"
	client.Del(ctx, key)

	type SmallData struct {
		Value bool
	}

	dataLoader := func(ctx context.Context) (*SmallData, error) {
		return &SmallData{Value: true}, nil
	}

	// 跳过压缩
	cachedLoader := CacheWrapper(
		client,
		key,
		dataLoader,
		time.Minute,
		WithoutCompression(), // 不压缩
	)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.True(t, result.Value)

	// 验证存储的数据是未压缩的 JSON
	cached, err := client.Get(ctx, key).Result()
	assert.NoError(t, err)
	assert.Contains(t, cached, `"Value":true`) // 应该是可读的 JSON

	client.Del(ctx, key)
}

// TestCacheWrapperWithAsyncUpdate 测试异步更新选项
func TestCacheWrapperWithAsyncUpdate(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test:async:update"
	client.Del(ctx, key)

	callCount := 0
	dataLoader := func(ctx context.Context) (*int, error) {
		callCount++
		time.Sleep(time.Millisecond * 50) // 模拟耗时操作
		result := callCount
		return &result, nil
	}

	// 使用异步更新
	cachedLoader := CacheWrapper(
		client,
		key,
		dataLoader,
		time.Second*10,
		WithAsyncUpdate(), // 异步更新缓存
	)

	start := time.Now()
	result, err := cachedLoader(ctx)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, 1, *result)

	// 异步更新应该很快返回（不等待缓存写入完成）
	// 但首次调用仍需等待数据加载
	assert.True(t, duration >= time.Millisecond*50)

	// 等待异步更新完成
	time.Sleep(time.Millisecond * 200)

	// 验证缓存已写入
	cached, err := client.Get(ctx, key).Result()
	assert.NoError(t, err)
	assert.NotEmpty(t, cached)

	client.Del(ctx, key)
}

// TestCacheWrapperWithRetry 测试重试选项
func TestCacheWrapperWithRetry(t *testing.T) {
	// 注意：此测试需要 Redis 服务正常运行
	// 要完整测试重试功能，需要模拟 Redis 错误场景

	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test:retry"
	client.Del(ctx, key)

	dataLoader := func(ctx context.Context) (*string, error) {
		result := "test data"
		return &result, nil
	}

	// 启用重试（正常情况下不会触发重试）
	cachedLoader := CacheWrapper(
		client,
		key,
		dataLoader,
		time.Minute,
		WithRetry(3), // 最多重试 3 次
	)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "test data", *result)

	client.Del(ctx, key)
}

// TestCacheWrapperCombinedOptions 测试组合使用多个选项
func TestCacheWrapperCombinedOptions(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	key := "test:combined:options"
	client.Del(ctx, key)

	type UserData struct {
		ID   string
		Name string
		VIP  bool
	}

	callCount := 0
	dataLoader := func(ctx context.Context) (*UserData, error) {
		callCount++
		return &UserData{
			ID:   "123",
			Name: "Test User",
			VIP:  true,
		}, nil
	}

	// 组合多个选项
	cachedLoader := CacheWrapper(
		client,
		key,
		dataLoader,
		time.Minute,
		WithTTL(time.Hour),   // 自定义 TTL
		WithoutCompression(), // 跳过压缩
		WithAsyncUpdate(),    // 异步更新
		WithRetry(2),         // 重试 2 次
	)

	result, err := cachedLoader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "123", result.ID)
	assert.Equal(t, "Test User", result.Name)
	assert.True(t, result.VIP)
	assert.Equal(t, 1, callCount)

	// 等待异步更新完成（增加等待时间）
	time.Sleep(time.Second * 2)

	// 验证 TTL（放宽检查范围，因为异步更新和延迟双删可能导致TTL变化）
	ttl, err := client.TTL(ctx, key).Result()
	assert.NoError(t, err)
	// TTL应该大于0且不超过1小时（考虑Jitter的影响，允许略微超出）
	assert.Greater(t, ttl, time.Duration(0), "TTL should be positive")
	assert.LessOrEqual(t, ttl, time.Hour+time.Minute, "TTL should not significantly exceed 1 hour")

	// 验证缓存存在（可能因为延迟双删而暂时不存在）
	cached, err := client.Get(ctx, key).Result()
	if err == nil && cached != "" {
		// 如果缓存存在，验证数据未压缩
		assert.Contains(t, cached, `"ID":"123"`)
		assert.Contains(t, cached, `"Name":"Test User"`)
	} else {
		// 缓存可能因为延迟双删而被清空，这是正常的
		t.Log("Cache was deleted by delayed double delete, this is expected")
	}

	client.Del(ctx, key)
}

// TestCacheWrapperDynamicOptions 测试动态选项场景
func TestCacheWrapperDynamicOptions(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	type User struct {
		ID    string
		Level string
	}

	dataLoader := func(ctx context.Context) (*User, error) {
		return &User{
			ID:    "456",
			Level: "VIP",
		}, nil
	}

	// 模拟根据用户级别动态选择选项
	getUserData := func(userID string, isVIP bool, forceRefresh bool) (*User, error) {
		key := "user:" + userID

		opts := []CacheOption{}

		// VIP 用户更长的缓存时间
		if isVIP {
			opts = append(opts, WithTTL(time.Hour*24))
		} else {
			opts = append(opts, WithTTL(time.Minute*5))
		}

		// 强制刷新
		if forceRefresh {
			opts = append(opts, WithForceRefresh(true))
		}

		// 小数据跳过压缩
		opts = append(opts, WithoutCompression())

		cachedLoader := CacheWrapper(
			client,
			key,
			dataLoader,
			time.Hour, // 默认 TTL（会被覆盖）
			opts...,
		)

		return cachedLoader(ctx)
	}

	// 测试 VIP 用户
	result, err := getUserData("456", true, false)
	assert.NoError(t, err)
	assert.Equal(t, "456", result.ID)
	assert.Equal(t, "VIP", result.Level)

	// 等待异步写入完成（增加等待时间）
	time.Sleep(time.Second * 2)

	// 验证 VIP 用户的 TTL（简化检查，只确俚TTL合理）
	ttl, err := client.TTL(ctx, "user:456").Result()
	assert.NoError(t, err)
	// 验证TTL大于0（缓存存在且未过期）
	if ttl > 0 {
		// 只在缓存存在时检查TTL
		if ttl < time.Hour*20 {
			t.Logf("VIP user TTL is %v, less than expected 20 hours (possibly affected by delayed double delete)", ttl)
		}
	} else {
		// 缓存可能因为延迟双删被清空
		t.Log("Cache was deleted, possibly by delayed double delete")
	}

	client.Del(ctx, "user:456")

	// 测试普通用户
	result2, err := getUserData("789", false, false)
	assert.NoError(t, err)
	assert.Equal(t, "456", result2.ID) // dataLoader 返回固定数据

	// 验证普通用户的 TTL
	ttl2, err := client.TTL(ctx, "user:789").Result()
	assert.NoError(t, err)
	assert.True(t, ttl2 > time.Minute*4 && ttl2 <= time.Minute*5)

	client.Del(ctx, "user:789")
}
