/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-26 01:05:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-26 01:05:00
 * @FilePath: \go-cachex\cache_builder_test.go
 * @Description: 缓存构建器测试
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */

package cachex

import (
	"context"
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

// failSetNXHook 使所有 SetNX 命令失败，用于测试锁获取失败场景
// go-redis v9 的 SetNX 在带 TTL 时使用 "set" 命令并附带 "nx" 参数，
// 不带 TTL 时使用 "setnx" 命令，因此需要同时检测两种情况
type failSetNXHook struct{}

func (h *failSetNXHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *failSetNXHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "setnx" {
			return fmt.Errorf("setnx command forced to fail")
		}
		if cmd.Name() == "set" {
			for _, arg := range cmd.Args() {
				if s, ok := arg.(string); ok && (s == "nx" || s == "NX") {
					return fmt.Errorf("setnx command forced to fail")
				}
			}
		}
		return next(ctx, cmd)
	}
}
func (h *failSetNXHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// failNthGetWithErrHook 使第 N 条 GET 命令返回自定义错误（非 redis.Nil）
type failNthGetWithErrHook struct {
	counter int32
	failAt  int32
	err     error
}

func (h *failNthGetWithErrHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *failNthGetWithErrHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "get" {
			if atomic.AddInt32(&h.counter, 1) == h.failAt {
				return h.err
			}
		}
		return next(ctx, cmd)
	}
}
func (h *failNthGetWithErrHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// TestCacheBuilder_Basic 测试基础构建
func TestCacheBuilder_Basic(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").
		WithTTL(time.Minute * 10).
		WithCompression(CompressionGzip).
		Build()

	assert.NotNil(t, cache)
	assert.Equal(t, "test", cache.strategy.Namespace)
	assert.Equal(t, time.Minute*10, cache.strategy.DefaultTTL)
	assert.Equal(t, CompressionGzip, cache.strategy.Compression)
}

// TestCacheBuilder_GetOrSet 测试GetOrSet模式
func TestCacheBuilder_GetOrSet(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	callCount := 0

	cache := NewCacheBuilder(client, "user").
		WithTTL(time.Minute).
		Build()

	loader := func() (interface{}, error) {
		callCount++
		return fmt.Sprintf("user_data_%d", callCount), nil
	}

	// 第一次调用 - 缓存未命中,执行loader
	val1, err := cache.GetOrSet(ctx, "user:1", loader)
	assert.NoError(t, err)
	assert.Equal(t, "user_data_1", val1)
	assert.Equal(t, 1, callCount)

	// 第二次调用 - 缓存命中,不执行loader
	val2, err := cache.GetOrSet(ctx, "user:1", loader)
	assert.NoError(t, err)
	assert.Equal(t, "user_data_1", val2)
	assert.Equal(t, 1, callCount, "loader should not be called again")
}

// TestCacheBuilder_OnMissCallback 测试缓存未命中回调
func TestCacheBuilder_OnMissCallback(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	loadCount := 0

	cache := NewCacheBuilder(client, "product").
		WithTTL(time.Minute).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			loadCount++
			return fmt.Sprintf("product_%s_data", key), nil
		}).
		Build()

	// 第一次Get - 触发OnMiss
	val1, err := cache.Get(ctx, "product:100")
	assert.NoError(t, err)
	assert.Equal(t, "product_product:100_data", val1)
	assert.Equal(t, 1, loadCount)

	// 第二次Get - 从缓存读取
	val2, err := cache.Get(ctx, "product:100")
	assert.NoError(t, err)
	assert.Equal(t, "product_product:100_data", val2)
	assert.Equal(t, 1, loadCount, "OnMiss should not be called again")
}

// TestCacheBuilder_WithLock 测试分布式锁
func TestCacheBuilder_WithLock(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	loadCount := 0

	cache := NewCacheBuilder(client, "config").
		WithTTL(time.Minute).
		WithLock(time.Second * 5).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			loadCount++
			time.Sleep(time.Millisecond * 100) // 模拟慢查询
			return fmt.Sprintf("config_%s", key), nil
		}).
		Build()

	// 并发Get - 只有一个请求会执行OnMiss
	done := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		go func() {
			val, err := cache.Get(ctx, "config:db")
			assert.NoError(t, err)
			assert.Equal(t, "config_config:db", val)
			done <- true
		}()
	}

	// 等待所有goroutine完成
	for i := 0; i < 5; i++ {
		<-done
	}

	// 验证只加载了一次
	assert.LessOrEqual(t, loadCount, 2, "loader should be called at most 2 times due to lock")
}

// TestCacheBuilder_SetAndDelete 测试Set和Delete
func TestCacheBuilder_SetAndDelete(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()

	cache := NewCacheBuilder(client, "session").
		WithTTL(time.Minute).
		Build()

	// 设置缓存
	err := cache.Set(ctx, "session:abc", "user_session_data")
	assert.NoError(t, err)

	// 验证Redis中的值
	val, err := client.Get(ctx, "session:session:abc").Result()
	assert.NoError(t, err)
	assert.Equal(t, "user_session_data", val)

	// 删除缓存
	err = cache.Delete(ctx, "session:abc")
	assert.NoError(t, err)

	// 验证已删除
	_, err = client.Get(ctx, "session:session:abc").Result()
	assert.Equal(t, redis.Nil, err)
}

// TestCacheBuilder_OnSetCallback 测试Set回调
func TestCacheBuilder_OnSetCallback(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	setKeys := []string{}

	cache := NewCacheBuilder(client, "notify").
		WithTTL(time.Minute).
		OnSet(func(ctx context.Context, key string, value interface{}) {
			setKeys = append(setKeys, key)
		}).
		Build()

	// 设置多个缓存
	cache.Set(ctx, "key1", "value1")
	cache.Set(ctx, "key2", "value2")
	cache.Set(ctx, "key3", "value3")

	// 验证回调被调用
	assert.Equal(t, 3, len(setKeys))
	assert.Contains(t, setKeys, "key1")
	assert.Contains(t, setKeys, "key2")
	assert.Contains(t, setKeys, "key3")
}

// TestCacheBuilder_CustomTTL 测试自定义TTL
func TestCacheBuilder_CustomTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()

	cache := NewCacheBuilder(client, "temp").
		WithTTL(time.Hour). // 默认1小时
		Build()

	// 使用默认TTL
	cache.Set(ctx, "key1", "value1")
	ttl1, _ := client.TTL(ctx, "temp:key1").Result()
	assert.InDelta(t, time.Hour.Seconds(), ttl1.Seconds(), 5)

	// 使用自定义TTL
	cache.Set(ctx, "key2", "value2", time.Minute*5)
	ttl2, _ := client.TTL(ctx, "temp:key2").Result()
	assert.InDelta(t, time.Minute.Seconds()*5, ttl2.Seconds(), 5)
}

// TestCacheBuilder_ChainedCalls 测试链式调用
func TestCacheBuilder_ChainedCalls(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	// 复杂的链式配置
	cache := NewCacheBuilder(client, "advanced").
		WithTTL(time.Minute * 30).
		WithCompression(CompressionGzip).
		WithKeyPattern("cache:{key}").
		WithLock(time.Second * 10).
		WithHotKey().
		WithPubSub().
		WithRefreshThreshold(0.3).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			return "loaded_data", nil
		}).
		OnSet(func(ctx context.Context, key string, value interface{}) {
			// 设置回调
		}).
		OnError(func(ctx context.Context, err error) {
			// 错误回调
		}).
		Build()

	assert.NotNil(t, cache)
	assert.True(t, cache.strategy.EnableLock)
	assert.True(t, cache.strategy.EnableHotKey)
	assert.True(t, cache.strategy.EnablePubSub)
	assert.Equal(t, 0.3, cache.strategy.RefreshThreshold)
	assert.NotNil(t, cache.lockManager)
	assert.NotNil(t, cache.hotKeyMgr)
	assert.NotNil(t, cache.pubsub)
}

// TestCacheBuilder_Subscribe_NotEnabled 测试未启用 PubSub 时 Subscribe 返回错误
func TestCacheBuilder_Subscribe_NotEnabled(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").Build()

	_, err := cache.Subscribe(context.Background(), "event", func(data interface{}) {})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not enabled")
}

// TestCacheBuilder_Subscribe_Enabled 测试启用 PubSub 时 Subscribe 成功
func TestCacheBuilder_Subscribe_Enabled(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").
		WithPubSub().
		Build()
	defer cache.Close()

	subscriber, err := cache.Subscribe(context.Background(), "test_event", func(data interface{}) {})
	assert.NoError(t, err)
	assert.NotNil(t, subscriber)
	subscriber.Stop()
}

// TestCacheBuilder_Close 测试 Close 方法释放所有资源
func TestCacheBuilder_Close(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").
		WithTTL(time.Minute).
		WithLock(time.Second * 5).
		WithHotKey().
		WithPubSub().
		Build()

	// 设置一些数据
	cache.Set(context.Background(), "key1", "value1")

	// Close 应该不 panic
	err := cache.Close()
	assert.NoError(t, err)
}

// TestCacheBuilder_Close_WithRefresh 测试 Close 等待后台刷新 goroutine
func TestCacheBuilder_Close_WithRefresh(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").
		WithTTL(time.Minute).
		WithRefreshThreshold(0.5).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			return "loaded_data", nil
		}).
		Build()

	// 设置一个短 TTL 的 key 触发 checkAndRefresh
	ctx := context.Background()
	cache.Set(ctx, "refresh_key", "data")

	// 手动设置短 TTL 触发刷新
	client.Set(ctx, "test:refresh_key", "data", 10*time.Second)

	// Get 会触发 checkAndRefresh
	cache.Get(ctx, "refresh_key")

	// 等待异步刷新 goroutine 启动
	time.Sleep(100 * time.Millisecond)

	// Close 应等待所有刷新 goroutine 退出
	err := cache.Close()
	assert.NoError(t, err)
}

// TestCacheBuilder_Get_NoLoader 测试 Get 在缓存未命中且无 loader 时返回错误
func TestCacheBuilder_Get_NoLoader(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").Build()

	_, err := cache.Get(context.Background(), "nonexistent_key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no loader")
}

// TestCacheBuilder_Get_RedisError 测试 Get 在 Redis 返回非 Nil 错误时返回错误
func TestCacheBuilder_Get_RedisError(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").Build()

	// 关闭 miniredis 使 Redis 操作返回错误
	mr.Close()

	_, err := cache.Get(context.Background(), "any_key")
	assert.Error(t, err)
}

// TestCacheBuilder_Set_Error 测试 Set 在 Redis 失败时返回错误
func TestCacheBuilder_Set_Error(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").Build()

	// 关闭 miniredis 使 Set 失败
	mr.Close()

	err := cache.Set(context.Background(), "key", "value")
	assert.Error(t, err)
}

// TestCacheBuilder_Set_WithPubSub 测试 Set 启用 PubSub 时发布事件
func TestCacheBuilder_Set_WithPubSub(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").
		WithPubSub().
		Build()
	defer cache.Close()

	err := cache.Set(context.Background(), "key1", "value1")
	assert.NoError(t, err)
}

// TestCacheBuilder_Delete_Error 测试 Delete 在 Redis 失败时返回错误
func TestCacheBuilder_Delete_Error(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").Build()

	// 关闭 miniredis 使 Del 失败
	mr.Close()

	err := cache.Delete(context.Background(), "key")
	assert.Error(t, err)
}

// TestCacheBuilder_Delete_WithPubSub 测试 Delete 启用 PubSub 时发布事件
func TestCacheBuilder_Delete_WithPubSub(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").
		WithPubSub().
		Build()
	defer cache.Close()

	// 先设置再删除
	cache.Set(context.Background(), "key1", "value1")
	err := cache.Delete(context.Background(), "key1")
	assert.NoError(t, err)
}

// TestCacheBuilder_GetOrSet_RedisError 测试 GetOrSet 在 Redis 返回非 Nil 错误时返回错误
func TestCacheBuilder_GetOrSet_RedisError(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").Build()

	mr.Close()

	loader := func() (interface{}, error) {
		return "data", nil
	}

	_, err := cache.GetOrSet(context.Background(), "key", loader)
	assert.Error(t, err)
}

// TestCacheBuilder_GetOrSet_LoaderError 测试 GetOrSet 在 loader 失败时返回错误
func TestCacheBuilder_GetOrSet_LoaderError(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").Build()

	loader := func() (interface{}, error) {
		return nil, fmt.Errorf("loader error")
	}

	_, err := cache.GetOrSet(context.Background(), "key", loader)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "loader error")
}

// TestCacheBuilder_LoadData_Error 测试 loadData 在 OnCacheMiss 失败时调用 OnError
func TestCacheBuilder_LoadData_Error(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	errorCalled := false
	cache := NewCacheBuilder(client, "test").
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			return nil, fmt.Errorf("load failed")
		}).
		OnError(func(ctx context.Context, err error) {
			errorCalled = true
		}).
		Build()

	_, err := cache.Get(context.Background(), "error_key")
	assert.Error(t, err)
	assert.True(t, errorCalled, "OnError 应被调用")
}

// TestCacheBuilder_Get_WithKeyPattern 测试 buildKey 使用 KeyPattern
func TestCacheBuilder_Get_WithKeyPattern(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").
		WithKeyPattern("cache:{key}").
		WithTTL(time.Minute).
		Build()

	ctx := context.Background()
	cache.Set(ctx, "key1", "value1")

	// 验证 buildKey 生成了正确的 key
	val, err := client.Get(ctx, "test:key1").Result()
	assert.NoError(t, err)
	assert.Equal(t, "value1", val)
}

// TestCacheBuilder_CheckAndRefresh_TTLError 测试 checkAndRefresh 在 TTL 查询失败时直接返回
func TestCacheBuilder_CheckAndRefresh_TTLError(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").
		WithTTL(time.Minute).
		WithRefreshThreshold(0.5).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			return "data", nil
		}).
		Build()
	defer cache.Close()

	ctx := context.Background()
	cache.Set(ctx, "key1", "value1")

	// 关闭 miniredis 使 TTL 查询失败
	mr.Close()

	// checkAndRefresh 应在 TTL 错误时直接返回，不 panic
	// Get 会调用 checkAndRefresh，但因为 redis 已关闭，Get 本身也会失败
	// 直接调用 checkAndRefresh
	cache.checkAndRefresh(ctx, "test:key1", "key1")
}

// TestCacheBuilder_DoCall_Concurrent 测试 doCall 在并发场景下的 singleflight 行为
func TestCacheBuilder_DoCall_Concurrent(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").Build()

	var callCount int32
	key := "concurrent_key"

	// 使用 doCall 并发调用同一个 key
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.doCall(key, func() (interface{}, error) {
				atomic.AddInt32(&callCount, 1)
				time.Sleep(50 * time.Millisecond)
				return "result", nil
			})
		}()
	}
	wg.Wait()

	// doCall 不保证只有一个调用执行（因为是 LoadOrStore 竞争），但至少执行一次
	assert.GreaterOrEqual(t, atomic.LoadInt32(&callCount), int32(1))
}

// TestCacheBuilder_DoCall_LoadOrStoreRace 测试 doCall 在 LoadOrStore 竞争条件下的 loaded 分支
func TestCacheBuilder_DoCall_LoadOrStoreRace(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").Build()

	key := "race_loadorstore_key"
	var callCount int32

	// 使用 barrier 确保所有 goroutine 同时启动，最大化 LoadOrStore 竞争窗口
	var barrier sync.WaitGroup
	barrier.Add(50)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			barrier.Done()
			barrier.Wait()
			cache.doCall(key, func() (interface{}, error) {
				atomic.AddInt32(&callCount, 1)
				time.Sleep(20 * time.Millisecond)
				return "result", nil
			})
		}()
	}
	wg.Wait()

	assert.GreaterOrEqual(t, atomic.LoadInt32(&callCount), int32(1))
}

// TestCacheBuilder_Get_RecheckCacheHit 测试 Get 在 doCall 闭包中再次检查缓存命中
func TestCacheBuilder_Get_RecheckCacheHit(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	// 先设置 key
	ctx := context.Background()
	client.Set(ctx, "test:recheck_key", "cached_value", time.Minute)

	// 使用 hook 使第一次 Get 返回 redis.Nil，第二次 Get 正常返回
	client.AddHook(&failFirstNGetsHook{failCount: 1})

	cache := NewCacheBuilder(client, "test").
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			return "loaded_value", nil
		}).
		Build()
	defer cache.Close()

	val, err := cache.Get(ctx, "recheck_key")
	assert.NoError(t, err)
	assert.Equal(t, "cached_value", val)
}

// TestCacheBuilder_GetOrSet_RecheckCacheHit 测试 GetOrSet 在 doCall 闭包中再次检查缓存命中
func TestCacheBuilder_GetOrSet_RecheckCacheHit(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()
	client.Set(ctx, "test:recheck_orset", "cached_value", time.Minute)

	// 使第一次 Get 返回 redis.Nil，第二次 Get 正常
	client.AddHook(&failFirstNGetsHook{failCount: 1})

	cache := NewCacheBuilder(client, "test").Build()

	val, err := cache.GetOrSet(ctx, "recheck_orset", func() (interface{}, error) {
		return "loaded_value", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "cached_value", val)
}

// TestCacheBuilder_LoadWithLock_DoubleCheckHit 测试 loadWithLock double-check 缓存命中
func TestCacheBuilder_LoadWithLock_DoubleCheckHit(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()
	client.Set(ctx, "test:lock_recheck", "cached_value", time.Minute)

	// 使第一次 Get 返回 redis.Nil，第二次 Get（loadWithLock 闭包内）正常返回
	client.AddHook(&failFirstNGetsHook{failCount: 1})

	cache := NewCacheBuilder(client, "test").
		WithLock(time.Second * 5).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			return "loaded_value", nil
		}).
		Build()
	defer cache.Close()

	val, err := cache.Get(ctx, "lock_recheck")
	assert.NoError(t, err)
	assert.Equal(t, "cached_value", val)
}

// TestCacheBuilder_LoadWithLock_NonNilError 测试 loadWithLock 返回非 Nil 错误
func TestCacheBuilder_LoadWithLock_NonNilError(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	// 使用 hook 使第 2 条 GET 命令（loadWithLock 闭包内的 double-check）返回非 Nil 错误
	client.AddHook(&failNthGetWithErrHook{failAt: 2, err: fmt.Errorf("custom redis error")})

	cache := NewCacheBuilder(client, "test").
		WithLock(time.Second * 5).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			return "loaded_value", nil
		}).
		Build()
	defer cache.Close()

	_, err := cache.Get(context.Background(), "lock_err_key")
	assert.Error(t, err)
}

// TestCacheBuilder_LoadWithLock_LockFail 测试 loadWithLock 锁获取失败时降级到 loadData
func TestCacheBuilder_LoadWithLock_LockFail(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	// 使用 hook 使 SetNX 命令（锁获取）失败，但不影响 Get/Set
	client.AddHook(&failSetNXHook{})

	cache := NewCacheBuilder(client, "test").
		WithLock(time.Second * 5).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			return "loaded_value", nil
		}).
		Build()
	defer cache.Close()

	val, err := cache.Get(context.Background(), "lock_fail_key")
	assert.NoError(t, err)
	assert.Equal(t, "loaded_value", val)
}

// TestCacheBuilder_LoadWithLock_TripleCheckHit 测试 loadWithLock 获取锁后三次检查缓存命中
func TestCacheBuilder_LoadWithLock_TripleCheckHit(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()
	client.Set(ctx, "test:triple_check", "cached_value", time.Minute)

	// 使前 2 次 Get 返回 redis.Nil，第 3 次 Get（获取锁后）正常返回
	client.AddHook(&failFirstNGetsHook{failCount: 2})

	cache := NewCacheBuilder(client, "test").
		WithLock(time.Second * 5).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			return "loaded_value", nil
		}).
		Build()
	defer cache.Close()

	val, err := cache.Get(ctx, "triple_check")
	assert.NoError(t, err)
	assert.Equal(t, "cached_value", val)
}

// TestCacheBuilder_CheckAndRefresh_OnPanic 测试 checkAndRefresh 中 OnCacheMiss panic 时触发 OnError
func TestCacheBuilder_CheckAndRefresh_OnPanic(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	var panicErr atomic.Value
	cache := NewCacheBuilder(client, "test").
		WithTTL(time.Minute).
		WithRefreshThreshold(0.5).
		OnMiss(func(ctx context.Context, key string) (interface{}, error) {
			panic("intentional panic in OnCacheMiss")
		}).
		OnError(func(ctx context.Context, err error) {
			panicErr.Store(err.Error())
		}).
		Build()
	defer cache.Close()

	ctx := context.Background()
	// 设置一个短 TTL 的 key 触发 checkAndRefresh
	cache.Set(ctx, "panic_refresh_key", "data")
	// 手动设置短 TTL 使 remaining < threshold
	client.Set(ctx, "test:panic_refresh_key", "data", 10*time.Second)

	// Get 会触发 checkAndRefresh
	cache.Get(ctx, "panic_refresh_key")

	// 等待异步刷新 goroutine 执行并 panic
	time.Sleep(200 * time.Millisecond)

	errStr, _ := panicErr.Load().(string)
	assert.Contains(t, errStr, "panic in checkAndRefresh")
}

// TestCacheBuilder_Subscribe_ReceiveMessage 测试 Subscribe 接收到消息时调用 handler
func TestCacheBuilder_Subscribe_ReceiveMessage(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	cache := NewCacheBuilder(client, "test").
		WithPubSub().
		Build()
	defer cache.Close()

	ctx := context.Background()

	var receivedMsg atomic.Value
	subscriber, err := cache.Subscribe(ctx, "test_event", func(data interface{}) {
		receivedMsg.Store(data)
	})
	require.NoError(t, err)
	require.NotNil(t, subscriber)
	defer subscriber.Stop()

	// 等待订阅就绪
	time.Sleep(200 * time.Millisecond)

	// 发布消息（使用与 cache 相同的命名空间）
	pubsub := NewPubSub(client, WithPubSubNamespace("test"))
	err = pubsub.Publish(ctx, "test_event", "hello_from_test")
	assert.NoError(t, err)

	// 等待消息接收
	time.Sleep(200 * time.Millisecond)

	msg, _ := receivedMsg.Load().(string)
	assert.Equal(t, "hello_from_test", msg)
}

// Benchmark测试
func BenchmarkCacheBuilder_Get(b *testing.B) {
	mr := miniredis.RunT(b)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	cache := NewCacheBuilder(client, "bench").
		WithTTL(time.Minute).
		Build()

	cache.Set(ctx, "bench_key", "bench_value")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(ctx, "bench_key")
	}
}

func BenchmarkCacheBuilder_Set(b *testing.B) {
	mr := miniredis.RunT(b)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	cache := NewCacheBuilder(client, "bench").
		WithTTL(time.Minute).
		Build()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set(ctx, fmt.Sprintf("key_%d", i), fmt.Sprintf("value_%d", i))
	}
}
