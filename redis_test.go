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
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failFirstNGetsHook 使前 N 条 GET 命令返回 redis.Nil，模拟缓存未命中
type failFirstNGetsHook struct {
	counter   int32
	failCount int32
}

func (h *failFirstNGetsHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *failFirstNGetsHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "get" {
			if atomic.AddInt32(&h.counter, 1) <= h.failCount {
				return redis.Nil
			}
		}
		return next(ctx, cmd)
	}
}
func (h *failFirstNGetsHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// mgetNonStringHook 使 MGET 返回非字符串类型值，触发类型断言失败
type mgetNonStringHook struct{}

func (h *mgetNonStringHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *mgetNonStringHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if sc, ok := cmd.(*redis.SliceCmd); ok && cmd.Name() == "mget" {
			sc.SetVal([]interface{}{int64(42)})
			return nil
		}
		return next(ctx, cmd)
	}
}
func (h *mgetNonStringHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// ttlNilHook 使 TTL 命令返回 redis.Nil 错误，触发 GetTTLWithCtx 的 redis.Nil 分支
type ttlNilHook struct{}

func (h *ttlNilHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *ttlNilHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "ttl" {
			return redis.Nil
		}
		return next(ctx, cmd)
	}
}
func (h *ttlNilHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// failFirstSetNXAndExistsZeroHook 使首次 SetNX 返回 false，EXISTS 返回 0
// 用于模拟锁竞争场景：首次获取锁失败，但重试时锁已释放
type failFirstSetNXAndExistsZeroHook struct {
	setnxCounter int32
}

func (h *failFirstSetNXAndExistsZeroHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *failFirstSetNXAndExistsZeroHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		// 使 EXISTS 返回 0（锁不存在）
		if cmd.Name() == "exists" {
			if intCmd, ok := cmd.(*redis.IntCmd); ok {
				intCmd.SetVal(0)
				return nil
			}
		}
		// 使首次 SetNX（SET NX，返回 BoolCmd）返回 false
		if boolCmd, ok := cmd.(*redis.BoolCmd); ok && cmd.Name() == "set" {
			if atomic.AddInt32(&h.setnxCounter, 1) == 1 {
				boolCmd.SetVal(false)
				return nil
			}
		}
		return next(ctx, cmd)
	}
}
func (h *failFirstSetNXAndExistsZeroHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// infoSuccessHook 使 INFO 命令返回有效结果，覆盖 Stats 中 redis_info 成功分支
type infoSuccessHook struct{}

func (h *infoSuccessHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *infoSuccessHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "info" {
			if stringCmd, ok := cmd.(*redis.StringCmd); ok {
				stringCmd.SetVal("redis_version:7.0.0\nredis_mode:standalone\n")
				return nil
			}
		}
		return next(ctx, cmd)
	}
}
func (h *infoSuccessHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// 测试Redis GetOrCompute的分布式锁机制
func TestRedisGetOrComputeDistributedLock(t *testing.T) {
	// 使用 miniredis 本地内存 Redis，无需外部服务
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

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
	// 使用 miniredis 本地内存 Redis，无需外部服务
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

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
	// 使用 miniredis 本地内存 Redis，无需外部服务
	mr := miniredis.RunT(t)
	handler, err := NewRedisHandlerSimple(mr.Addr(), "", 0)
	require.NoError(t, err)

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

// TestRedisHandler_SimplifiedMethodsWithNilCtx 测试简化版方法的 nil ctx 分支
func TestRedisHandler_SimplifiedMethodsWithNilCtx(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DisableIdentity: true})
	defer client.Close()

	// 创建 ctx 为 nil 的 handler，触发简化方法中的 nil ctx 分支
	handler := &RedisHandler{redis: client, ctx: nil}

	// Set
	err := handler.Set([]byte("nil-ctx-key"), []byte("nil-ctx-value"))
	assert.NoError(t, err)

	// Get
	val, err := handler.Get([]byte("nil-ctx-key"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("nil-ctx-value"), val)

	// SetWithTTL
	err = handler.SetWithTTL([]byte("nil-ctx-ttl"), []byte("v"), time.Second)
	assert.NoError(t, err)

	// GetTTL
	ttl, err := handler.GetTTL([]byte("nil-ctx-ttl"))
	assert.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))

	// Del
	err = handler.Del([]byte("nil-ctx-key"))
	assert.NoError(t, err)

	// BatchGet
	_, _ = handler.BatchGet([][]byte{[]byte("nil-ctx-ttl")})

	// GetOrCompute (nil ctx 分支)
	val, err = handler.GetOrCompute([]byte("nil-ctx-compute"), time.Second, func() ([]byte, error) {
		return []byte("computed"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("computed"), val)
}

// TestRedisHandler_ValidateErrors 测试验证错误分支（nil redis 和 nil key）
func TestRedisHandler_ValidateErrors(t *testing.T) {
	// nil redis client
	nilRedisHandler := &RedisHandler{redis: nil, ctx: context.Background()}

	_, err := nilRedisHandler.GetWithCtx(context.Background(), []byte("k"))
	assert.ErrorIs(t, err, ErrNotInitialized)

	_, err = nilRedisHandler.GetTTLWithCtx(context.Background(), []byte("k"))
	assert.ErrorIs(t, err, ErrNotInitialized)

	err = nilRedisHandler.SetWithTTLAndCtx(context.Background(), []byte("k"), []byte("v"), time.Second)
	assert.ErrorIs(t, err, ErrNotInitialized)

	err = nilRedisHandler.DelWithCtx(context.Background(), []byte("k"))
	assert.ErrorIs(t, err, ErrNotInitialized)

	// nil key
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DisableIdentity: true})
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	_, err = handler.GetWithCtx(context.Background(), nil)
	assert.ErrorIs(t, err, ErrInvalidKey)

	_, err = handler.GetTTLWithCtx(context.Background(), nil)
	assert.ErrorIs(t, err, ErrInvalidKey)

	err = handler.SetWithTTLAndCtx(context.Background(), nil, []byte("v"), time.Second)
	assert.ErrorIs(t, err, ErrInvalidKey)

	err = handler.DelWithCtx(context.Background(), nil)
	assert.ErrorIs(t, err, ErrInvalidKey)
}

// TestRedisHandler_GetWithCtxErrors 测试 GetWithCtx 的错误分支
func TestRedisHandler_GetWithCtxErrors(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DisableIdentity: true})
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	// DeadlineExceeded → ErrTimeout
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := handler.GetWithCtx(ctx, []byte("key"))
	assert.ErrorIs(t, err, ErrTimeout)

	// 其他错误（连接关闭）→ ErrUnavailable
	mr.Close()
	_, err = handler.GetWithCtx(context.Background(), []byte("key"))
	assert.ErrorIs(t, err, ErrUnavailable)
}

// TestRedisHandler_GetTTLWithCtxErrors 测试 GetTTLWithCtx 的错误分支
func TestRedisHandler_GetTTLWithCtxErrors(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DisableIdentity: true})
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	// 先设置一个有 TTL 的 key
	handler.SetWithTTL([]byte("ttl-key"), []byte("v"), time.Second)

	// DeadlineExceeded → ErrTimeout
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := handler.GetTTLWithCtx(ctx, []byte("ttl-key"))
	assert.ErrorIs(t, err, ErrTimeout)

	// redis.Nil → ErrNotFound（通过 hook 模拟 TTL 返回 redis.Nil）
	mr2 := miniredis.RunT(t)
	client2 := redis.NewClient(&redis.Options{Addr: mr2.Addr(), DisableIdentity: true})
	defer client2.Close()
	client2.AddHook(&ttlNilHook{})
	handler2 := &RedisHandler{redis: client2, ctx: context.Background()}
	_, err = handler2.GetTTLWithCtx(context.Background(), []byte("any-key"))
	assert.ErrorIs(t, err, ErrNotFound)

	// 其他错误（连接关闭）→ ErrUnavailable
	mr.Close()
	_, err = handler.GetTTLWithCtx(context.Background(), []byte("ttl-key"))
	assert.ErrorIs(t, err, ErrUnavailable)
}

// TestRedisHandler_SetWithTTLAndCtxErrors 测试 SetWithTTLAndCtx 的错误分支
func TestRedisHandler_SetWithTTLAndCtxErrors(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DisableIdentity: true})
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	// DeadlineExceeded → ErrTimeout
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := handler.SetWithTTLAndCtx(ctx, []byte("k"), []byte("v"), time.Second)
	assert.ErrorIs(t, err, ErrTimeout)

	// 其他错误（连接关闭）→ ErrUnavailable
	mr.Close()
	err = handler.SetWithTTLAndCtx(context.Background(), []byte("k"), []byte("v"), time.Second)
	assert.ErrorIs(t, err, ErrUnavailable)
}

// TestRedisHandler_DelWithCtxErrors 测试 DelWithCtx 的错误分支
func TestRedisHandler_DelWithCtxErrors(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DisableIdentity: true})
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	// 正常 Del 不存在的 key（不返回 redis.Nil，返回 0 affected）
	err := handler.DelWithCtx(context.Background(), []byte("nonexistent"))
	assert.NoError(t, err)

	// DeadlineExceeded → ErrTimeout
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err = handler.DelWithCtx(ctx, []byte("key"))
	assert.ErrorIs(t, err, ErrTimeout)

	// 其他错误（连接关闭）→ ErrUnavailable
	mr.Close()
	err = handler.DelWithCtx(context.Background(), []byte("key"))
	assert.ErrorIs(t, err, ErrUnavailable)
}

// TestRedisHandler_BatchGetWithCtx_TypeError 测试 BatchGetWithCtx 的类型断言失败分支
func TestRedisHandler_BatchGetWithCtx_TypeError(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DisableIdentity: true})
	defer client.Close()

	// 添加 hook 使 MGET 返回非字符串类型
	client.AddHook(&mgetNonStringHook{})

	handler := &RedisHandler{redis: client, ctx: context.Background()}
	results, errs := handler.BatchGetWithCtx(context.Background(), [][]byte{[]byte("type-err-key")})
	assert.Len(t, results, 1)
	assert.Len(t, errs, 1)
	assert.ErrorIs(t, errs[0], ErrDataRead)
}

// TestRedisHandler_BatchGetWithCtx_RedisError 测试 BatchGetWithCtx 的 Redis 错误分支
func TestRedisHandler_BatchGetWithCtx_RedisError(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DisableIdentity: true})
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	// 关闭 redis 使 MGET 失败
	mr.Close()

	results, errs := handler.BatchGetWithCtx(context.Background(), [][]byte{[]byte("err-key")})
	assert.Len(t, results, 1)
	assert.Len(t, errs, 1)
	assert.Error(t, errs[0])
}

// TestRedisHandler_Stats_AllBranches 测试 Stats 的所有分支
func TestRedisHandler_Stats_AllBranches(t *testing.T) {
	t.Run("nil ctx", func(t *testing.T) {
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DisableIdentity: true})
		defer client.Close()
		handler := &RedisHandler{redis: client, ctx: nil}
		stats := handler.Stats()
		assert.NotNil(t, stats)
		assert.Equal(t, "redis", stats["cache_type"])
	})

	t.Run("memory_usage branch", func(t *testing.T) {
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DisableIdentity: true})
		defer client.Close()
		// 添加 hook 使 INFO 命令返回有效结果
		client.AddHook(&infoSuccessHook{})
		handler := &RedisHandler{redis: client, ctx: context.Background()}

		// 设置一个名为 "nonexistent" 的 key，使 MemoryUsage 返回非 nil
		handler.Set([]byte("nonexistent"), []byte("value"))

		stats := handler.Stats()
		assert.NotNil(t, stats)
		// redis_info 成功分支
		_, ok := stats["redis_info"]
		assert.True(t, ok, "应该包含 redis_info 字段")
		// memory_usage 分支被覆盖
		_, ok = stats["memory_usage"]
		assert.True(t, ok, "应该包含 memory_usage 字段")
	})

	t.Run("info and dbsize error", func(t *testing.T) {
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DisableIdentity: true})
		handler := &RedisHandler{redis: client, ctx: context.Background()}

		// 关闭 redis 使 Info 和 DBSize 失败
		mr.Close()

		stats := handler.Stats()
		assert.NotNil(t, stats)
		_, ok := stats["redis_info_error"]
		assert.True(t, ok, "应该包含 redis_info_error 字段")
		_, ok = stats["db_size_error"]
		assert.True(t, ok, "应该包含 db_size_error 字段")
	})
}

// TestRedisHandler_GetOrComputeWithCtx_SingleflightLoad 测试 singleflight Load 分支
func TestRedisHandler_GetOrComputeWithCtx_SingleflightLoad(t *testing.T) {
	t.Run("load success", func(t *testing.T) {
		client := setupRedisClient(t)
		defer client.Close()
		handler := &RedisHandler{redis: client, ctx: context.Background()}

		// 预填充 loadGroup，模拟另一个 goroutine 已完成加载
		call := &redisLoadCall{val: []byte("precomputed-value")}
		call.wg.Add(1)
		call.wg.Done()
		handler.loadGroup.Store("singleflight-success-key", call)

		// key 不在缓存中（GetWithCtx 返回 ErrNotFound），但 loadGroup 有记录
		result, err := handler.GetOrComputeWithCtx(context.Background(), []byte("singleflight-success-key"), time.Minute, func(ctx context.Context) ([]byte, error) {
			return nil, errors.New("loader should not be called")
		})
		assert.NoError(t, err)
		assert.Equal(t, []byte("precomputed-value"), result)
	})

	t.Run("load error", func(t *testing.T) {
		client := setupRedisClient(t)
		defer client.Close()
		handler := &RedisHandler{redis: client, ctx: context.Background()}

		// 预填充 loadGroup，模拟另一个 goroutine 加载失败
		testErr := errors.New("loader error")
		call := &redisLoadCall{err: testErr}
		call.wg.Add(1)
		call.wg.Done()
		handler.loadGroup.Store("singleflight-error-key", call)

		_, err := handler.GetOrComputeWithCtx(context.Background(), []byte("singleflight-error-key"), time.Minute, func(ctx context.Context) ([]byte, error) {
			return nil, errors.New("loader should not be called")
		})
		assert.ErrorIs(t, err, testErr)
	})
}

// TestRedisHandler_GetOrComputeWithCtx_DoubleCheck 测试双重检查缓存命中分支
func TestRedisHandler_GetOrComputeWithCtx_DoubleCheck(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("double-check-key")
	// 预设缓存值
	handler.Set(key, []byte("cached-value"))

	// 使用 hook 使第 1 次 GET 返回 redis.Nil（模拟缓存未命中），第 2 次 GET 返回真实值
	hook := &failFirstNGetsHook{failCount: 1}
	client.AddHook(hook)

	result, err := handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("loader should not be called")
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("cached-value"), result)
}

// TestRedisHandler_GetOrComputeWithCtx_TripleCheck 测试三重检查缓存命中分支
func TestRedisHandler_GetOrComputeWithCtx_TripleCheck(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("triple-check-key")
	// 预设缓存值
	handler.Set(key, []byte("cached-value"))

	// 使用 hook 使前 2 次 GET 返回 redis.Nil，第 3 次 GET 返回真实值
	hook := &failFirstNGetsHook{failCount: 2}
	client.AddHook(hook)

	result, err := handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("loader should not be called")
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("cached-value"), result)
}

// TestRedisHandler_GetOrComputeWithCtx_LockNotAcquired_CacheHit 测试未获锁时重试命中缓存
func TestRedisHandler_GetOrComputeWithCtx_LockNotAcquired_CacheHit(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("lock-not-acquired-cache-hit")
	lockKey := string(key) + ":lock"

	// 预设缓存值
	handler.Set(key, []byte("retry-cached-value"))

	// 预设锁，使 SetNX 失败
	client.Set(context.Background(), lockKey, "other-lock-value", 30*time.Second)

	// 使用 hook 使前 3 次 GET 返回 redis.Nil，第 4 次 GET 返回真实值
	// GET #1: 首次检查 → miss
	// GET #2: 双重检查 → miss
	// GET #3: 第一次重试 → miss
	// GET #4: 第二次重试 → hit
	hook := &failFirstNGetsHook{failCount: 3}
	client.AddHook(hook)

	result, err := handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("loader should not be called")
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("retry-cached-value"), result)
}

// TestRedisHandler_GetOrComputeWithCtx_LockNotAcquired_CtxCancelled 测试未获锁时上下文取消
func TestRedisHandler_GetOrComputeWithCtx_LockNotAcquired_CtxCancelled(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("lock-not-acquired-ctx-cancel")
	lockKey := string(key) + ":lock"

	// 预设锁，使 SetNX 失败
	client.Set(context.Background(), lockKey, "other-lock-value", 30*time.Second)

	// 创建可取消的上下文
	ctx, cancel := context.WithCancel(context.Background())
	// 在第一次重试等待期间取消上下文
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	_, err := handler.GetOrComputeWithCtx(ctx, key, time.Minute, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("loader should not be called")
	})
	assert.ErrorIs(t, err, context.Canceled)
}

// TestRedisHandler_GetOrComputeWithCtx_LockNotAcquired_LockReleased 测试未获锁时锁释放后获取锁并计算
func TestRedisHandler_GetOrComputeWithCtx_LockNotAcquired_LockReleased(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("lock-released-key")
	lockKey := string(key) + ":lock"

	// 预设锁，使 SetNX 失败
	client.Set(context.Background(), lockKey, "other-lock-value", 30*time.Second)

	// 在短延迟后删除锁，使 Exists 返回 0 并跳出重试循环
	go func() {
		time.Sleep(50 * time.Millisecond)
		client.Del(context.Background(), lockKey)
	}()

	result, err := handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, func(ctx context.Context) ([]byte, error) {
		return []byte("computed-after-lock-release"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("computed-after-lock-release"), result)
}

// TestRedisHandler_GetOrComputeWithCtx_LockNotAcquired_AfterRetryCacheHit 测试重试后 SetNX 失败但最终缓存命中
func TestRedisHandler_GetOrComputeWithCtx_LockNotAcquired_AfterRetryCacheHit(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("after-retry-cache-hit")
	lockKey := string(key) + ":lock"

	// 预设缓存值
	handler.Set(key, []byte("final-cache-value"))

	// 预设锁，使 SetNX 始终失败
	client.Set(context.Background(), lockKey, "other-lock-value", 30*time.Second)

	// 使用 hook 使前 12 次 GET 返回 redis.Nil，第 13 次 GET（最终检查）返回真实值
	// GET #1: 首次检查
	// GET #2: 双重检查
	// GET #3-#12: 10 次重试
	// GET #13: 最终检查（循环后）
	hook := &failFirstNGetsHook{failCount: 12}
	client.AddHook(hook)

	result, err := handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("loader should not be called")
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("final-cache-value"), result)
}

// TestRedisHandler_GetOrComputeWithCtx_LoaderError 测试 loader 返回错误
func TestRedisHandler_GetOrComputeWithCtx_LoaderError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	testErr := errors.New("loader failed")
	_, err := handler.GetOrComputeWithCtx(context.Background(), []byte("loader-error-key"), time.Minute, func(ctx context.Context) ([]byte, error) {
		return nil, testErr
	})
	assert.ErrorIs(t, err, testErr)
}

// TestRedisHandler_GetOrComputeWithCtx_TTLZero 测试 ttl <= 0 分支
func TestRedisHandler_GetOrComputeWithCtx_TTLZero(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	result, err := handler.GetOrComputeWithCtx(context.Background(), []byte("ttl-zero-key"), 0, func(ctx context.Context) ([]byte, error) {
		return []byte("computed-with-zero-ttl"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("computed-with-zero-ttl"), result)

	// 验证值已缓存（使用 SetWithCtx 而非 SetWithTTLAndCtx）
	cached, err := handler.Get([]byte("ttl-zero-key"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("computed-with-zero-ttl"), cached)
}

// TestRedisHandler_GetOrComputeWithCtx_LockNotAcquired_AfterRetryAcquireAndCompute 测试重试后获取锁并计算
func TestRedisHandler_GetOrComputeWithCtx_LockNotAcquired_AfterRetryAcquireAndCompute(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("after-retry-acquire")
	// 使用 hook 使首次 SetNX 返回 false，EXISTS 返回 0
	// 这样首次锁获取失败，重试时立即跳出循环，第二次 SetNX 成功
	client.AddHook(&failFirstSetNXAndExistsZeroHook{})

	result, err := handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, func(ctx context.Context) ([]byte, error) {
		return []byte("computed-after-retry-acquire"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("computed-after-retry-acquire"), result)
}

// TestRedisHandler_GetOrComputeWithCtx_AfterRetryTripleCheckCacheHit 测试重试后获取锁的三重检查缓存命中
func TestRedisHandler_GetOrComputeWithCtx_AfterRetryTripleCheckCacheHit(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("after-retry-triple-check")
	// 预设缓存值
	handler.Set(key, []byte("triple-check-cached-value"))

	// 使用 hook 使前 3 次 GET 返回 redis.Nil（首次检查 + 双重检查 + 重试第一次）
	// 同时使首次 SetNX 返回 false，EXISTS 返回 0
	// GET #1: 首次检查 → miss (hook)
	// GET #2: 双重检查 → miss (hook)
	// SetNX #1: fail (hook) → enter else
	// Retry i=0: GET #3: miss (hook), EXISTS: 0 (hook) → break
	// SetNX #2: pass through → succeed
	// GET #4: 三重检查 → hit (hook passes through, actual value)
	hook := &failFirstNGetsHook{failCount: 3}
	client.AddHook(hook)
	client.AddHook(&failFirstSetNXAndExistsZeroHook{})

	result, err := handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("loader should not be called")
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("triple-check-cached-value"), result)
}

// TestRedisHandler_GetOrComputeWithCtx_AfterRetryLoaderError 测试重试后获取锁的 loader 错误
func TestRedisHandler_GetOrComputeWithCtx_AfterRetryLoaderError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("after-retry-loader-error")
	// 使用 hook 使所有 GET 返回 redis.Nil，首次 SetNX 失败，EXISTS 返回 0
	hook := &failFirstNGetsHook{failCount: 100}
	client.AddHook(hook)
	client.AddHook(&failFirstSetNXAndExistsZeroHook{})

	testErr := errors.New("after-retry-loader-failed")
	_, err := handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, func(ctx context.Context) ([]byte, error) {
		return nil, testErr
	})
	assert.ErrorIs(t, err, testErr)
}

// TestRedisHandler_GetOrComputeWithCtx_AfterRetryTTLZero 测试重试后获取锁的 ttl<=0 分支
func TestRedisHandler_GetOrComputeWithCtx_AfterRetryTTLZero(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("after-retry-ttl-zero")
	// 使用 hook 使所有 GET 返回 redis.Nil，首次 SetNX 失败，EXISTS 返回 0
	hook := &failFirstNGetsHook{failCount: 100}
	client.AddHook(hook)
	client.AddHook(&failFirstSetNXAndExistsZeroHook{})

	result, err := handler.GetOrComputeWithCtx(context.Background(), key, 0, func(ctx context.Context) ([]byte, error) {
		return []byte("after-retry-zero-ttl-computed"), nil
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("after-retry-zero-ttl-computed"), result)
}

// TestRedisHandler_GetOrComputeWithCtx_AllFail 测试所有重试失败后返回错误
func TestRedisHandler_GetOrComputeWithCtx_AllFail(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping slow test in short mode")
	}

	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("all-fail-key")
	lockKey := string(key) + ":lock"

	// 预设锁，使 SetNX 始终失败
	client.Set(context.Background(), lockKey, "other-lock-value", 30*time.Second)

	// 不设置缓存，所有 GET 都返回 redis.Nil
	// 等待所有重试完成后，最终检查也失败，返回错误
	_, err := handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("loader should not be called")
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to compute value after max retries")
}

// TestRedisHandler_GetOrComputeWithCtx_EmptyKey 测试空 key 分支
func TestRedisHandler_GetOrComputeWithCtx_EmptyKey(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	_, err := handler.GetOrComputeWithCtx(context.Background(), []byte{}, time.Minute, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("should not be called")
	})
	assert.ErrorIs(t, err, ErrInvalidKey)
}

// TestRedisHandler_GetOrComputeWithCtx_LoadOrStoreLoaded 测试 LoadOrStore loaded 分支
func TestRedisHandler_GetOrComputeWithCtx_LoadOrStoreLoaded(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("loadorstore-loaded-key")

	// 预填充 loadGroup，使 Load 返回 false（key 不匹配）
	// 但 LoadOrStore 返回 loaded=true（需要另一个 call 已存储）
	// 由于 Load 和 LoadOrStore 之间存在竞争窗口，这里通过手动方式测试：
	// 直接在 loadGroup 中存储一个已完成的 call，使 Load 返回 true
	// 然后单独测试 LoadOrStore loaded 分支需要并发场景
	call := &redisLoadCall{val: []byte("loadorstore-value")}
	call.wg.Add(1)
	call.wg.Done()
	handler.loadGroup.Store(string(key), call)

	result, err := handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("should not be called")
	})
	assert.NoError(t, err)
	assert.Equal(t, []byte("loadorstore-value"), result)
}

// TestRedisHandler_GetOrComputeWithCtx_ConcurrentLoadOrStore 测试并发场景下的 LoadOrStore loaded 分支
func TestRedisHandler_GetOrComputeWithCtx_ConcurrentLoadOrStore(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	key := []byte("concurrent-loadorstore-key")

	// 使用 barrier 确保多个 goroutine 同时进入 GetOrComputeWithCtx
	var wg sync.WaitGroup
	var startBarrier sync.WaitGroup
	startBarrier.Add(1)

	results := make([][]byte, 5)
	errors := make([]error, 5)
	callCount := int32(0)

	loader := func(ctx context.Context) ([]byte, error) {
		atomic.AddInt32(&callCount, 1)
		time.Sleep(50 * time.Millisecond) // 模拟计算时间，增加竞争窗口
		return []byte("concurrent-result"), nil
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			startBarrier.Wait() // 等待所有 goroutine 就绪
			val, err := handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, loader)
			results[idx] = val
			errors[idx] = err
		}(i)
	}

	// 同时启动所有 goroutine
	startBarrier.Done()
	wg.Wait()

	// 验证所有调用都成功
	for i := 0; i < 5; i++ {
		assert.NoError(t, errors[i], "goroutine %d should succeed", i)
		assert.Equal(t, []byte("concurrent-result"), results[i], "goroutine %d should get correct result", i)
	}
}

// TestRedisHandler_BatchGetWithCtx_EmptyKeys 测试空 keys 分支（已覆盖但显式验证）
func TestRedisHandler_BatchGetWithCtx_EmptyKeys(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	results, errs := handler.BatchGetWithCtx(context.Background(), [][]byte{})
	assert.Nil(t, results)
	assert.Nil(t, errs)
}

// TestRedisHandler_BatchGetWithCtx_EmptyKeyInBatch 测试批量中包含空 key
func TestRedisHandler_BatchGetWithCtx_EmptyKeyInBatch(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	handler := &RedisHandler{redis: client, ctx: context.Background()}

	// 设置一个有效值
	handler.Set([]byte("valid-key"), []byte("valid-value"))

	// 批量中包含空 key 和有效 key
	results, errs := handler.BatchGetWithCtx(context.Background(), [][]byte{
		[]byte("valid-key"),
		[]byte{}, // 空 key
		nil,      // nil key
		[]byte("nonexistent"),
	})
	assert.Len(t, results, 4)
	assert.Len(t, errs, 4)
	assert.NoError(t, errs[0])
	assert.Equal(t, []byte("valid-value"), results[0])
	assert.ErrorIs(t, errs[1], ErrInvalidKey)
	assert.ErrorIs(t, errs[2], ErrInvalidKey)
	assert.ErrorIs(t, errs[3], ErrNotFound)
}

// TestRedisHandler_GetOrComputeWithCtx_LoadOrStoreRace_Success 通过大量并发 goroutine
// 配合 spammer goroutine 高频 store/delete loadGroup 条目，
// 触发 LoadOrStore 的 loaded=true 分支（成功路径）
func TestRedisHandler_GetOrComputeWithCtx_LoadOrStoreRace_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race test in short mode")
	}

	for iter := 0; iter < 10; iter++ {
		client := setupRedisClient(t)
		handler := &RedisHandler{redis: client, ctx: context.Background()}

		key := []byte(fmt.Sprintf("race-success-key-%d", iter))
		strKey := string(key)
		handler.Del(key)

		const numGoroutines = 200
		var wg sync.WaitGroup
		var startBarrier sync.WaitGroup
		startBarrier.Add(1)

		results := make([][]byte, numGoroutines)
		errors := make([]error, numGoroutines)

		loader := func(ctx context.Context) ([]byte, error) {
			return []byte("race-success-result"), nil
		}

		// spammer goroutine: 高频 store/delete loadGroup 条目，
		// 增加 Load 和 LoadOrStore 之间出现 store 操作的概率
		spammerStop := make(chan struct{})
		var spammerDone sync.WaitGroup
		spammerDone.Add(1)
		go func() {
			defer spammerDone.Done()
			for {
				select {
				case <-spammerStop:
					return
				default:
					call := &redisLoadCall{val: []byte("spam-value")}
					call.wg.Add(1)
					call.wg.Done()
					handler.loadGroup.Store(strKey, call)
					handler.loadGroup.Delete(strKey)
				}
			}
		}()

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				startBarrier.Wait()
				val, err := handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, loader)
				results[idx] = val
				errors[idx] = err
			}(i)
		}

		startBarrier.Done()
		wg.Wait()

		// 停止 spammer
		close(spammerStop)
		spammerDone.Wait()

		for i := 0; i < numGoroutines; i++ {
			if errors[i] != nil {
				// spammer 干扰可能导致一些 goroutine 收到错误，这是预期的
				continue
			}
			// 成功的 goroutine 应该返回有效的值
			if results[i] != nil {
				assert.Contains(t, []string{"race-success-result", "spam-value"}, string(results[i]))
			}
		}

		// 等待 loadGroup 清理完成
		time.Sleep(20 * time.Millisecond)
		client.Close()
	}
}

// TestRedisHandler_GetOrComputeWithCtx_LoadOrStoreRace_Error 通过大量并发 goroutine
// 配合 spammer goroutine，触发 LoadOrStore 的 loaded=true 分支（错误路径）
func TestRedisHandler_GetOrComputeWithCtx_LoadOrStoreRace_Error(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race test in short mode")
	}

	testErr := errors.New("race-loader-error")

	for iter := 0; iter < 10; iter++ {
		client := setupRedisClient(t)
		handler := &RedisHandler{redis: client, ctx: context.Background()}

		key := []byte(fmt.Sprintf("race-error-key-%d", iter))
		strKey := string(key)
		handler.Del(key)

		const numGoroutines = 200
		var wg sync.WaitGroup
		var startBarrier sync.WaitGroup
		startBarrier.Add(1)

		loader := func(ctx context.Context) ([]byte, error) {
			return nil, testErr
		}

		// spammer goroutine: 高频 store/delete loadGroup 条目
		spammerStop := make(chan struct{})
		var spammerDone sync.WaitGroup
		spammerDone.Add(1)
		go func() {
			defer spammerDone.Done()
			for {
				select {
				case <-spammerStop:
					return
				default:
					errCall := &redisLoadCall{err: testErr}
					errCall.wg.Add(1)
					errCall.wg.Done()
					handler.loadGroup.Store(strKey, errCall)
					handler.loadGroup.Delete(strKey)
				}
			}
		}()

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				startBarrier.Wait()
				handler.GetOrComputeWithCtx(context.Background(), key, time.Minute, loader)
			}()
		}

		startBarrier.Done()
		wg.Wait()

		close(spammerStop)
		spammerDone.Wait()

		time.Sleep(20 * time.Millisecond)
		client.Close()
	}
}
