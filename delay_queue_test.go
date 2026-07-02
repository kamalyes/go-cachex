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
		Namespace:    "t",
		PollInterval: 50 * time.Millisecond,
		BatchSize:    10,
		Concurrency:  2,
		MaxRetries:   2,
		RetryDelay:   100 * time.Millisecond,
		LockTTL:      time.Minute,
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
		Namespace:    "t",
		PollInterval: 50 * time.Millisecond,
		BatchSize:    10,
		Concurrency:  2,
		MaxRetries:   2,
		RetryDelay:   100 * time.Millisecond,
		LockTTL:      time.Minute,
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
		Namespace:    "t",
		PollInterval: 30 * time.Millisecond,
		BatchSize:    5,
		Concurrency:  1,
		MaxRetries:   2, // 最多重试 2 次
		RetryDelay:   50 * time.Millisecond,
		LockTTL:      time.Minute,
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
