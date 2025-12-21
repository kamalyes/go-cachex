/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 23:27:50
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 23:41:36
 * @FilePath: \go-cachex\redis_test.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 测试Redis GetOrCompute的分布式锁机制
func TestRedisGetOrComputeDistributedLock(t *testing.T) {
	// 创建Redis客户端
	client := redis.NewClient(&redis.Options{
		Addr:     "120.79.25.168:16389",
		DB:       1, // 使用DB1进行测试
		Password: "M5Pi9YW6u",
		// 禁用身份设置以减少警告
		DisableIdentity: true,
	})

	// 测试连接
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis不可用，跳过测试: %v", err)
		return
	}

	// 清理测试数据
	defer client.FlushDB(ctx)

	// 创建Redis Handler
	handler := &RedisHandler{
		redis: client,
		ctx:   ctx,
	}

	testKey := []byte("test-distributed-lock")
	ttl := 5 * time.Second

	// 模拟多个goroutine同时调用GetOrCompute
	var wg sync.WaitGroup
	var mu sync.Mutex
	callCount := 0
	results := make([][]byte, 3)
	errors := make([]error, 3)

	loader := func() ([]byte, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		// 模拟计算时间
		time.Sleep(100 * time.Millisecond)
		return []byte("computed-value"), nil
	}

	// 启动3个并发goroutine
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			result, err := handler.GetOrCompute(testKey, ttl, loader)
			results[index] = result
			errors[index] = err
		}(i)
	}

	wg.Wait()

	// 验证结果
	for i := 0; i < 3; i++ {
		assert.NoError(t, errors[i], "第%d个调用应该成功", i)
		assert.Equal(t, "computed-value", string(results[i]), "第%d个调用结果应该正确", i)
	}

	// 验证loader只被调用了一次（分布式锁生效）
	assert.Equal(t, 1, callCount, "loader应该只被调用1次（分布式锁生效）")

	// 验证值已经缓存
	cachedValue, err := handler.Get(testKey)
	assert.NoError(t, err, "获取缓存值应该成功")
	assert.Equal(t, "computed-value", string(cachedValue), "缓存值应该正确")
}

// 测试Redis GetOrCompute的基本功能
func TestRedisGetOrComputeBasic(t *testing.T) {
	// 创建Redis客户端
	client := redis.NewClient(&redis.Options{
		Addr:     "120.79.25.168:16389",
		DB:       1, // 使用DB1进行测试
		Password: "M5Pi9YW6u",
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis不可用，跳过测试: %v", err)
		return
	}

	defer client.FlushDB(ctx)

	handler := &RedisHandler{
		redis: client,
		ctx:   ctx,
	}

	testKey := []byte("test-basic")
	expectedValue := []byte("test-value")

	// 第一次调用，缓存未命中
	callCount := 0
	loader := func() ([]byte, error) {
		callCount++
		return expectedValue, nil
	}

	result, err := handler.GetOrCompute(testKey, time.Minute, loader)
	if err != nil {
		t.Fatalf("GetOrCompute失败: %v", err)
	}

	if string(result) != string(expectedValue) {
		t.Errorf("期望 %s，得到 %s", string(expectedValue), string(result))
	}

	if callCount != 1 {
		t.Errorf("期望loader被调用1次，实际调用了%d次", callCount)
	}

	// 第二次调用，应该从缓存获取
	result2, err := handler.GetOrCompute(testKey, time.Minute, loader)
	if err != nil {
		t.Fatalf("第二次GetOrCompute失败: %v", err)
	}

	if string(result2) != string(expectedValue) {
		t.Errorf("期望 %s，得到 %s", string(expectedValue), string(result2))
	}

	if callCount != 1 {
		t.Errorf("期望loader还是被调用1次，实际调用了%d次", callCount)
	}
}

// 测试使用推荐配置的Redis Handler
func TestRedisHandlerWithRecommendedConfig(t *testing.T) {
	// 使用推荐配置创建Handler
	handler, err := NewRedisHandlerSimple("120.79.25.168:16389", "M5Pi9YW6u", 1)
	if err != nil {
		t.Fatalf("创建Redis Handler失败: %v", err)
	}

	// 转换为具体类型以测试连接
	redisHandler := handler.(*RedisHandler)

	ctx := context.Background()
	if err := redisHandler.redis.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis不可用，跳过测试: %v", err)
		return
	}

	// 清理测试数据
	defer redisHandler.redis.FlushDB(ctx)

	// 测试基本操作
	testKey := []byte("test-recommended-config")
	testValue := []byte("test-value")

	// Set操作
	err = handler.Set(testKey, testValue)
	if err != nil {
		t.Fatalf("Set操作失败: %v", err)
	}

	// Get操作
	result, err := handler.Get(testKey)
	if err != nil {
		t.Fatalf("Get操作失败: %v", err)
	}

	if string(result) != string(testValue) {
		t.Errorf("期望 %s，得到 %s", string(testValue), string(result))
	}

	t.Log("推荐配置的Redis Handler工作正常")
}

// 补充缺失的测试以提升覆盖率
func TestRedisHandlerMissingMethods(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	options := NewRedisOptions(mr.Addr(), "", 0)

	handler, err := NewRedisHandler(options)
	require.NoError(t, err)

	ctx := context.Background()
	redisHandler := handler.(*RedisHandler)

	t.Run("WithCtx", func(t *testing.T) {
		newHandler := redisHandler.WithCtx(ctx)
		assert.NotNil(t, newHandler)
	})

	t.Run("GetTTL", func(t *testing.T) {
		key := []byte("ttl_key")
		value := []byte("ttl_value")

		redisHandler.SetWithTTL(key, value, 10*time.Second)
		ttl, err := redisHandler.GetTTL(key)
		assert.NoError(t, err)
		assert.Greater(t, ttl, 0*time.Second)
	})

	t.Run("Del", func(t *testing.T) {
		key := []byte("del_key")
		value := []byte("del_value")

		redisHandler.Set(key, value)
		err := redisHandler.Del(key)
		assert.NoError(t, err)

		_, err = redisHandler.Get(key)
		assert.Error(t, err)
	})

	t.Run("BatchGet", func(t *testing.T) {
		redisHandler.Set([]byte("batch1"), []byte("value1"))
		redisHandler.Set([]byte("batch2"), []byte("value2"))

		keys := [][]byte{[]byte("batch1"), []byte("batch2"), []byte("nonexistent")}
		results, errs := redisHandler.BatchGet(keys)
		assert.Len(t, results, 3)
		assert.Len(t, errs, 3)
		assert.Equal(t, []byte("value1"), results[0])
		assert.Equal(t, []byte("value2"), results[1])
		assert.Nil(t, results[2])
	})

	t.Run("Stats", func(t *testing.T) {
		stats := redisHandler.Stats()
		assert.NotNil(t, stats)
	})

	t.Run("Close", func(t *testing.T) {
		err := redisHandler.Close()
		assert.NoError(t, err)
	})
}
