/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-07-06 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-07-06 00:26:06
 * @FilePath: \go-cachex\object_pool_test.go
 * @Description: 通用对象池单元测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unregisterPool 测试辅助：从全局注册表移除并停止池，避免污染其它用例
func unregisterPool(name string) {
	objectPoolRegistryMu.Lock()
	defer objectPoolRegistryMu.Unlock()
	if p, ok := objectPoolRegistry[name]; ok {
		_ = p.Stop()
		delete(objectPoolRegistry, name)
	}
}

// uniqueName 生成唯一池名，避免用例间注册冲突
var uniqueCounter atomic.Int64

func uniqueName(prefix string) string {
	return prefix + "_" + assertObjPoolUniqueSuffix()
}

func assertObjPoolUniqueSuffix() string {
	n := uniqueCounter.Add(1)
	return time.Now().Format("150405") + "_" + itoa(n)
}

// itoa 简易整数转字符串（避免引入 strconv 增加可读性）
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestObjectPool_BasicAndColdStart 验证注册即启动 + 冷启动预生成
func TestObjectPool_BasicAndColdStart(t *testing.T) {
	name := uniqueName("basic")
	defer unregisterPool(name)

	var gen atomic.Int64
	// Capacity == ColdStartCount，同步预生成即填满，异步预填充看到池满直接退出
	// 避免 goroutine 调度时序导致断言不稳定
	pool := RegisterObjectPool[int](name, func() (int, error) {
		return int(gen.Add(1)), nil
	},
		WithObjectPoolCapacity(4),
		WithObjectPoolColdStartCount(4),
		WithObjectPoolRefreshInterval(time.Hour), // 拉长间隔避免干扰
		WithObjectPoolLogger(globalLogger),
	)

	// 冷启动同步预生成 4 个
	assert.Equal(t, 4, pool.Len(), "冷启动应同步预生成 4 个对象")
	assert.Equal(t, int64(4), pool.Stats().Generated)

	// TryGet 取出 4 个，值应为 1..4
	got := map[int]bool{}
	for i := 0; i < 4; i++ {
		v, ok := pool.TryGet()
		require.True(t, ok, "应能取出预生成的对象")
		got[v] = true
	}
	assert.Len(t, got, 4, "取出的对象应互不相同")
	assert.Equal(t, int64(4), pool.Stats().Served)

	// 池空后再取返回 false
	_, ok := pool.TryGet()
	assert.False(t, ok, "池空应返回 false")
}

// TestObjectPool_DepletedFallback 验证池空时调用方可回退实时构造
func TestObjectPool_DepletedFallback(t *testing.T) {
	name := uniqueName("depleted")
	defer unregisterPool(name)

	pool := RegisterObjectPool[string](name, func() (string, error) {
		return "pooled", nil
	},
		WithObjectPoolCapacity(2),
		WithObjectPoolColdStartCount(2),
		WithObjectPoolRefreshInterval(time.Hour),
	)

	// 取空
	_, _ = pool.TryGet()
	_, _ = pool.TryGet()
	_, ok := pool.TryGet()
	require.False(t, ok, "池空应返回 false")

	// 调用方回退逻辑（业务侧自行实时构造）
	fallback := "realtime"
	assert.Equal(t, "realtime", fallback, "调用方可回退实时构造")
}

// TestObjectPool_RefillOnLowWatermark 验证低于阈值时周期任务自动补充
func TestObjectPool_RefillOnLowWatermark(t *testing.T) {
	name := uniqueName("refill")
	defer unregisterPool(name)

	pool := RegisterObjectPool[int](name, func() (int, error) {
		return 1, nil
	},
		WithObjectPoolCapacity(10),
		WithObjectPoolColdStartCount(10), // 启动即填满
		WithObjectPoolMinThreshold(5),
		WithObjectPoolBatchSize(10),
		WithObjectPoolRefreshInterval(50*time.Millisecond), // 短间隔快速验证
	)

	// 启动时填满 10
	require.Equal(t, 10, pool.Len())

	// 消耗到阈值以下（取走 6 个，剩 4 < 5）
	for i := 0; i < 6; i++ {
		_, ok := pool.TryGet()
		require.True(t, ok)
	}
	require.Equal(t, 4, pool.Len(), "消耗后应剩 4 个，低于阈值 5")

	// 等待周期任务补充（带超时轮询）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pool.Len() == 10 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	assert.Equal(t, 10, pool.Len(), "周期任务应将池补充回 10")
}

// TestObjectPool_FactoryError 验证 factory 返回 error 时跳过并继续生成
func TestObjectPool_FactoryError(t *testing.T) {
	name := uniqueName("factoryerr")
	defer unregisterPool(name)

	var gen atomic.Int64
	var failRemaining atomic.Int64
	failRemaining.Store(5) // 前 5 次生成返回 error，之后都成功
	// MinThreshold == Capacity，强制周期任务持续补充直到填满
	pool := RegisterObjectPool[int](name, func() (int, error) {
		n := gen.Add(1)
		if failRemaining.Add(-1) >= 0 {
			return 0, errFactoryTestSentinel
		}
		return int(n), nil
	},
		WithObjectPoolCapacity(6),
		WithObjectPoolColdStartCount(0), // 关闭同步预生成，纯异步 + 周期
		WithObjectPoolMinThreshold(6),   // == Capacity，不满就补
		WithObjectPoolBatchSize(20),     // 大批量，单次周期即可填满
		WithObjectPoolRefreshInterval(20*time.Millisecond),
	)

	// 等待异步 + 周期填充填满（前 5 次失败，之后成功）
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pool.Len() >= 6 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	assert.Equal(t, 6, pool.Len(), "factory 错误应被跳过，池最终填满")

	// 取出的对象编号都应 > 5（前 5 次失败被跳过）
	for i := 0; i < 6; i++ {
		v, ok := pool.TryGet()
		require.True(t, ok)
		assert.Greater(t, v, 5, "取出的对象编号应 > 5（前 5 次失败被跳过）")
	}
}

// errFactoryTestSentinel 测试用 factory 错误
var errFactoryTestSentinel = &testError{"factory test error"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// TestObjectPool_DuplicateRegisterPanic 验证重复注册同名池 panic
func TestObjectPool_DuplicateRegisterPanic(t *testing.T) {
	name := uniqueName("dup")
	defer unregisterPool(name)

	RegisterObjectPool[int](name, func() (int, error) { return 1, nil },
		WithObjectPoolColdStartCount(0),
		WithObjectPoolRefreshInterval(time.Hour),
	)
	assert.Panics(t, func() {
		RegisterObjectPool[int](name, func() (int, error) { return 2, nil },
			WithObjectPoolColdStartCount(0),
			WithObjectPoolRefreshInterval(time.Hour),
		)
	}, "重复注册应 panic")
}

// TestObjectPool_GetTypeMismatch 验证类型不匹配时 Get 返回错误
func TestObjectPool_GetTypeMismatch(t *testing.T) {
	name := uniqueName("typemismatch")
	defer unregisterPool(name)

	RegisterObjectPool[int](name, func() (int, error) { return 1, nil },
		WithObjectPoolColdStartCount(0),
		WithObjectPoolRefreshInterval(time.Hour),
	)

	_, err := GetObjectPool[string](name)
	assert.Error(t, err, "类型不匹配应返回错误")

	_, err = GetObjectPool[int]("not_exist")
	assert.Error(t, err, "未注册应返回错误")

	must := MustGetObjectPool[int](name)
	assert.NotNil(t, must)
}

// TestObjectPool_StopIdempotent 验证 Stop 幂等且能停止补充
func TestObjectPool_StopIdempotent(t *testing.T) {
	name := uniqueName("stop")
	defer unregisterPool(name)

	var gen atomic.Int64
	pool := RegisterObjectPool[int](name, func() (int, error) {
		return int(gen.Add(1)), nil
	},
		WithObjectPoolCapacity(10),
		WithObjectPoolColdStartCount(0),
		WithObjectPoolRefreshInterval(20*time.Millisecond),
	)

	// 等待异步填充
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if pool.Len() >= 10 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	genBefore := gen.Load()

	// 多次 Stop 应幂等
	require.NoError(t, pool.Stop())
	require.NoError(t, pool.Stop())

	// 等待一段时间，确认不再有新对象生成
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, genBefore, gen.Load(), "Stop 后不应再生成新对象")

	// 取出后池不应被补充
	before := pool.Len()
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, before, pool.Len(), "Stop 后周期任务不应再补充")
}

// TestObjectPool_ConcurrentTryGet 验证并发取用安全性
func TestObjectPool_ConcurrentTryGet(t *testing.T) {
	name := uniqueName("concurrent")
	defer unregisterPool(name)

	pool := RegisterObjectPool[int](name, func() (int, error) {
		return 1, nil
	},
		WithObjectPoolCapacity(100),
		WithObjectPoolColdStartCount(100),
		WithObjectPoolRefreshInterval(time.Hour),
	)

	var wg sync.WaitGroup
	var served atomic.Int64
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, ok := pool.TryGet(); ok {
					served.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	// 50*20=1000 次取用，但池只有 100 个，served 应等于 100
	assert.Equal(t, int64(100), served.Load(), "并发取用应安全且不超发")
	assert.Equal(t, int64(100), pool.Stats().Served)
}

// TestObjectPool_StopAllObjectPools 验证全局启停
func TestObjectPool_StopAllObjectPools(t *testing.T) {
	name1 := uniqueName("all1")
	name2 := uniqueName("all2")
	defer unregisterPool(name1)
	defer unregisterPool(name2)

	RegisterObjectPool[int](name1, func() (int, error) { return 1, nil },
		WithObjectPoolColdStartCount(0), WithObjectPoolRefreshInterval(time.Hour))
	RegisterObjectPool[int](name2, func() (int, error) { return 2, nil },
		WithObjectPoolColdStartCount(0), WithObjectPoolRefreshInterval(time.Hour))

	// StopAll 应无错误（已启动的池重复 Stop 幂等）
	require.NoError(t, StopAllObjectPools())
	require.NoError(t, StopAllObjectPools())
}
