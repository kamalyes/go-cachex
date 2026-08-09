/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-08-09 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-08-09 00:00:00
 * @FilePath: \go-cachex\scheduler_test.go
 * @Description: 调度器管理器测试，覆盖配置选项/注册表/全局启停/SchedulerManager 方法
 *
 * 复用 queue_test.go 中的 setupRedisClient（基于 miniredis），
 * schedulerRegistry 为全局 map，每个测试通过 setupSchedulerEnv 备份并清空，
 * 测试结束自动恢复全局 Redis 客户端与日志器，避免污染其它测试。
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupSchedulerEnv 注入全局 Redis 客户端与日志器，并清空调度器注册表，
// 返回测试用 Redis 客户端；测试结束通过 t.Cleanup 自动恢复全局状态并清空注册表
func setupSchedulerEnv(t *testing.T) *redis.Client {
	t.Helper()
	client := setupRedisClient(t)

	oldRedis := globalRedisClient
	oldLogger := globalLogger
	SetGlobalRedisClient(client)
	SetLogger(NewDefaultCachexLogger())

	clearSchedulerRegistry()

	t.Cleanup(func() {
		SetGlobalRedisClient(oldRedis)
		SetLogger(oldLogger)
		clearSchedulerRegistry()
	})
	return client
}

// clearSchedulerRegistry 清空全局调度器注册表
func clearSchedulerRegistry() {
	schedulerRegistryMu.Lock()
	for k := range schedulerRegistry {
		delete(schedulerRegistry, k)
	}
	schedulerRegistryMu.Unlock()
}

// fakeScheduler 用于测试全局启停的错误分支（实现 Scheduler 接口）
type fakeScheduler struct {
	name     string
	startErr error
	stopErr  error
}

func (f *fakeScheduler) Name() string                    { return f.name }
func (f *fakeScheduler) Start(ctx context.Context) error { return f.startErr }
func (f *fakeScheduler) Stop() error                     { return f.stopErr }

// addFakeScheduler 直接向全局注册表注入 fake 调度器，用于覆盖错误分支
func addFakeScheduler(t *testing.T, name string, startErr, stopErr error) {
	t.Helper()
	schedulerRegistryMu.Lock()
	schedulerRegistry[name] = &fakeScheduler{name: name, startErr: startErr, stopErr: stopErr}
	schedulerRegistryMu.Unlock()
}

// noOpSchedulerHandler 返回一个总是成功的处理函数
func noOpSchedulerHandler[T any]() SchedulerHandler[T] {
	return func(ctx context.Context, task *DelayTask[T]) error { return nil }
}

// ============================================================
// 配置选项 测试
// ============================================================

func TestSchedulerOptions(t *testing.T) {
	var cfg SchedulerConfig
	opts := []SchedulerOption{
		WithSchedulerQueueName("my-queue"),
		WithSchedulerNamespace("my-ns"),
		WithSchedulerPollInterval(123 * time.Millisecond),
		WithSchedulerBatchSize(7),
		WithSchedulerConcurrency(3),
		WithSchedulerMaxRetries(2),
		WithSchedulerRetryDelay(200 * time.Millisecond),
		WithSchedulerVisibilityTimeout(10 * time.Second),
		WithSchedulerOrdered(),
		WithSchedulerMaxReadySize(500),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	assert.Equal(t, "my-queue", cfg.QueueName)
	assert.Equal(t, "my-ns", cfg.Namespace)
	assert.Equal(t, 123*time.Millisecond, cfg.PollInterval)
	assert.Equal(t, int64(7), cfg.BatchSize)
	assert.Equal(t, 3, cfg.Concurrency)
	assert.Equal(t, 2, cfg.MaxRetries)
	assert.Equal(t, 200*time.Millisecond, cfg.RetryDelay)
	assert.Equal(t, 10*time.Second, cfg.VisibilityTimeout)
	assert.True(t, cfg.Ordered)
	assert.Equal(t, int64(500), cfg.MaxReadySize)
}

// ============================================================
// RegisterScheduler 测试
// ============================================================

func TestRegisterScheduler_Success(t *testing.T) {
	setupSchedulerEnv(t)

	sm := RegisterScheduler[string]("ok", noOpSchedulerHandler[string](),
		WithSchedulerQueueName("q-ok"),
		WithSchedulerNamespace("ns-ok"),
		WithSchedulerPollInterval(50*time.Millisecond),
		WithSchedulerBatchSize(4),
		WithSchedulerConcurrency(2),
		WithSchedulerMaxRetries(1),
		WithSchedulerRetryDelay(10*time.Millisecond),
		WithSchedulerVisibilityTimeout(time.Minute),
		WithSchedulerOrdered(),
		WithSchedulerMaxReadySize(100),
	)
	defer sm.Stop()

	assert.Equal(t, "ok", sm.Name())
	// 校验配置已合并
	assert.Equal(t, "q-ok", sm.config.QueueName)
	assert.Equal(t, "ns-ok", sm.config.Namespace)
	assert.Equal(t, 50*time.Millisecond, sm.config.PollInterval)
	assert.Equal(t, int64(4), sm.config.BatchSize)
	assert.Equal(t, 2, sm.config.Concurrency)
	assert.Equal(t, 1, sm.config.MaxRetries)
	assert.Equal(t, 10*time.Millisecond, sm.config.RetryDelay)
	assert.Equal(t, time.Minute, sm.config.VisibilityTimeout)
	assert.True(t, sm.config.Ordered)
	assert.Equal(t, int64(100), sm.config.MaxReadySize)

	// 已注册到全局表，可通过 GetScheduler 取到同一实例
	got, err := GetScheduler[string]("ok")
	require.NoError(t, err)
	assert.Same(t, sm, got)
}

func TestRegisterScheduler_Panic_NoGlobalRedis(t *testing.T) {
	oldRedis := globalRedisClient
	globalRedisClient = nil
	defer func() { globalRedisClient = oldRedis }()
	clearSchedulerRegistry()

	assert.Panics(t, func() {
		RegisterScheduler[string]("no-redis", noOpSchedulerHandler[string](), WithSchedulerQueueName("q"))
	})
}

func TestRegisterScheduler_Panic_NilHandler(t *testing.T) {
	setupSchedulerEnv(t)

	assert.Panics(t, func() {
		RegisterScheduler[string]("nil-handler", nil, WithSchedulerQueueName("q"))
	})
}

func TestRegisterScheduler_Panic_Duplicate(t *testing.T) {
	setupSchedulerEnv(t)

	RegisterScheduler[string]("dup", noOpSchedulerHandler[string](), WithSchedulerQueueName("q-dup"))
	defer MustGetScheduler[string]("dup").Stop()

	assert.Panics(t, func() {
		RegisterScheduler[string]("dup", noOpSchedulerHandler[string](), WithSchedulerQueueName("q-dup2"))
	})
}

func TestRegisterScheduler_Panic_EmptyQueueName(t *testing.T) {
	setupSchedulerEnv(t)

	assert.Panics(t, func() {
		RegisterScheduler[string]("no-queue", noOpSchedulerHandler[string]())
	})
}

// ============================================================
// GetScheduler / MustGetScheduler 测试
// ============================================================

func TestGetScheduler_NotRegistered(t *testing.T) {
	setupSchedulerEnv(t)

	_, err := GetScheduler[string]("missing")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

func TestGetScheduler_TypeMismatch(t *testing.T) {
	setupSchedulerEnv(t)

	RegisterScheduler[string]("tm", noOpSchedulerHandler[string](), WithSchedulerQueueName("q-tm"))
	defer MustGetScheduler[string]("tm").Stop()

	// 以 int 类型取 string 调度器 → 类型不匹配
	_, err := GetScheduler[int]("tm")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "type mismatch")
}

func TestMustGetScheduler_Success(t *testing.T) {
	setupSchedulerEnv(t)

	RegisterScheduler[string]("must", noOpSchedulerHandler[string](), WithSchedulerQueueName("q-must"))
	defer MustGetScheduler[string]("must").Stop()

	sm := MustGetScheduler[string]("must")
	assert.NotNil(t, sm)
	assert.Equal(t, "must", sm.Name())
}

func TestMustGetScheduler_Panic(t *testing.T) {
	setupSchedulerEnv(t)

	assert.Panics(t, func() {
		MustGetScheduler[string]("not-exist")
	})
}

// ============================================================
// SchedulerManager 方法 测试
// ============================================================

func TestSchedulerManager_EnqueueAndQuery(t *testing.T) {
	setupSchedulerEnv(t)

	sm := RegisterScheduler[string]("query", noOpSchedulerHandler[string](),
		WithSchedulerQueueName("q-query"),
		WithSchedulerPollInterval(50*time.Millisecond))
	defer sm.Stop()

	ctx := context.Background()

	// 入队 2 个远期任务（不会被消费者消费，因为没有启动消费者）
	require.NoError(t, sm.EnqueueAt(ctx, &DelayTask[string]{Key: "k1", Data: "v1", ExecuteAt: time.Now().Add(time.Hour)}))
	require.NoError(t, sm.EnqueueAt(ctx, &DelayTask[string]{Key: "k2", Data: "v2", ExecuteAt: time.Now().Add(time.Hour)}))

	// Length
	n, err := sm.Length(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	// GetTask 命中
	task, err := sm.GetTask(ctx, "k1")
	require.NoError(t, err)
	assert.Equal(t, "v1", task.Data)
	assert.Equal(t, "k1", task.Key)

	// GetTask 未命中
	_, err = sm.GetTask(ctx, "nope")
	assert.ErrorIs(t, err, ErrDelayTaskNotFound)

	// Cancel 命中
	removed, err := sm.Cancel(ctx, "k1")
	require.NoError(t, err)
	assert.True(t, removed)

	// Cancel 重复 → false
	removed, err = sm.Cancel(ctx, "k1")
	require.NoError(t, err)
	assert.False(t, removed)

	// Length 减为 1
	n, err = sm.Length(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	// DeadLength 为 0
	dn, err := sm.DeadLength(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), dn)

	// GetDeadTasks 空队列返回空切片
	dead, err := sm.GetDeadTasks(ctx, 0, 10)
	require.NoError(t, err)
	assert.Empty(t, dead)
}

func TestSchedulerManager_EnqueueWithDelay(t *testing.T) {
	setupSchedulerEnv(t)

	sm := RegisterScheduler[string]("delay", noOpSchedulerHandler[string](),
		WithSchedulerQueueName("q-delay"))
	defer sm.Stop()

	before := time.Now()
	require.NoError(t, sm.EnqueueWithDelay(context.Background(), &DelayTask[string]{Key: "k1", Data: "v1"}, 200*time.Millisecond))

	task, err := sm.GetTask(context.Background(), "k1")
	require.NoError(t, err)
	// ExecuteAt 应约等于 before + 200ms
	assert.Greater(t, task.ExecuteAt.UnixMilli(), before.UnixMilli()+150)
	assert.Less(t, task.ExecuteAt.UnixMilli(), before.UnixMilli()+400)
}

func TestSchedulerManager_StartStop(t *testing.T) {
	setupSchedulerEnv(t)

	sm := RegisterScheduler[string]("ss", noOpSchedulerHandler[string](),
		WithSchedulerQueueName("q-ss"),
		WithSchedulerPollInterval(50*time.Millisecond))
	ctx := context.Background()

	// Start 成功
	require.NoError(t, sm.Start(ctx))

	// 重复 Start → ErrDelayQueueRunning
	assert.ErrorIs(t, sm.Start(ctx), ErrDelayQueueRunning)

	// Stop 成功
	require.NoError(t, sm.Stop())

	// Stop 幂等（DelayQueue.Stop 已关闭返回 nil）
	require.NoError(t, sm.Stop())
}

func TestSchedulerManager_DeadLetter(t *testing.T) {
	setupSchedulerEnv(t)

	var attempts atomic.Int32
	sm := RegisterScheduler[string]("dead", func(ctx context.Context, task *DelayTask[string]) error {
		attempts.Add(1)
		return errors.New("always fail")
	},
		WithSchedulerQueueName("q-dead"),
		WithSchedulerPollInterval(20*time.Millisecond),
		WithSchedulerBatchSize(5),
		WithSchedulerConcurrency(1),
		WithSchedulerMaxRetries(1), // 首次失败 + 1 次重试后进入死信
		WithSchedulerRetryDelay(5*time.Millisecond),
		WithSchedulerVisibilityTimeout(time.Minute))
	ctx := context.Background()

	require.NoError(t, sm.Start(ctx))
	defer sm.Stop()

	require.NoError(t, sm.EnqueueAt(ctx, &DelayTask[string]{
		Key: "fail", Data: "v", ExecuteAt: time.Now().Add(20 * time.Millisecond),
	}))

	// 等待进入死信队列
	require.Eventually(t, func() bool {
		n, _ := sm.DeadLength(ctx)
		return n >= 1
	}, 5*time.Second, 20*time.Millisecond)

	// 至少尝试 2 次（首次 + 1 次重试）
	assert.GreaterOrEqual(t, attempts.Load(), int32(2))

	dead, err := sm.GetDeadTasks(ctx, 0, 10)
	require.NoError(t, err)
	require.Len(t, dead, 1)
	assert.Equal(t, "fail", dead[0].Key)
}

// ============================================================
// 全局启停 测试
// ============================================================

func TestStartAllSchedulers_Success(t *testing.T) {
	setupSchedulerEnv(t)

	var processed atomic.Int32
	mk := func(name, qn string) {
		RegisterScheduler[int](name, func(ctx context.Context, task *DelayTask[int]) error {
			processed.Add(1)
			return nil
		}, WithSchedulerQueueName(qn), WithSchedulerPollInterval(30*time.Millisecond))
	}
	mk("a1", "qa1")
	mk("a2", "qa2")

	ctx := context.Background()

	sm1 := MustGetScheduler[int]("a1")
	sm2 := MustGetScheduler[int]("a2")
	require.NoError(t, sm1.EnqueueAt(ctx, &DelayTask[int]{Key: "k1", Data: 1, ExecuteAt: time.Now().Add(30 * time.Millisecond)}))
	require.NoError(t, sm2.EnqueueAt(ctx, &DelayTask[int]{Key: "k2", Data: 2, ExecuteAt: time.Now().Add(30 * time.Millisecond)}))

	// 启动所有
	require.NoError(t, StartAllSchedulers(ctx))
	defer StopAllSchedulers()

	// 再次启动 → ErrDelayQueueRunning 被过滤，返回 nil
	require.NoError(t, StartAllSchedulers(ctx))

	// 等待两个任务都处理完成
	require.Eventually(t, func() bool { return processed.Load() == 2 }, 3*time.Second, 20*time.Millisecond)

	// 停止所有（返回 nil，DelayQueue.Stop 永不报错）
	require.NoError(t, StopAllSchedulers())
}

func TestStartAllSchedulers_Error(t *testing.T) {
	setupSchedulerEnv(t)

	// 注入一个 Start 返回非 ErrDelayQueueRunning 错误的 fake
	addFakeScheduler(t, "fstart", errors.New("start boom"), nil)

	err := StartAllSchedulers(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fstart")
	assert.Contains(t, err.Error(), "start boom")
}

func TestStopAllSchedulers_Success(t *testing.T) {
	setupSchedulerEnv(t)

	// 两个 Stop 返回 nil 的 fake，覆盖无错误分支
	addFakeScheduler(t, "ok1", nil, nil)
	addFakeScheduler(t, "ok2", nil, nil)

	require.NoError(t, StopAllSchedulers())
}

func TestStopAllSchedulers_ErrorWithLogger(t *testing.T) {
	setupSchedulerEnv(t) // 注入了 globalLogger，覆盖 Errorf 分支

	// 两个 Stop 均报错，覆盖 firstErr 首次赋值与再次赋值（firstErr != nil）分支
	addFakeScheduler(t, "fstop1", nil, errors.New("stop boom1"))
	addFakeScheduler(t, "fstop2", nil, errors.New("stop boom2"))

	err := StopAllSchedulers()
	require.Error(t, err)
	// 返回首个错误（顺序不确定，断言二者之一）
	assert.Contains(t, err.Error(), "stop boom")
}

func TestStopAllSchedulers_ErrorWithoutLogger(t *testing.T) {
	setupSchedulerEnv(t)

	// 临时清空日志器，覆盖 globalLogger == nil 时跳过 Errorf 的分支
	oldLogger := globalLogger
	globalLogger = nil
	defer func() { globalLogger = oldLogger }()

	addFakeScheduler(t, "fnolog", nil, errors.New("stop nolog"))

	err := StopAllSchedulers()
	require.Error(t, err)
	assert.Equal(t, "stop nolog", err.Error())
}
