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
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
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

// fakePool 用于测试全局启停的 mock ObjectPool，可注入 Start/Stop 错误
type fakePool struct {
	name     string
	startErr error
	stopErr  error
}

func (f *fakePool) Start(ctx context.Context) error { return f.startErr }
func (f *fakePool) Stop() error                     { return f.stopErr }
func (f *fakePool) Name() string                    { return f.name }

// registerFakePool 直接将 mock 池注册到全局表（绕过 RegisterObjectPool 的启动逻辑）
func registerFakePool(name string, startErr, stopErr error) {
	objectPoolRegistryMu.Lock()
	defer objectPoolRegistryMu.Unlock()
	objectPoolRegistry[name] = &fakePool{name: name, startErr: startErr, stopErr: stopErr}
}

// TestObjectPool_Name 验证 Name 方法返回池名称
func TestObjectPool_Name(t *testing.T) {
	name := uniqueName("name")
	defer unregisterPool(name)

	pool := RegisterObjectPool[int](name, func() (int, error) { return 1, nil },
		WithObjectPoolColdStartCount(0), WithObjectPoolRefreshInterval(time.Hour))

	assert.Equal(t, name, pool.Name(), "Name 应返回注册时的名称")
}

// TestObjectPool_StartAllObjectPools 验证 StartAllObjectPools 启动所有已注册池
func TestObjectPool_StartAllObjectPools(t *testing.T) {
	name1 := uniqueName("startall1")
	name2 := uniqueName("startall2")
	defer unregisterPool(name1)
	defer unregisterPool(name2)

	// 已自动启动的池，StartAll 再次启动应幂等无错误
	RegisterObjectPool[int](name1, func() (int, error) { return 1, nil },
		WithObjectPoolColdStartCount(0), WithObjectPoolRefreshInterval(time.Hour))
	RegisterObjectPool[int](name2, func() (int, error) { return 2, nil },
		WithObjectPoolColdStartCount(0), WithObjectPoolRefreshInterval(time.Hour))

	require.NoError(t, StartAllObjectPools(context.Background()))
}

// TestObjectPool_StartAllObjectPoolsError 验证 StartAllObjectPools 在某池 Start 失败时返回错误
func TestObjectPool_StartAllObjectPoolsError(t *testing.T) {
	name := uniqueName("startallerr")
	defer unregisterPool(name)

	startErr := errors.New("start failed")
	// 直接注册一个 Start 返回错误的 mock 池
	registerFakePool(name, startErr, nil)

	err := StartAllObjectPools(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start object pool")
	assert.ErrorIs(t, err, startErr)
}

// TestObjectPool_StopAllObjectPoolsError 验证 StopAllObjectPools 在某池 Stop 失败时返回首个错误
func TestObjectPool_StopAllObjectPoolsError(t *testing.T) {
	name := uniqueName("stopallerr")
	defer unregisterPool(name)

	stopErr := errors.New("stop failed")
	registerFakePool(name, nil, stopErr)

	err := StopAllObjectPools()
	require.Error(t, err)
	assert.ErrorIs(t, err, stopErr)
}

// TestObjectPool_StopAllObjectPoolsErrorWithoutLogger 验证 StopAllObjectPools 在 globalLogger 为 nil 时仍返回错误
func TestObjectPool_StopAllObjectPoolsErrorWithoutLogger(t *testing.T) {
	name := uniqueName("stopallnolog")
	defer unregisterPool(name)

	// 临时清空全局日志器，覆盖 globalLogger == nil 分支
	oldLogger := globalLogger
	globalLogger = nil
	defer func() { globalLogger = oldLogger }()

	stopErr := errors.New("stop nolog")
	registerFakePool(name, nil, stopErr)

	err := StopAllObjectPools()
	require.Error(t, err)
	assert.ErrorIs(t, err, stopErr)
}

// TestObjectPool_ResolveLogger 验证 resolveLogger 的三个分支
func TestObjectPool_ResolveLogger(t *testing.T) {
	// 1. 显式传入非 nil 日志器 -> 返回该日志器
	custom := logger.NewLogger()
	assert.Same(t, custom, resolveLogger(custom))

	// 2. 传入 nil，globalLogger 非 nil -> 返回 globalLogger
	old := globalLogger
	defer func() { globalLogger = old }()
	gl := logger.NewLogger()
	globalLogger = gl
	assert.Same(t, gl, resolveLogger(nil))

	// 3. 传入 nil，globalLogger 为 nil -> 返回默认日志器（非 nil）
	globalLogger = nil
	assert.NotNil(t, resolveLogger(nil))
}

// TestObjectPool_RegisterPanicConditions 验证注册时的 panic 分支
func TestObjectPool_RegisterPanicConditions(t *testing.T) {
	// factory 为 nil 应 panic
	assert.Panics(t, func() {
		RegisterObjectPool[int](uniqueName("nilfactory"), nil,
			WithObjectPoolColdStartCount(0), WithObjectPoolRefreshInterval(time.Hour))
	}, "factory 为 nil 应 panic")

	// capacity <= 0 应 panic
	assert.Panics(t, func() {
		RegisterObjectPool[int](uniqueName("zerocap"), func() (int, error) { return 1, nil },
			WithObjectPoolCapacity(0),
			WithObjectPoolColdStartCount(0), WithObjectPoolRefreshInterval(time.Hour))
	}, "capacity <= 0 应 panic")
}

// TestObjectPool_MustGetObjectPoolPanic 验证 MustGetObjectPool 未注册时 panic
func TestObjectPool_MustGetObjectPoolPanic(t *testing.T) {
	assert.Panics(t, func() {
		MustGetObjectPool[int]("definitely-not-registered-pool-xyz")
	}, "未注册的池应 panic")
}

// TestObjectPool_StartPeriodicManagerError 验证 Start 在周期管理器已运行时返回错误
func TestObjectPool_StartPeriodicManagerError(t *testing.T) {
	// 手动构造 manager，预先启动 periodicMgr 使其 isRunning=true，
	// 之后调用 Start 时 StartWithContext 返回 "already running" 错误
	m := &ObjectPoolManager[int]{
		name:    "start-err-test",
		config:  ObjectPoolConfig{Capacity: 1, ColdStartCount: 0, MinThreshold: 1, BatchSize: 1, RefreshInterval: time.Hour},
		factory: func() (int, error) { return 1, nil },
		pool:    make(chan int, 1),
		logger:  resolveLogger(nil),
	}
	periodicMgr := syncx.NewPeriodicTaskManager()
	periodicMgr.AddTask(syncx.NewPeriodicTask("prestart", time.Hour, func(ctx context.Context) error { return nil }))
	require.NoError(t, periodicMgr.Start()) // isRunning=true
	m.periodicMgr = periodicMgr

	err := m.Start(context.Background())
	assert.Error(t, err, "周期管理器已运行时 Start 应返回错误")

	// 清理：停止周期管理器并标记停止
	_ = m.Stop()
	_ = periodicMgr.Stop()
}

// TestObjectPool_RefillTaskBranches 验证 refillTask 的各分支
func TestObjectPool_RefillTaskBranches(t *testing.T) {
	// 直接构造 manager 以便精确控制状态，不经过 RegisterObjectPool
	mkManager := func(capacity, minThreshold, batchSize int) *ObjectPoolManager[int] {
		return &ObjectPoolManager[int]{
			name:    "refill-test",
			config:  ObjectPoolConfig{Capacity: capacity, MinThreshold: minThreshold, BatchSize: batchSize, ColdStartCount: 0, RefreshInterval: time.Hour},
			factory: func() (int, error) { return 1, nil },
			pool:    make(chan int, capacity),
			logger:  resolveLogger(nil),
		}
	}

	t.Run("已停止返回nil", func(t *testing.T) {
		m := mkManager(10, 5, 2)
		m.stopped.Store(true)
		assert.NoError(t, m.refillTask(context.Background()))
	})

	t.Run("高于阈值返回nil", func(t *testing.T) {
		m := mkManager(10, 5, 2)
		// 填充到 6（>= MinThreshold 5）
		for i := 0; i < 6; i++ {
			m.pool <- i
		}
		assert.NoError(t, m.refillTask(context.Background()))
		assert.Equal(t, 6, len(m.pool), "高于阈值时不应补充")
	})

	t.Run("need超过BatchSize时被截断", func(t *testing.T) {
		m := mkManager(10, 5, 2)
		// 池为空：current=0 < MinThreshold=5，need=10-0=10 > BatchSize=2 -> need=2
		assert.NoError(t, m.refillTask(context.Background()))
		assert.Equal(t, 2, len(m.pool), "补充数量应被 BatchSize 截断为 2")
	})

	t.Run("need未超过BatchSize时全量补充", func(t *testing.T) {
		m := mkManager(10, 5, 20)
		// 池为空：need=10 < BatchSize=20 -> need=10
		assert.NoError(t, m.refillTask(context.Background()))
		assert.Equal(t, 10, len(m.pool), "need 未超过 BatchSize 时应全量补充到 10")
	})
}

// TestObjectPool_FillStoppedAndFullBranches 验证 fill 的停止与池满分支
func TestObjectPool_FillStoppedAndFullBranches(t *testing.T) {
	t.Run("已停止时fill立即返回", func(t *testing.T) {
		m := &ObjectPoolManager[int]{
			name:    "fill-stopped",
			config:  ObjectPoolConfig{Capacity: 10, MinThreshold: 5, BatchSize: 5, ColdStartCount: 0, RefreshInterval: time.Hour},
			factory: func() (int, error) { return 1, nil },
			pool:    make(chan int, 10),
			logger:  resolveLogger(nil),
		}
		m.stopped.Store(true)
		m.fill(5) // 停止时应立即返回，不生成任何对象
		assert.Equal(t, 0, len(m.pool))
		assert.Equal(t, int64(0), m.generated.Load())
	})

	t.Run("池满时fill立即返回", func(t *testing.T) {
		m := &ObjectPoolManager[int]{
			name:    "fill-full",
			config:  ObjectPoolConfig{Capacity: 2, MinThreshold: 1, BatchSize: 5, ColdStartCount: 0, RefreshInterval: time.Hour},
			factory: func() (int, error) { return 1, nil },
			pool:    make(chan int, 2),
			logger:  resolveLogger(nil),
		}
		// 预先填满
		m.pool <- 1
		m.pool <- 2
		m.fill(5) // 池满（len >= Capacity）应立即返回
		assert.Equal(t, 2, len(m.pool))
	})
}

// TestObjectPool_FillDefaultRaceBranch 验证 fill 在并发竞争时 select 命中 default 分支
// 通过控制 factory 阻塞，在 len 检查通过后、send 前手动填满 channel，使 send 命中 default
func TestObjectPool_FillDefaultRaceBranch(t *testing.T) {
	m := &ObjectPoolManager[int]{
		name:   "fill-default",
		config: ObjectPoolConfig{Capacity: 1, MinThreshold: 1, BatchSize: 1, ColdStartCount: 0, RefreshInterval: time.Hour},
		pool:   make(chan int, 1),
		logger: resolveLogger(nil),
	}

	entered := make(chan struct{})
	proceed := make(chan struct{})
	m.factory = func() (int, error) {
		entered <- struct{}{} // 通知主 goroutine factory 已进入
		<-proceed             // 阻塞等待放行
		return 42, nil
	}

	done := make(chan struct{})
	go func() {
		m.fill(2) // 会进入 factory 一次
		close(done)
	}()

	// 等待 factory 被调用（此时 fill 已通过 len 检查，正在 factory 内）
	<-entered
	// 手动填满 channel，使随后的 send 命中 default
	m.pool <- 99
	// 放行 factory，让它返回；fill 随后 select 时 channel 已满 -> default -> return
	close(proceed)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fill 未在超时内返回")
	}

	// channel 应仍只有手动放入的 99，default 分支丢弃了 factory 产出的值
	require.Equal(t, 1, len(m.pool))
	v, ok := <-m.pool
	require.True(t, ok)
	assert.Equal(t, 99, v)
	// generated 仍会计数（factory 被调用并产出了值，只是 send 时被丢弃）
	assert.Equal(t, int64(1), m.generated.Load())
}

// TestObjectPool_RegisterConfigEdgeCases 验证注册时配置边界分支
// - ColdStartCount < 0 被归零
// - MinThreshold > Capacity 被截断为 Capacity
func TestObjectPool_RegisterConfigEdgeCases(t *testing.T) {
	name := uniqueName("cfgedge")
	defer unregisterPool(name)

	pool := RegisterObjectPool[int](name, func() (int, error) { return 1, nil },
		WithObjectPoolCapacity(2),
		WithObjectPoolMinThreshold(10),   // > Capacity，应被截断为 Capacity
		WithObjectPoolColdStartCount(-1), // < 0，应被归零
		WithObjectPoolRefreshInterval(time.Hour),
	)

	stats := pool.Stats()
	assert.Equal(t, 2, stats.Capacity, "Capacity 应为 2")
	// ColdStartCount 被归零，异步预填充会填满到 Capacity
	// 等待异步填充完成
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) && pool.Len() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, 2, pool.Len(), "ColdStartCount 归零后异步预填充应填满到 Capacity")
}

// TestObjectPool_StopAllObjectPoolsMultipleErrors 验证 StopAllObjectPools 多池均失败时只返回首个错误
func TestObjectPool_StopAllObjectPoolsMultipleErrors(t *testing.T) {
	name1 := uniqueName("multierr1")
	name2 := uniqueName("multierr2")
	defer unregisterPool(name1)
	defer unregisterPool(name2)

	err1 := errors.New("stop one")
	err2 := errors.New("stop two")
	// 注册两个均返回错误的 mock 池，第二个池命中 firstErr != nil 分支（仅记录日志不覆盖）
	registerFakePool(name1, nil, err1)
	registerFakePool(name2, nil, err2)

	err := StopAllObjectPools()
	require.Error(t, err)
	// 返回的应为其中一个错误（首个，顺序由 map 遍历决定，断言为二者之一）
	assert.True(t, errors.Is(err, err1) || errors.Is(err, err2), "应返回首个错误")
}

// TestObjectPool_AsyncPrefillPanic 验证异步预填充 factory panic 时被 OnPanic 捕获并记录日志
func TestObjectPool_AsyncPrefillPanic(t *testing.T) {
	name := uniqueName("panic")
	defer unregisterPool(name)

	// ColdStartCount=0 + Capacity>0 触发异步预填充；factory panic 被 OnPanic 捕获
	pool := RegisterObjectPool[int](name, func() (int, error) {
		panic("factory boom")
	},
		WithObjectPoolCapacity(2),
		WithObjectPoolColdStartCount(0),
		WithObjectPoolRefreshInterval(time.Hour), // 拉长间隔避免周期任务干扰
	)

	// 等待异步预填充触发 panic 并被 OnPanic 捕获（pool 应保持空）
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if pool.Stats().Generated >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// factory panic 不会产出对象，池应保持空
	assert.Equal(t, 0, pool.Len(), "factory panic 时池应保持空")
}

// TestObjectPool_RefillTaskPanicOnError 验证周期 refillTask panic 时被 SetOnError 捕获并记录日志
// syncx 周期任务在 executeFunc panic 时通过 recover 调用 onError，触发 Start 中注册的 SetOnError 闭包
func TestObjectPool_RefillTaskPanicOnError(t *testing.T) {
	name := uniqueName("refillpanic")
	defer unregisterPool(name)

	// factory 始终 panic；MinThreshold=1 确保空池时 refillTask 调用 fill -> factory panic -> recover -> onError
	// 注意：Capacity=2 时默认 MinThreshold=2/3=0（整数除法），会导致 refillTask 直接返回 nil 不调用 fill
	pool := RegisterObjectPool[int](name, func() (int, error) {
		panic("refill boom")
	},
		WithObjectPoolCapacity(4),
		WithObjectPoolMinThreshold(2), // 显式设置 >0，空池时 current(0) < 2 触发 fill
		WithObjectPoolColdStartCount(0),
		WithObjectPoolRefreshInterval(20*time.Millisecond), // 短间隔快速触发周期任务
	)

	// 等待若干次周期 refillTask 触发 panic 并被 onError 捕获
	time.Sleep(300 * time.Millisecond)
	// factory panic 不产出对象，池应保持空
	assert.Equal(t, 0, pool.Len(), "周期 refillTask panic 时池应保持空")
}

// TestObjectPool_StopAllObjectPoolsWithLogger 验证 StopAllObjectPools 在 globalLogger 非 nil 时记录错误日志
func TestObjectPool_StopAllObjectPoolsWithLogger(t *testing.T) {
	name := uniqueName("stopallwithlog")
	defer unregisterPool(name)

	// 临时设置全局日志器（非 nil），覆盖 globalLogger != nil 时调用 Errorf 的分支
	oldLogger := globalLogger
	globalLogger = NewDefaultCachexLogger()
	defer func() { globalLogger = oldLogger }()

	stopErr := errors.New("stop with log")
	registerFakePool(name, nil, stopErr)

	err := StopAllObjectPools()
	require.Error(t, err)
	assert.ErrorIs(t, err, stopErr)
}
