/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-07-02 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-07-02 02:18:18
 * @FilePath: \go-cachex\delay_queue_test.go
 * @Description: 延迟队列测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
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

// newDelayQueueClient 创建基于 miniredis 的测试客户端
func newDelayQueueClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return client, mr
}

// TestDelayQueue_EnqueueAndLength 测试入队与长度查询
func TestDelayQueue_EnqueueAndLength(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{
		Namespace:         "t",
		PollInterval:      50 * time.Millisecond,
		BatchSize:         10,
		Concurrency:       2,
		MaxRetries:        2,
		RetryDelay:        100 * time.Millisecond,
		VisibilityTimeout: time.Minute,
	})
	defer q.Stop()

	ctx := context.Background()

	// 入队 3 个任务
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "k1", Data: "v1", ExecuteAt: time.Now().Add(time.Hour),
	}))
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "k2", Data: "v2", ExecuteAt: time.Now().Add(time.Hour),
	}))
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "k3", Data: "v3", ExecuteAt: time.Now().Add(time.Hour),
	}))

	len, err := q.Length(ctx, "q1")
	require.NoError(t, err)
	assert.Equal(t, int64(3), len)
}

// TestDelayQueue_Reschedule 测试同 Key 重新调度（覆盖执行时间）
func TestDelayQueue_Reschedule(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{Namespace: "t"})
	defer q.Stop()

	ctx := context.Background()

	first := time.Now().Add(time.Hour)
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "k1", Data: "v1", ExecuteAt: first,
	}))

	// 同 Key 重新调度到更早时间
	earlier := time.Now().Add(time.Minute)
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "k1", Data: "v1-updated", ExecuteAt: earlier,
	}))

	// 长度仍为 1（覆盖而非新增）
	len, err := q.Length(ctx, "q1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), len)

	// 数据应为更新后的
	task, err := q.GetTask(ctx, "q1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "v1-updated", task.Data)
}

// TestDelayQueue_Cancel 测试取消任务
func TestDelayQueue_Cancel(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{Namespace: "t"})
	defer q.Stop()

	ctx := context.Background()

	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "k1", Data: "v1", ExecuteAt: time.Now().Add(time.Hour),
	}))

	// 取消存在的任务
	removed, err := q.Cancel(ctx, "q1", "k1")
	require.NoError(t, err)
	assert.True(t, removed)

	// 长度应为 0
	len, err := q.Length(ctx, "q1")
	require.NoError(t, err)
	assert.Equal(t, int64(0), len)

	// 再次取消返回 false
	removed, err = q.Cancel(ctx, "q1", "k1")
	require.NoError(t, err)
	assert.False(t, removed)
}

// TestDelayQueue_GetTaskNotFound 测试获取不存在的任务
func TestDelayQueue_GetTaskNotFound(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{Namespace: "t"})
	defer q.Stop()

	ctx := context.Background()
	_, err := q.GetTask(ctx, "q1", "not-exist")
	assert.ErrorIs(t, err, ErrDelayTaskNotFound)
}

// TestDelayQueue_InvalidTask 测试无效任务校验
func TestDelayQueue_InvalidTask(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{Namespace: "t"})
	defer q.Stop()

	ctx := context.Background()

	// nil 任务
	err := q.EnqueueAt(ctx, "q1", nil)
	assert.ErrorIs(t, err, ErrInvalidDelayTask)

	// 空 Key
	err = q.EnqueueAt(ctx, "q1", &DelayTask[string]{Key: "", Data: "v", ExecuteAt: time.Now()})
	assert.ErrorIs(t, err, ErrInvalidDelayTask)

	// 零值 ExecuteAt
	err = q.EnqueueAt(ctx, "q1", &DelayTask[string]{Key: "k", Data: "v", ExecuteAt: time.Time{}})
	assert.ErrorIs(t, err, ErrInvalidDelayTask)
}

// TestDelayQueue_DequeueExpired 测试到期出队（Lua 原子）
func TestDelayQueue_DequeueExpired(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{Namespace: "t", BatchSize: 10})
	defer q.Stop()

	ctx := context.Background()

	// 入队 2 个未到期 + 1 个已到期
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "future1", Data: "f1", ExecuteAt: time.Now().Add(time.Hour),
	}))
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "future2", Data: "f2", ExecuteAt: time.Now().Add(time.Hour),
	}))
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "due1", Data: "d1", ExecuteAt: time.Now().Add(-time.Second), // 已到期
	}))

	// 出队：应只拿到 1 个到期任务
	tasks, err := q.dequeueExpired(ctx, "q1")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "due1", tasks[0].Key)

	// 队列剩余 2 个未到期
	len, err := q.Length(ctx, "q1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), len)
}

// TestDelayQueue_ConsumerBasic 测试消费者基础流程
func TestDelayQueue_ConsumerBasic(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[int](client, DelayQueueConfig{
		Namespace:         "t",
		PollInterval:      50 * time.Millisecond,
		BatchSize:         10,
		Concurrency:       2,
		MaxRetries:        2,
		RetryDelay:        100 * time.Millisecond,
		VisibilityTimeout: time.Minute,
	})
	defer q.Stop()

	ctx := context.Background()

	var processed atomic.Int32
	var mu sync.Mutex
	processedKeys := make(map[int]bool)

	require.NoError(t, q.StartConsumer(ctx, "q1", func(ctx context.Context, task *DelayTask[int]) error {
		mu.Lock()
		processedKeys[task.Data] = true
		mu.Unlock()
		processed.Add(1)
		return nil
	}))

	// 入队 3 个立即到期的任务
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[int]{Key: "k1", Data: 1, ExecuteAt: time.Now().Add(100 * time.Millisecond)}))
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[int]{Key: "k2", Data: 2, ExecuteAt: time.Now().Add(100 * time.Millisecond)}))
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[int]{Key: "k3", Data: 3, ExecuteAt: time.Now().Add(100 * time.Millisecond)}))

	// 等待消费完成
	require.Eventually(t, func() bool {
		return processed.Load() == 3
	}, 3*time.Second, 50*time.Millisecond)

	mu.Lock()
	assert.True(t, processedKeys[1])
	assert.True(t, processedKeys[2])
	assert.True(t, processedKeys[3])
	mu.Unlock()
}

// TestDelayQueue_ConsumerRetryAndDead 测试失败重试与死信队列
func TestDelayQueue_ConsumerRetryAndDead(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{
		Namespace:         "t",
		PollInterval:      30 * time.Millisecond,
		BatchSize:         5,
		Concurrency:       1,
		MaxRetries:        2, // 最多重试 2 次
		RetryDelay:        50 * time.Millisecond,
		VisibilityTimeout: time.Minute,
	})
	defer q.Stop()

	ctx := context.Background()

	var attempts atomic.Int32
	require.NoError(t, q.StartConsumer(ctx, "q1", func(ctx context.Context, task *DelayTask[string]) error {
		attempts.Add(1)
		return errors.New("always fail")
	}))

	// 入队 1 个任务
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "fail", Data: "v", ExecuteAt: time.Now().Add(50 * time.Millisecond),
	}))

	// 等待进入死信队列（首次 + 2 次重试 = 3 次尝试）
	require.Eventually(t, func() bool {
		return attempts.Load() >= 3
	}, 5*time.Second, 50*time.Millisecond)

	// 等待死信写入
	require.Eventually(t, func() bool {
		n, _ := q.DeadLength(ctx, "q1")
		return n >= 1
	}, 3*time.Second, 50*time.Millisecond)

	deadTasks, err := q.GetDeadTasks(ctx, "q1", 0, 10)
	require.NoError(t, err)
	require.Len(t, deadTasks, 1)
	assert.Equal(t, "fail", deadTasks[0].Key)
	assert.Equal(t, 2, deadTasks[0].RetryCount) // 最后一次重试后 retry=2
}

// TestDelayQueue_StopConsumer 测试停止单个消费者
func TestDelayQueue_StopConsumer(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{
		Namespace:    "t",
		PollInterval: 50 * time.Millisecond,
	})
	defer q.Stop()

	ctx := context.Background()

	var processed atomic.Int32
	require.NoError(t, q.StartConsumer(ctx, "q1", func(ctx context.Context, task *DelayTask[string]) error {
		processed.Add(1)
		return nil
	}))

	// 停止消费者
	q.StopConsumer("q1")

	// 入队任务，不应被处理
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "k1", Data: "v", ExecuteAt: time.Now().Add(50 * time.Millisecond),
	}))

	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(0), processed.Load())

	// 重复停止不应 panic
	q.StopConsumer("q1")
}

// TestDelayQueue_DuplicateConsumer 测试重复启动消费者
func TestDelayQueue_DuplicateConsumer(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{Namespace: "t"})
	defer q.Stop()

	ctx := context.Background()

	require.NoError(t, q.StartConsumer(ctx, "q1", func(ctx context.Context, task *DelayTask[string]) error {
		return nil
	}))
	defer q.StopConsumer("q1")

	// 重复启动应报错
	err := q.StartConsumer(ctx, "q1", func(ctx context.Context, task *DelayTask[string]) error {
		return nil
	})
	assert.ErrorIs(t, err, ErrDelayQueueRunning)
}

// TestDelayQueue_EnqueueWithDelay 测试延迟入队
func TestDelayQueue_EnqueueWithDelay(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{Namespace: "t"})
	defer q.Stop()

	ctx := context.Background()

	before := time.Now()
	require.NoError(t, q.EnqueueWithDelay(ctx, "q1", &DelayTask[string]{
		Key: "k1", Data: "v1",
	}, 200*time.Millisecond))

	task, err := q.GetTask(ctx, "q1", "k1")
	require.NoError(t, err)
	// ExecuteAt 应大约等于 before + 200ms
	assert.Greater(t, task.ExecuteAt.UnixMilli(), before.UnixMilli()+150)
	assert.Less(t, task.ExecuteAt.UnixMilli(), before.UnixMilli()+300)
}

// ========== ACK 机制场景测试 ==========

// TestDelayQueue_ACKSuccess 测试正常 ACK：处理成功后任务从 processing 和 hash 删除
func TestDelayQueue_ACKSuccess(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{
		Namespace:         "t",
		VisibilityTimeout: time.Minute,
	})
	defer q.Stop()

	ctx := context.Background()

	// 入队到期任务
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "k1", Data: "v1", ExecuteAt: time.Now().Add(-time.Second),
	}))

	// 出队（进入 processing，Hash 保留）
	tasks, err := q.dequeueExpired(ctx, "q1")
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	// processing 有 1 个，ready 为 0，Hash 仍有数据
	procLen, _ := q.ProcessingLength(ctx, "q1")
	assert.Equal(t, int64(1), procLen)
	readyLen, _ := q.ReadyLength(ctx, "q1")
	assert.Equal(t, int64(0), readyLen)
	_, err = q.GetTask(ctx, "q1", "k1")
	assert.NoError(t, err) // Hash 仍有

	// ACK 确认完成
	require.NoError(t, q.ack(ctx, "q1", "k1"))

	// processing 和 Hash 都清空
	procLen, _ = q.ProcessingLength(ctx, "q1")
	assert.Equal(t, int64(0), procLen)
	_, err = q.GetTask(ctx, "q1", "k1")
	assert.ErrorIs(t, err, ErrDelayTaskNotFound)
}

// TestDelayQueue_CrashRecovery 测试 Pod 崩溃恢复（ACK 机制核心场景）
// 模拟：Pod A 出队任务后崩溃（未 ACK）→ 可见性超时后 Pod B 通过 requeueStale 接管
func TestDelayQueue_CrashRecovery(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{
		Namespace:         "t",
		VisibilityTimeout: 200 * time.Millisecond, // 短超时便于测试
	})
	defer q.Stop()

	ctx := context.Background()

	// 1. Pod A 入队到期任务
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "crash", Data: "v", ExecuteAt: time.Now().Add(-time.Second),
	}))

	// 2. Pod A 出队（任务进入 processing）
	tasks, err := q.dequeueExpired(ctx, "q1")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "crash", tasks[0].Key)

	// 3. Pod A 崩溃：不调用 ACK，任务卡在 processing
	readyLen, _ := q.ReadyLength(ctx, "q1")
	assert.Equal(t, int64(0), readyLen)
	procLen, _ := q.ProcessingLength(ctx, "q1")
	assert.Equal(t, int64(1), procLen)

	// 4. 等待可见性超时
	time.Sleep(300 * time.Millisecond)

	// 5. Pod B 执行 requeueStale 回收
	count, err := q.requeueStale(ctx, "q1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// 6. 任务回到 ready，processing 清空
	procLen, _ = q.ProcessingLength(ctx, "q1")
	assert.Equal(t, int64(0), procLen)
	readyLen, _ = q.ReadyLength(ctx, "q1")
	assert.Equal(t, int64(1), readyLen)

	// 7. Pod B 重新出队消费（接管成功）
	tasks2, err := q.dequeueExpired(ctx, "q1")
	require.NoError(t, err)
	require.Len(t, tasks2, 1)
	assert.Equal(t, "crash", tasks2[0].Key)

	// 8. Pod B ACK 完成
	require.NoError(t, q.ack(ctx, "q1", "crash"))
	procLen, _ = q.ProcessingLength(ctx, "q1")
	assert.Equal(t, int64(0), procLen)
}

// TestDelayQueue_RequeueStaleNoOp 测试无超时任务时 requeueStale 返回 0
func TestDelayQueue_RequeueStaleNoOp(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{
		Namespace:         "t",
		VisibilityTimeout: time.Minute,
	})
	defer q.Stop()

	ctx := context.Background()

	// 入队 + 出队进入 processing（未超时）
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "k1", Data: "v", ExecuteAt: time.Now().Add(-time.Second),
	}))
	_, _ = q.dequeueExpired(ctx, "q1")

	// 未超时，requeueStale 不应回收
	count, err := q.requeueStale(ctx, "q1")
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	procLen, _ := q.ProcessingLength(ctx, "q1")
	assert.Equal(t, int64(1), procLen) // 仍在 processing
}

// TestDelayQueue_CancelProcessing 测试取消 processing 中的任务
func TestDelayQueue_CancelProcessing(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{
		Namespace:         "t",
		VisibilityTimeout: time.Minute,
	})
	defer q.Stop()

	ctx := context.Background()

	// 入队 + 出队进入 processing
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "k1", Data: "v", ExecuteAt: time.Now().Add(-time.Second),
	}))
	_, err := q.dequeueExpired(ctx, "q1")
	require.NoError(t, err)

	// Cancel 应能删除 processing 中的任务
	removed, err := q.Cancel(ctx, "q1", "k1")
	require.NoError(t, err)
	assert.True(t, removed)

	// processing 和 hash 都清空
	procLen, _ := q.ProcessingLength(ctx, "q1")
	assert.Equal(t, int64(0), procLen)
	_, err = q.GetTask(ctx, "q1", "k1")
	assert.ErrorIs(t, err, ErrDelayTaskNotFound)
}

// TestDelayQueue_OrderedMode 测试顺序模式：任务按到期时间串行处理
func TestDelayQueue_OrderedMode(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[int](client, DelayQueueConfig{
		Namespace:    "t",
		PollInterval: 30 * time.Millisecond,
		BatchSize:    10,
		Ordered:      true, // 顺序模式
	})
	defer q.Stop()

	ctx := context.Background()

	// 记录处理顺序
	var mu sync.Mutex
	order := make([]int, 0)

	require.NoError(t, q.StartConsumer(ctx, "q1", func(ctx context.Context, task *DelayTask[int]) error {
		mu.Lock()
		order = append(order, task.Data)
		mu.Unlock()
		// 模拟处理耗时，验证不会并发
		time.Sleep(20 * time.Millisecond)
		return nil
	}))

	// 入队 3 个任务，到期时间依次递增（都已到期，score 决定顺序）
	now := time.Now()
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[int]{Key: "a", Data: 1, ExecuteAt: now.Add(-3 * time.Second)}))
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[int]{Key: "b", Data: 2, ExecuteAt: now.Add(-2 * time.Second)}))
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[int]{Key: "c", Data: 3, ExecuteAt: now.Add(-1 * time.Second)}))

	// 等待全部处理完成
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) == 3
	}, 3*time.Second, 30*time.Millisecond)

	// 验证按到期时间升序处理（1 → 2 → 3）
	mu.Lock()
	assert.Equal(t, []int{1, 2, 3}, order)
	mu.Unlock()
}

// TestDelayQueue_AckIdempotent 测试 ACK 幂等：对已 ACK 的任务再次 ACK 不报错
func TestDelayQueue_AckIdempotent(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{
		Namespace:         "t",
		VisibilityTimeout: time.Minute,
	})
	defer q.Stop()

	ctx := context.Background()

	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{
		Key: "k1", Data: "v", ExecuteAt: time.Now().Add(-time.Second),
	}))
	_, _ = q.dequeueExpired(ctx, "q1")

	// 第一次 ACK 成功
	require.NoError(t, q.ack(ctx, "q1", "k1"))
	// 第二次 ACK 幂等（任务已不在 processing，不报错）
	require.NoError(t, q.ack(ctx, "q1", "k1"))
}

// TestDelayQueue_LengthQueries 测试 ready/processing/dead 长度查询
func TestDelayQueue_LengthQueries(t *testing.T) {
	client, _ := newDelayQueueClient(t)
	defer client.Close()

	q := NewDelayQueue[string](client, DelayQueueConfig{
		Namespace:         "t",
		VisibilityTimeout: time.Minute,
	})
	defer q.Stop()

	ctx := context.Background()

	// 入队 2 个到期 + 1 个未到期
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{Key: "d1", Data: "v", ExecuteAt: time.Now().Add(-time.Second)}))
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{Key: "d2", Data: "v", ExecuteAt: time.Now().Add(-time.Second)}))
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[string]{Key: "f1", Data: "v", ExecuteAt: time.Now().Add(time.Hour)}))

	readyLen, _ := q.ReadyLength(ctx, "q1")
	assert.Equal(t, int64(3), readyLen)

	// 出队 2 个到期任务进入 processing
	tasks, err := q.dequeueExpired(ctx, "q1")
	require.NoError(t, err)
	require.Len(t, tasks, 2)

	readyLen, _ = q.ReadyLength(ctx, "q1")
	assert.Equal(t, int64(1), readyLen) // 剩 1 个未到期
	procLen, _ := q.ProcessingLength(ctx, "q1")
	assert.Equal(t, int64(2), procLen) // 2 个处理中
	deadLen, _ := q.DeadLength(ctx, "q1")
	assert.Equal(t, int64(0), deadLen)
}

// ========== 多消费者分布式场景测试 ==========

// TestDelayQueue_MultiConsumerNoDuplicate 多消费者并发不重复消费（分布式核心保证）
// 模拟 3 个 Pod 共享同一 Redis，50 个任务，每个任务只被处理一次
func TestDelayQueue_MultiConsumerNoDuplicate(t *testing.T) {
	client, mr := newDelayQueueClient(t)
	defer client.Close()
	defer mr.Close()

	ctx := context.Background()
	const totalTasks = 50

	// 入队 50 个到期任务
	seedQ := NewDelayQueue[int](client, DelayQueueConfig{Namespace: "t"})
	for i := 0; i < totalTasks; i++ {
		require.NoError(t, seedQ.EnqueueAt(ctx, "q1", &DelayTask[int]{
			Key: fmt.Sprintf("k%d", i), Data: i, ExecuteAt: time.Now().Add(-time.Second),
		}))
	}

	// 记录处理情况（data → 处理次数）
	var mu sync.Mutex
	processed := make(map[int]int, totalTasks)

	// 3 个 DelayQueue 实例（模拟 3 Pod）共享同一 Redis，各自 StartConsumer 并发消费
	consumers := make([]*DelayQueue[int], 3)
	for i := range consumers {
		consumers[i] = NewDelayQueue[int](client, DelayQueueConfig{
			Namespace:    "t",
			PollInterval: 10 * time.Millisecond,
			BatchSize:    5,
			Concurrency:  2,
		})
		require.NoError(t, consumers[i].StartConsumer(ctx, "q1", func(ctx context.Context, task *DelayTask[int]) error {
			mu.Lock()
			processed[task.Data]++
			mu.Unlock()
			return nil
		}))
	}
	defer func() {
		for _, c := range consumers {
			c.Stop()
		}
	}()

	// 等待全部处理完
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(processed) == totalTasks
	}, 5*time.Second, 20*time.Millisecond)

	// 验证每个任务只处理一次（ACK 机制 + Lua 原子出队保证）
	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, processed, totalTasks)
	for data, times := range processed {
		assert.Equal(t, 1, times, "task %d 被处理了 %d 次（应只处理 1 次）", data, times)
	}
}

// TestDelayQueue_MultiConsumerCrashRecovery 多消费者崩溃恢复
// 消费者 A 出队后"崩溃"（不 ACK），消费者 B 通过 pollExpired 自动回收并接管
func TestDelayQueue_MultiConsumerCrashRecovery(t *testing.T) {
	client, mr := newDelayQueueClient(t)
	defer client.Close()
	defer mr.Close()

	ctx := context.Background()

	q := NewDelayQueue[int](client, DelayQueueConfig{
		Namespace:         "t",
		VisibilityTimeout: 200 * time.Millisecond,
	})
	defer q.Stop()

	// 1. 入队到期任务
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[int]{
		Key: "k1", Data: 1, ExecuteAt: time.Now().Add(-time.Second),
	}))

	// 2. 消费者 A 出队（进入 processing），不 ACK（模拟崩溃）
	tasks, err := q.dequeueExpired(ctx, "q1")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "k1", tasks[0].Key)

	// 3. 消费者 B 用 pollExpired 轮询：未超时，不应拿到任务
	tasks2, err := q.pollExpired(ctx, "q1")
	require.NoError(t, err)
	assert.Empty(t, tasks2, "未超时的 processing 任务不应被回收")

	// 4. 等待可见性超时
	time.Sleep(300 * time.Millisecond)

	// 5. 消费者 B 再次 pollExpired：回收超时任务 + 拉取（单次 Lua 完成）
	tasks3, err := q.pollExpired(ctx, "q1")
	require.NoError(t, err)
	require.Len(t, tasks3, 1, "超时后应被回收并重新拉取")
	assert.Equal(t, "k1", tasks3[0].Key)

	// 6. 消费者 B ACK 完成
	require.NoError(t, q.ack(ctx, "q1", "k1"))

	procLen, _ := q.ProcessingLength(ctx, "q1")
	assert.Equal(t, int64(0), procLen)
	readyLen, _ := q.ReadyLength(ctx, "q1")
	assert.Equal(t, int64(0), readyLen)
}

// TestDelayQueue_PollExpiredRequeueAndDequeue 验证 pollExpired 单次 Lua 同时完成回收+拉取
// 场景：processing 有超时任务 b（未 ACK），ready 有到期任务 c，pollExpired 一次返回 b,c
func TestDelayQueue_PollExpiredRequeueAndDequeue(t *testing.T) {
	client, mr := newDelayQueueClient(t)
	defer client.Close()
	defer mr.Close()

	ctx := context.Background()

	q := NewDelayQueue[int](client, DelayQueueConfig{
		Namespace:         "t",
		VisibilityTimeout: 200 * time.Millisecond,
	})
	defer q.Stop()

	// 1. 入队 a, b（到期）
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[int]{Key: "a", Data: 1, ExecuteAt: time.Now().Add(-time.Second)}))
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[int]{Key: "b", Data: 2, ExecuteAt: time.Now().Add(-time.Second)}))

	// 2. 出队 a, b 进入 processing
	tasks, err := q.dequeueExpired(ctx, "q1")
	require.NoError(t, err)
	require.Len(t, tasks, 2)

	// 3. ACK a（完成），不 ACK b（b 模拟崩溃）
	require.NoError(t, q.ack(ctx, "q1", "a"))

	// 4. 入队 c（到期）
	require.NoError(t, q.EnqueueAt(ctx, "q1", &DelayTask[int]{Key: "c", Data: 3, ExecuteAt: time.Now().Add(-time.Second)}))

	// 5. 等待 b 超时
	time.Sleep(300 * time.Millisecond)

	// 6. pollExpired 单次调用：回收 b（processing→ready）+ 拉取 b,c（ready→processing）
	tasks2, err := q.pollExpired(ctx, "q1")
	require.NoError(t, err)
	require.Len(t, tasks2, 2, "应同时回收 b 和拉取 c")

	keys := map[string]bool{}
	for _, task := range tasks2 {
		keys[task.Key] = true
	}
	assert.True(t, keys["b"], "超时任务 b 应被回收")
	assert.True(t, keys["c"], "到期任务 c 应被拉取")
	assert.False(t, keys["a"], "已 ACK 的 a 不应出现")

	// 7. ACK b, c 完成
	require.NoError(t, q.ack(ctx, "q1", "b"))
	require.NoError(t, q.ack(ctx, "q1", "c"))
	procLen, _ := q.ProcessingLength(ctx, "q1")
	assert.Equal(t, int64(0), procLen)
}

// TestDelayQueue_MultiConsumerThroughput 多消费者吞吐量基准
// 对比：单消费者 vs 多消费者处理 200 个任务的总耗时（验证多消费者加速效果）
func TestDelayQueue_MultiConsumerThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过吞吐量基准测试")
	}

	runWith := func(t *testing.T, consumerCount, concurrency int) time.Duration {
		client, mr := newDelayQueueClient(t)
		defer client.Close()
		defer mr.Close()

		ctx := context.Background()
		const totalTasks = 200

		// 入队
		seedQ := NewDelayQueue[int](client, DelayQueueConfig{Namespace: "t"})
		for i := 0; i < totalTasks; i++ {
			require.NoError(t, seedQ.EnqueueAt(ctx, "q1", &DelayTask[int]{
				Key: fmt.Sprintf("k%d", i), Data: i, ExecuteAt: time.Now().Add(-time.Second),
			}))
		}

		var count atomic.Int64
		consumers := make([]*DelayQueue[int], consumerCount)
		for i := range consumers {
			consumers[i] = NewDelayQueue[int](client, DelayQueueConfig{
				Namespace:    "t",
				PollInterval: 5 * time.Millisecond,
				BatchSize:    10,
				Concurrency:  concurrency,
			})
			require.NoError(t, consumers[i].StartConsumer(ctx, "q1", func(ctx context.Context, task *DelayTask[int]) error {
				// 模拟轻量处理
				count.Add(1)
				return nil
			}))
		}

		start := time.Now()
		require.Eventually(t, func() bool {
			return count.Load() == totalTasks
		}, 10*time.Second, 10*time.Millisecond)
		elapsed := time.Since(start)

		for _, c := range consumers {
			c.Stop()
		}
		return elapsed
	}

	// 单消费者（1 实例 × 并发 1）
	singleElapsed := runWith(t, 1, 1)
	t.Logf("单消费者 200 任务耗时: %v", singleElapsed)

	// 多消费者（3 实例 × 并发 3）
	multiElapsed := runWith(t, 3, 3)
	t.Logf("多消费者(3×3) 200 任务耗时: %v", multiElapsed)

	// 多消费者应不慢于单消费者（miniredis 单线程，主要验证正确性而非真实加速比）
	assert.LessOrEqual(t, multiElapsed, singleElapsed*2, "多消费者不应显著慢于单消费者")
}
