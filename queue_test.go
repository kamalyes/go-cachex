/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-19 23:52:55
 * @FilePath: \go-cachex\queue_test.go
 * @Description: 队列功能测试
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failNthCmdHook 在第 N 条命令（1-based）执行时注入错误，其余正常转发
type failNthCmdHook struct {
	counter int32
	failAt  int32
	err     error
}

func (h *failNthCmdHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *failNthCmdHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if atomic.AddInt32(&h.counter, 1) == h.failAt {
			return h.err
		}
		return next(ctx, cmd)
	}
}
func (h *failNthCmdHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// shortBLPopResultHook 使 BLPop 返回长度为 1 的切片，触发 len(result) < 2 分支
type shortBLPopResultHook struct{}

func (h *shortBLPopResultHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *shortBLPopResultHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if err := next(ctx, cmd); err != nil {
			return err
		}
		if ss, ok := cmd.(*redis.StringSliceCmd); ok && cmd.Name() == "blpop" {
			ss.SetVal([]string{"only_one"})
		}
		return nil
	}
}
func (h *shortBLPopResultHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// evalResultHook 替换 EVAL 命令的返回值
type evalResultHook struct {
	result interface{}
}

func (h *evalResultHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *evalResultHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if c, ok := cmd.(*redis.Cmd); ok && cmd.Name() == "eval" {
			c.SetVal(h.result)
			return nil
		}
		return next(ctx, cmd)
	}
}
func (h *evalResultHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// cmdErrorHook 使指定名称的命令返回注入的错误
type cmdErrorHook struct {
	cmdName string
	err     error
}

func (h *cmdErrorHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *cmdErrorHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == h.cmdName {
			return h.err
		}
		return next(ctx, cmd)
	}
}
func (h *cmdErrorHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// setupRedisClient 创建基于 miniredis 的本地内存 Redis 客户端，供测试离线运行
// 每次调用启动一个独立 miniredis 实例，通过 tb.Cleanup 自动关闭，无需外部 Redis 服务
// miniredis 使用虚拟时钟，TTL 不会随真实时间自动过期，因此启动后台 goroutine
// 周期性调用 FastForward 同步虚拟时钟，使 TTL 行为与真实 Redis 一致
func setupRedisClient(tb testing.TB) *redis.Client {
	mr := miniredis.RunT(tb)

	// 后台同步虚拟时钟：每 50ms 推进 miniredis 时钟，使 TTL 过期与真实时间一致
	clockDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-clockDone:
				return
			case <-ticker.C:
				mr.FastForward(50 * time.Millisecond)
			}
		}
	}()
	tb.Cleanup(func() { close(clockDone) })

	client := redis.NewClient(&redis.Options{
		Addr:            mr.Addr(),
		DialTimeout:     3 * time.Second,
		ReadTimeout:     3 * time.Second,
		WriteTimeout:    3 * time.Second,
		PoolTimeout:     5 * time.Second,
		PoolSize:        10,
		DisableIdentity: true,
	})

	// 验证连接可用
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		tb.Fatalf("miniredis 连接失败: %v", err)
	}

	return client
}

func TestQueueHandler_FIFO(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	config := QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second,
		BatchSize:       10,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := NewQueueHandler(client, "test", config)
	ctx := context.Background()
	queueName := "test_fifo"

	// 测试入队
	items := []*QueueItem{
		{Data: "第一个任务"},
		{Data: "第二个任务"},
		{Data: "第三个任务"},
	}

	for i, item := range items {
		err := queue.Enqueue(ctx, queueName, QueueTypeFIFO, item)
		assert.NoError(t, err, "入队第%d个任务失败", i+1)
		assert.NotEmpty(t, item.ID, "任务ID应该被自动生成")
		assert.NotZero(t, item.CreatedAt, "创建时间应该被设置")
	}

	// 测试队列长度
	length, err := queue.Length(ctx, queueName, QueueTypeFIFO)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), length, "队列长度应该是3")

	// 测试出队（FIFO：先进先出）
	expectedOrder := []string{"第一个任务", "第二个任务", "第三个任务"}
	for i, expected := range expectedOrder {
		item, err := queue.Dequeue(ctx, queueName, QueueTypeFIFO)
		assert.NoError(t, err, "出队第%d个任务失败", i+1)
		require.NotNil(t, item, "出队的任务不应为空")
		assert.Equal(t, expected, item.Data, "任务顺序不正确")
	}

	// 队列应该为空
	length, err = queue.Length(ctx, queueName, QueueTypeFIFO)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), length, "队列应该为空")

	// 空队列出队应该返回nil
	item, err := queue.Dequeue(ctx, queueName, QueueTypeFIFO)
	assert.NoError(t, err)
	assert.Nil(t, item, "空队列出队应该返回nil")
}

func TestQueueHandler_Priority(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	config := QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second,
		BatchSize:       10,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := NewQueueHandler(client, "test", config)
	ctx := context.Background()
	queueName := "test_priority"

	// 测试优先级队列入队（故意乱序入队）
	items := []*QueueItem{
		{Data: "低优先级任务", Priority: 1.0},
		{Data: "高优先级任务", Priority: 10.0},
		{Data: "中优先级任务", Priority: 5.0},
	}

	for _, item := range items {
		err := queue.Enqueue(ctx, queueName, QueueTypePriority, item)
		assert.NoError(t, err, "优先级队列入队失败")
	}

	// 测试队列长度
	length, err := queue.Length(ctx, queueName, QueueTypePriority)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), length, "优先级队列长度应该是3")

	// 测试出队（应该按优先级从高到低）
	expectedOrder := []string{"高优先级任务", "中优先级任务", "低优先级任务"}
	for _, expected := range expectedOrder {
		item, err := queue.Dequeue(ctx, queueName, QueueTypePriority)
		assert.NoError(t, err, "优先级队列出队失败")
		require.NotNil(t, item, "出队的任务不应为空")
		assert.Equal(t, expected, item.Data, "优先级顺序不正确，期望：%s，实际：%s", expected, item.Data)
	}
}

func TestQueueHandler_Delayed(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	config := QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second,
		BatchSize:       10,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := NewQueueHandler(client, "test", config)
	ctx := context.Background()
	queueName := "test_delayed"

	// 测试延时队列
	items := []*QueueItem{
		{Data: "立即执行任务", DelayTime: 0}, // 立即执行
		{Data: "延时5秒任务", DelayTime: 5}, // 5秒后执行
		{Data: "延时2秒任务", DelayTime: 2}, // 2秒后执行
	}

	for _, item := range items {
		err := queue.Enqueue(ctx, queueName, QueueTypeDelayed, item)
		assert.NoError(t, err, "延时队列入队失败")
	}

	// 立即尝试获取任务（应该只能获取到立即执行的任务）
	item, err := queue.Dequeue(ctx, queueName, QueueTypeDelayed)
	assert.NoError(t, err)
	assert.NotNil(t, item, "应该能获取到立即执行的任务")
	assert.Equal(t, "立即执行任务", item.Data)

	// 等待2.5秒后再次获取（应该能获取到2秒延时的任务）
	time.Sleep(time.Millisecond * 2500)
	item, err = queue.Dequeue(ctx, queueName, QueueTypeDelayed)
	assert.NoError(t, err)
	assert.NotNil(t, item, "应该能获取到2秒延时的任务")
	assert.Equal(t, "延时2秒任务", item.Data)

	// 现在不应该有可用的任务（5秒任务还需要至少2秒才能到期）
	item, err = queue.Dequeue(ctx, queueName, QueueTypeDelayed)
	assert.NoError(t, err)
	assert.Nil(t, item, "5秒任务还没到时间，不应该有可用任务")

	// 清理测试数据
	queue.Clear(ctx, queueName, QueueTypeDelayed)
}

func TestQueueHandler_BatchOperations(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	config := QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second,
		BatchSize:       3,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := NewQueueHandler(client, "test", config)
	ctx := context.Background()
	queueName := "test_batch"

	// 批量入队
	for i := 1; i <= 5; i++ {
		item := &QueueItem{Data: fmt.Sprintf("任务%d", i)}
		err := queue.Enqueue(ctx, queueName, QueueTypeFIFO, item)
		assert.NoError(t, err)
	}

	// 批量出队
	items, err := queue.BatchDequeue(ctx, queueName, QueueTypeFIFO, 3)
	assert.NoError(t, err)
	assert.Len(t, items, 3, "应该获取到3个任务")

	// 检查任务顺序
	for i, item := range items {
		expected := fmt.Sprintf("任务%d", i+1)
		assert.Equal(t, expected, item.Data)
	}

	// 再次批量出队（应该获取到剩余的2个任务）
	items, err = queue.BatchDequeue(ctx, queueName, QueueTypeFIFO, 5)
	assert.NoError(t, err)
	assert.Len(t, items, 2, "应该获取到剩余的2个任务")
}

func TestQueueHandler_Peek(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	config := QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second,
		BatchSize:       10,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := NewQueueHandler(client, "test", config)
	ctx := context.Background()

	t.Run("FIFO Peek基本功能", func(t *testing.T) {
		queueName := "test_peek_fifo"

		// 入队任务
		for i := 1; i <= 5; i++ {
			item := &QueueItem{Data: fmt.Sprintf("FIFO任务%d", i)}
			err := queue.Enqueue(ctx, queueName, QueueTypeFIFO, item)
			assert.NoError(t, err)
		}

		// Peek前2个任务（应该是最先入队的）
		items, err := queue.Peek(ctx, queueName, QueueTypeFIFO, 2)
		assert.NoError(t, err)
		assert.Len(t, items, 2, "应该返回2个任务")
		assert.Equal(t, "FIFO任务1", items[0].Data, "第一个应该是任务1")
		assert.Equal(t, "FIFO任务2", items[1].Data, "第二个应该是任务2")

		// 队列长度不变
		length, err := queue.Length(ctx, queueName, QueueTypeFIFO)
		assert.NoError(t, err)
		assert.Equal(t, int64(5), length, "Peek不应改变队列长度")

		// 出队一个后再Peek
		dequeued, err := queue.Dequeue(ctx, queueName, QueueTypeFIFO)
		assert.NoError(t, err)
		assert.Equal(t, "FIFO任务1", dequeued.Data, "应该出队任务1")

		items, err = queue.Peek(ctx, queueName, QueueTypeFIFO, 2)
		assert.NoError(t, err)
		assert.Len(t, items, 2)
		assert.Equal(t, "FIFO任务2", items[0].Data, "现在第一个应该是任务2")
		assert.Equal(t, "FIFO任务3", items[1].Data, "现在第二个应该是任务3")

		queue.Clear(ctx, queueName, QueueTypeFIFO)
	})

	t.Run("LIFO Peek基本功能", func(t *testing.T) {
		queueName := "test_peek_lifo"

		// 入队任务
		for i := 1; i <= 5; i++ {
			item := &QueueItem{Data: fmt.Sprintf("LIFO任务%d", i)}
			err := queue.Enqueue(ctx, queueName, QueueTypeLIFO, item)
			assert.NoError(t, err)
		}

		// Peek前2个任务（应该是最后入队的）
		items, err := queue.Peek(ctx, queueName, QueueTypeLIFO, 2)
		assert.NoError(t, err)
		assert.Len(t, items, 2, "应该返回2个任务")
		assert.Equal(t, "LIFO任务5", items[0].Data, "LIFO第一个应该是最后入队的任务5")
		assert.Equal(t, "LIFO任务4", items[1].Data, "LIFO第二个应该是任务4")

		// 队列长度不变
		length, err := queue.Length(ctx, queueName, QueueTypeLIFO)
		assert.NoError(t, err)
		assert.Equal(t, int64(5), length, "Peek不应改变队列长度")

		// 出队一个后再Peek
		dequeued, err := queue.Dequeue(ctx, queueName, QueueTypeLIFO)
		assert.NoError(t, err)
		assert.Equal(t, "LIFO任务5", dequeued.Data, "LIFO应该出队最后入队的任务5")

		items, err = queue.Peek(ctx, queueName, QueueTypeLIFO, 2)
		assert.NoError(t, err)
		assert.Len(t, items, 2)
		assert.Equal(t, "LIFO任务4", items[0].Data, "LIFO现在第一个应该是任务4")
		assert.Equal(t, "LIFO任务3", items[1].Data, "LIFO现在第二个应该是任务3")

		queue.Clear(ctx, queueName, QueueTypeLIFO)
	})

	t.Run("Priority Peek优先级顺序", func(t *testing.T) {
		queueName := "test_peek_priority"

		// 乱序入队不同优先级的任务
		items := []*QueueItem{
			{Data: "低优先级", Priority: 1.0},
			{Data: "高优先级", Priority: 10.0},
			{Data: "中优先级", Priority: 5.0},
			{Data: "超高优先级", Priority: 20.0},
			{Data: "极低优先级", Priority: 0.5},
		}
		for _, item := range items {
			err := queue.Enqueue(ctx, queueName, QueueTypePriority, item)
			assert.NoError(t, err)
		}

		// Peek前3个（应该按优先级从高到低）
		peeked, err := queue.Peek(ctx, queueName, QueueTypePriority, 3)
		assert.NoError(t, err)
		assert.Len(t, peeked, 3)
		assert.Equal(t, "超高优先级", peeked[0].Data, "第一个应该是优先级最高的")
		assert.Equal(t, "高优先级", peeked[1].Data, "第二个应该是优先级第二的")
		assert.Equal(t, "中优先级", peeked[2].Data, "第三个应该是优先级第三的")

		queue.Clear(ctx, queueName, QueueTypePriority)
	})

	t.Run("Delayed Peek延时队列", func(t *testing.T) {
		queueName := "test_peek_delayed"

		// 入队不同延时的任务
		items := []*QueueItem{
			{Data: "延时5秒", DelayTime: 5},
			{Data: "立即执行", DelayTime: 0},
			{Data: "延时2秒", DelayTime: 2},
		}
		for _, item := range items {
			err := queue.Enqueue(ctx, queueName, QueueTypeDelayed, item)
			assert.NoError(t, err)
		}

		// Peek应该按执行时间排序
		peeked, err := queue.Peek(ctx, queueName, QueueTypeDelayed, 3)
		assert.NoError(t, err)
		assert.Len(t, peeked, 3)
		assert.Equal(t, "立即执行", peeked[0].Data, "第一个应该是延时最短的")

		queue.Clear(ctx, queueName, QueueTypeDelayed)
	})

	t.Run("Peek空队列", func(t *testing.T) {
		queueName := "test_peek_empty"

		items, err := queue.Peek(ctx, queueName, QueueTypeFIFO, 5)
		assert.NoError(t, err)
		assert.Empty(t, items, "空队列Peek应该返回空数组")
	})

	t.Run("Peek数量超过队列长度", func(t *testing.T) {
		queueName := "test_peek_overflow"

		// 只入队2个任务
		for i := 1; i <= 2; i++ {
			item := &QueueItem{Data: fmt.Sprintf("任务%d", i)}
			err := queue.Enqueue(ctx, queueName, QueueTypeFIFO, item)
			assert.NoError(t, err)
		}

		// 尝试Peek 10个
		items, err := queue.Peek(ctx, queueName, QueueTypeFIFO, 10)
		assert.NoError(t, err)
		assert.Len(t, items, 2, "应该只返回实际存在的2个任务")

		queue.Clear(ctx, queueName, QueueTypeFIFO)
	})

	t.Run("Peek单个任务", func(t *testing.T) {
		queueName := "test_peek_single"

		for i := 1; i <= 3; i++ {
			item := &QueueItem{Data: fmt.Sprintf("任务%d", i)}
			err := queue.Enqueue(ctx, queueName, QueueTypeFIFO, item)
			assert.NoError(t, err)
		}

		// Peek 1个任务
		items, err := queue.Peek(ctx, queueName, QueueTypeFIFO, 1)
		assert.NoError(t, err)
		assert.Len(t, items, 1)
		assert.Equal(t, "任务1", items[0].Data, "应该返回第一个任务")

		queue.Clear(ctx, queueName, QueueTypeFIFO)
	})

	t.Run("连续Peek保持一致性", func(t *testing.T) {
		queueName := "test_peek_consistency"

		for i := 1; i <= 4; i++ {
			item := &QueueItem{Data: fmt.Sprintf("任务%d", i)}
			err := queue.Enqueue(ctx, queueName, QueueTypeFIFO, item)
			assert.NoError(t, err)
		}

		// 多次Peek应该返回相同结果
		items1, err := queue.Peek(ctx, queueName, QueueTypeFIFO, 2)
		assert.NoError(t, err)

		items2, err := queue.Peek(ctx, queueName, QueueTypeFIFO, 2)
		assert.NoError(t, err)

		assert.Equal(t, items1[0].Data, items2[0].Data, "多次Peek应该返回相同的第一个任务")
		assert.Equal(t, items1[1].Data, items2[1].Data, "多次Peek应该返回相同的第二个任务")

		queue.Clear(ctx, queueName, QueueTypeFIFO)
	})
}

func TestQueueHandler_Contains(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	config := QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second,
		BatchSize:       10,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := NewQueueHandler(client, "test", config)
	ctx := context.Background()
	queueName := "test_contains"

	// 入队任务
	item := &QueueItem{
		ID:   "unique_task_id",
		Data: "测试任务",
	}
	err := queue.Enqueue(ctx, queueName, QueueTypeFIFO, item)
	assert.NoError(t, err)

	// 检查包含
	contains, err := queue.Contains(ctx, queueName, QueueTypeFIFO, "unique_task_id")
	assert.NoError(t, err)
	assert.True(t, contains, "队列应该包含指定的任务ID")

	// 检查不存在的ID
	contains, err = queue.Contains(ctx, queueName, QueueTypeFIFO, "non_existent_id")
	assert.NoError(t, err)
	assert.False(t, contains, "队列不应该包含不存在的任务ID")
}

func TestQueueHandler_RetryFailed(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	config := QueueConfig{
		MaxRetries:      2, // 设置最大重试次数为2
		RetryDelay:      time.Second,
		BatchSize:       10,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := NewQueueHandler(client, "test", config)
	ctx := context.Background()
	queueName := "test_retry"

	// 创建一个失败的任务
	item := &QueueItem{
		Data:       "失败任务",
		RetryCount: 1, // 已经重试过1次
	}

	// 重试任务（未达到最大重试次数）
	err := queue.RetryFailed(ctx, queueName, QueueTypeFIFO, item)
	assert.NoError(t, err)
	assert.Equal(t, 2, item.RetryCount, "重试次数应该增加")

	// 再次重试（达到最大重试次数，应该进入失败队列）
	err = queue.RetryFailed(ctx, queueName, QueueTypeFIFO, item)
	assert.NoError(t, err)

	// 检查失败队列
	failedItems, err := queue.GetFailedItems(ctx, queueName, 0, 10)
	assert.NoError(t, err)
	assert.Len(t, failedItems, 1, "失败队列应该有1个任务")
	assert.Equal(t, "失败任务", failedItems[0].Data)
}

func TestQueueHandler_Clear(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	config := QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second,
		BatchSize:       10,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := NewQueueHandler(client, "test", config)
	ctx := context.Background()
	queueName := "test_clear"

	// 入队几个任务
	for i := 1; i <= 3; i++ {
		item := &QueueItem{Data: fmt.Sprintf("任务%d", i)}
		err := queue.Enqueue(ctx, queueName, QueueTypeFIFO, item)
		assert.NoError(t, err)
	}

	// 确认队列有数据
	length, err := queue.Length(ctx, queueName, QueueTypeFIFO)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), length)

	// 清空队列
	err = queue.Clear(ctx, queueName, QueueTypeFIFO)
	assert.NoError(t, err)

	// 确认队列为空
	length, err = queue.Length(ctx, queueName, QueueTypeFIFO)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), length)
}

func TestQueueHandler_Lock(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	config := QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second,
		BatchSize:       10,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := NewQueueHandler(client, "test", config)
	ctx := context.Background()
	queueName := "test_lock"
	workerID1 := "worker1"
	workerID2 := "worker2"

	// 注意：由于现在队列默认不启用分布式锁，这些方法应该都返回true/nil
	// worker1获取锁
	acquired, err := queue.AcquireLock(ctx, queueName, workerID1)
	assert.NoError(t, err)
	assert.True(t, acquired, "未启用分布式锁时应该总是返回true")

	// worker2尝试获取同一个锁（在未启用锁的情况下也应该成功）
	acquired, err = queue.AcquireLock(ctx, queueName, workerID2)
	assert.NoError(t, err)
	assert.True(t, acquired, "未启用分布式锁时应该总是返回true")

	// worker1释放锁
	err = queue.ReleaseLock(ctx, queueName, workerID1)
	assert.NoError(t, err)

	// worker2释放锁
	err = queue.ReleaseLock(ctx, queueName, workerID2)
	assert.NoError(t, err)
}

func TestQueueHandler_Stats(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	config := QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second,
		BatchSize:       10,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := NewQueueHandler(client, "test", config)
	ctx := context.Background()
	queueName := "test_stats"

	// 入队一些任务
	for i := 1; i <= 5; i++ {
		item := &QueueItem{Data: fmt.Sprintf("任务%d", i)}
		err := queue.Enqueue(ctx, queueName, QueueTypeFIFO, item)
		assert.NoError(t, err)
	}

	// 获取统计信息
	stats, err := queue.GetStats(ctx, queueName, QueueTypeFIFO)
	assert.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Equal(t, queueName, stats.QueueName)
	assert.Equal(t, string(QueueTypeFIFO), stats.QueueType)
	assert.Equal(t, int64(5), stats.Length)
	assert.Equal(t, int64(0), stats.FailedCount) // 应该没有失败任务
}

// newTestQueue 创建基于 miniredis 的队列处理器（用于补充测试，独立于 setupRedisClient）
func newTestQueue(t *testing.T, namespace string, config QueueConfig) (*QueueHandler, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	queue := NewQueueHandler(client, namespace, config)
	return queue, client, mr
}

// TestQueueHandler_DequeueNonBlocking 验证非阻塞出队（覆盖 DequeueNonBlocking 与 dequeueWithTimeout 的 timeout=0 分支）
func TestQueueHandler_DequeueNonBlocking(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{BatchSize: 10})
	defer client.Close()
	ctx := context.Background()

	t.Run("FIFO非阻塞出队", func(t *testing.T) {
		queueName := "nb_fifo"
		require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypeFIFO, &QueueItem{Data: "a"}))
		require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypeFIFO, &QueueItem{Data: "b"}))

		item, err := queue.DequeueNonBlocking(ctx, queueName, QueueTypeFIFO)
		assert.NoError(t, err)
		require.NotNil(t, item)
		assert.Equal(t, "a", item.Data) // FIFO 先进先出

		// 空队列出队返回 nil
		require.NoError(t, queue.Clear(ctx, queueName, QueueTypeFIFO))
		item, err = queue.DequeueNonBlocking(ctx, queueName, QueueTypeFIFO)
		assert.NoError(t, err)
		assert.Nil(t, item, "空队列出队应返回 nil")
	})

	t.Run("LIFO非阻塞出队", func(t *testing.T) {
		queueName := "nb_lifo"
		require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypeLIFO, &QueueItem{Data: "x"}))
		require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypeLIFO, &QueueItem{Data: "y"}))

		item, err := queue.DequeueNonBlocking(ctx, queueName, QueueTypeLIFO)
		assert.NoError(t, err)
		require.NotNil(t, item)
		assert.Equal(t, "y", item.Data) // LIFO 后进先出

		// 空队列出队返回 nil
		require.NoError(t, queue.Clear(ctx, queueName, QueueTypeLIFO))
		item, err = queue.DequeueNonBlocking(ctx, queueName, QueueTypeLIFO)
		assert.NoError(t, err)
		assert.Nil(t, item, "空队列出队应返回 nil")
	})

	t.Run("Priority非阻塞出队", func(t *testing.T) {
		queueName := "nb_priority"
		require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypePriority, &QueueItem{Data: "low", Priority: 1}))
		require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypePriority, &QueueItem{Data: "high", Priority: 10}))

		item, err := queue.DequeueNonBlocking(ctx, queueName, QueueTypePriority)
		assert.NoError(t, err)
		require.NotNil(t, item)
		assert.Equal(t, "high", item.Data) // 最高优先级先出

		// 空队列出队返回 nil（ZPopMax 返回空）
		require.NoError(t, queue.Clear(ctx, queueName, QueueTypePriority))
		item, err = queue.DequeueNonBlocking(ctx, queueName, QueueTypePriority)
		assert.NoError(t, err)
		assert.Nil(t, item, "空队列出队应返回 nil")
	})
}

// TestQueueHandler_GetLockKey 验证 getLockKey 生成的键名格式
func TestQueueHandler_GetLockKey(t *testing.T) {
	queue, client, _ := newTestQueue(t, "myns", QueueConfig{})
	defer client.Close()

	// getLockKey 是未导出方法，通过 AcquireLock/ReleaseLock 间接覆盖；这里直接验证格式
	lockKey := queue.getLockKey("order_queue")
	assert.Equal(t, "myns:lock:queue:order_queue", lockKey)
}

// TestQueueHandler_DistributedLock 验证启用分布式锁时的 AcquireLock/ReleaseLock
func TestQueueHandler_DistributedLock(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{
		LockTimeout:           time.Minute,
		EnableDistributedLock: true,
	})
	defer client.Close()
	ctx := context.Background()
	queueName := "dist_lock"

	// worker1 获取锁成功
	acquired, err := queue.AcquireLock(ctx, queueName, "worker1")
	assert.NoError(t, err)
	assert.True(t, acquired, "首次获取锁应成功")

	// worker2 获取锁失败（锁已被 worker1 持有）
	acquired, err = queue.AcquireLock(ctx, queueName, "worker2")
	assert.NoError(t, err)
	assert.False(t, acquired, "锁已被持有时应获取失败")

	// worker2 释放锁不应影响 worker1 的锁（Lua 脚本校验 lockValue）
	assert.NoError(t, queue.ReleaseLock(ctx, queueName, "worker2"))
	// worker2 仍可获取失败
	acquired, _ = queue.AcquireLock(ctx, queueName, "worker2")
	assert.False(t, acquired, "非持有者释放后锁仍被 worker1 持有")

	// worker1 释放锁
	assert.NoError(t, queue.ReleaseLock(ctx, queueName, "worker1"))
	// 之后 worker2 可获取
	acquired, err = queue.AcquireLock(ctx, queueName, "worker2")
	assert.NoError(t, err)
	assert.True(t, acquired, "worker1 释放后 worker2 应获取成功")
}

// TestQueueHandler_DistributedLockError 验证分布式锁在 Redis 错误时返回错误
func TestQueueHandler_DistributedLockError(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	queue := NewQueueHandler(client, "test", QueueConfig{
		LockTimeout:           time.Minute,
		EnableDistributedLock: true,
	})
	ctx := context.Background()

	// 关闭 miniredis 使 SetNX 失败
	mr.Close()
	acquired, err := queue.AcquireLock(ctx, "any", "worker1")
	assert.Error(t, err)
	assert.False(t, acquired)

	// ReleaseLock 的 Eval 在 Redis 不可用时返回错误
	err = queue.ReleaseLock(ctx, "any", "worker1")
	assert.Error(t, err)
}

// TestQueueHandler_GetStatsDelayed 验证 GetStats 对延时队列统计 DelayedCount 分支
func TestQueueHandler_GetStatsDelayed(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{})
	defer client.Close()
	ctx := context.Background()
	queueName := "stats_delayed"

	// 入队延时任务
	require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypeDelayed, &QueueItem{Data: "d1", DelayTime: 5}))
	require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypeDelayed, &QueueItem{Data: "d2", DelayTime: 10}))

	stats, err := queue.GetStats(ctx, queueName, QueueTypeDelayed)
	assert.NoError(t, err)
	require.NotNil(t, stats)
	assert.Equal(t, int64(2), stats.Length, "延时队列长度应为 2")
	assert.Equal(t, int64(2), stats.DelayedCount, "DelayedCount 应为 2")
}

// TestQueueHandler_ContainsBranches 验证 Contains 的各分支
func TestQueueHandler_ContainsBranches(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{BatchSize: 10})
	defer client.Close()
	ctx := context.Background()

	t.Run("空队列返回false", func(t *testing.T) {
		// 长度为 0 时直接返回 false
		contains, err := queue.Contains(ctx, "empty_queue", QueueTypeFIFO, "any")
		assert.NoError(t, err)
		assert.False(t, contains, "空队列应返回 false")
	})

	t.Run("数量超过1000被截断", func(t *testing.T) {
		queueName := "big_queue"
		// 入队 1001 个任务，触发 count > 1000 截断分支
		for i := 0; i < 1001; i++ {
			require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypeFIFO, &QueueItem{Data: fmt.Sprintf("task%d", i)}))
		}
		// Contains 应正常返回（不报错），仅检查前 1000 个
		contains, err := queue.Contains(ctx, queueName, QueueTypeFIFO, "nonexistent")
		assert.NoError(t, err)
		assert.False(t, contains, "不存在的 ID 应返回 false")
	})
}

// TestQueueHandler_UnsupportedType 验证不支持队列类型的默认分支
func TestQueueHandler_UnsupportedType(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{})
	defer client.Close()
	ctx := context.Background()
	invalidType := QueueType("invalid")

	// Enqueue 默认分支
	err := queue.Enqueue(ctx, "q", invalidType, &QueueItem{Data: "x"})
	assert.Error(t, err)

	// Dequeue 默认分支
	_, err = queue.Dequeue(ctx, "q", invalidType)
	assert.Error(t, err)

	// DequeueNonBlocking 默认分支
	_, err = queue.DequeueNonBlocking(ctx, "q", invalidType)
	assert.Error(t, err)

	// Length 默认分支
	_, err = queue.Length(ctx, "q", invalidType)
	assert.Error(t, err)

	// Peek 默认分支
	_, err = queue.Peek(ctx, "q", invalidType, 1)
	assert.Error(t, err)
}

// TestQueueHandler_MarshalError 验证 Enqueue 在序列化失败时返回错误
func TestQueueHandler_MarshalError(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{})
	defer client.Close()
	ctx := context.Background()

	// Data 为 channel 无法被 json.Marshal 序列化
	err := queue.Enqueue(ctx, "q", QueueTypeFIFO, &QueueItem{Data: make(chan int)})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal queue item")
}

// TestQueueHandler_DequeueUnmarshalError 验证出队时反序列化失败返回错误
func TestQueueHandler_DequeueUnmarshalError(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{})
	defer client.Close()
	ctx := context.Background()

	t.Run("FIFO反序列化错误", func(t *testing.T) {
		queueName := "bad_fifo"
		// 直接推送非法 JSON 到队列
		require.NoError(t, client.RPush(ctx, queue.getQueueKey(queueName, QueueTypeFIFO), "not-json").Err())

		_, err := queue.DequeueNonBlocking(ctx, queueName, QueueTypeFIFO)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal queue item")
	})

	t.Run("Priority反序列化错误", func(t *testing.T) {
		queueName := "bad_priority"
		// ZAdd 非法 JSON 成员
		require.NoError(t, client.ZAdd(ctx, queue.getQueueKey(queueName, QueueTypePriority), redis.Z{Score: 1, Member: "not-json"}).Err())

		_, err := queue.DequeueNonBlocking(ctx, queueName, QueueTypePriority)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal queue item")
	})

	t.Run("Delayed反序列化错误", func(t *testing.T) {
		queueName := "bad_delayed"
		// 向延时 key 推入非法 JSON，score 为当前时间（立即到期）
		require.NoError(t, client.ZAdd(ctx, queue.getDelayKey(queueName), redis.Z{Score: float64(time.Now().Unix()), Member: "not-json"}).Err())

		_, err := queue.Dequeue(ctx, queueName, QueueTypeDelayed)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal delayed queue item")
	})
}

// TestQueueHandler_DelayedProcessError 验证 processDelayedQueue 在 Redis 错误时返回错误
func TestQueueHandler_DelayedProcessError(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	queue := NewQueueHandler(client, "test", QueueConfig{})
	ctx := context.Background()

	// 关闭 miniredis 使 ZRangeByScoreWithScores 失败
	mr.Close()
	_, err := queue.Dequeue(ctx, "any", QueueTypeDelayed)
	assert.Error(t, err)
}

// TestQueueHandler_GetFailedItemsErrors 验证 GetFailedItems 的错误分支
func TestQueueHandler_GetFailedItemsErrors(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{})
	defer client.Close()
	ctx := context.Background()
	queueName := "failed_items"

	t.Run("反序列化错误被跳过", func(t *testing.T) {
		failedKey := fmt.Sprintf("%s:failed:%s", "test", queueName)
		// 推入非法 JSON 到失败队列
		require.NoError(t, client.LPush(ctx, failedKey, "not-json").Err())
		// 再推入合法 JSON
		validItem, _ := json.Marshal(&QueueItem{Data: "valid"})
		require.NoError(t, client.LPush(ctx, failedKey, validItem).Err())

		items, err := queue.GetFailedItems(ctx, queueName, 0, 10)
		assert.NoError(t, err, "反序列化错误应被跳过而非返回")
		require.Len(t, items, 1)
		assert.Equal(t, "valid", items[0].Data)
	})

	t.Run("Redis错误返回错误", func(t *testing.T) {
		mr := miniredis.RunT(t)
		c2 := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		q2 := NewQueueHandler(c2, "test", QueueConfig{})
		mr.Close() // 关闭使 LRange 失败
		_, err := q2.GetFailedItems(ctx, "any", 0, 10)
		assert.Error(t, err)
		c2.Close()
	})
}

// TestQueueHandler_BatchDequeuePriority 验证 BatchDequeue 对优先级队列的循环出队路径
func TestQueueHandler_BatchDequeuePriority(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{BatchSize: 10})
	defer client.Close()
	ctx := context.Background()
	queueName := "batch_priority"

	// 入队 3 个不同优先级任务
	require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypePriority, &QueueItem{Data: "low", Priority: 1}))
	require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypePriority, &QueueItem{Data: "mid", Priority: 5}))
	require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypePriority, &QueueItem{Data: "high", Priority: 10}))

	// 批量出队（Priority 走 DequeueNonBlocking 循环路径）
	items, err := queue.BatchDequeue(ctx, queueName, QueueTypePriority, 5)
	assert.NoError(t, err)
	assert.Len(t, items, 3, "应批量出队 3 个任务")
	// 按优先级从高到低
	assert.Equal(t, "high", items[0].Data)
	assert.Equal(t, "mid", items[1].Data)
	assert.Equal(t, "low", items[2].Data)
}

// TestQueueHandler_BatchDequeueLuaError 验证 batchDequeueLua 在 Eval 失败时返回错误
func TestQueueHandler_BatchDequeueLuaError(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	queue := NewQueueHandler(client, "test", QueueConfig{BatchSize: 10})
	ctx := context.Background()

	// 预先入队一个任务
	require.NoError(t, queue.Enqueue(ctx, "lua_err", QueueTypeFIFO, &QueueItem{Data: "x"}))
	// 关闭 miniredis 使 Eval 失败
	mr.Close()
	_, err := queue.BatchDequeue(ctx, "lua_err", QueueTypeFIFO, 5)
	assert.Error(t, err)
}

// TestQueueHandler_PeekErrors 验证 Peek 在 Redis 错误时返回错误
func TestQueueHandler_PeekErrors(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	queue := NewQueueHandler(client, "test", QueueConfig{BatchSize: 10})
	ctx := context.Background()

	// 预先入队
	require.NoError(t, queue.Enqueue(ctx, "peek_err", QueueTypePriority, &QueueItem{Data: "x", Priority: 1}))
	require.NoError(t, queue.Enqueue(ctx, "peek_d", QueueTypeDelayed, &QueueItem{Data: "y", DelayTime: 0}))

	// 关闭 miniredis 使各 Peek 命令失败
	mr.Close()

	_, err := queue.Peek(ctx, "peek_err", QueueTypeFIFO, 1)
	assert.Error(t, err)
	_, err = queue.Peek(ctx, "peek_err", QueueTypeLIFO, 1)
	assert.Error(t, err)
	_, err = queue.Peek(ctx, "peek_err", QueueTypePriority, 1)
	assert.Error(t, err)
	_, err = queue.Peek(ctx, "peek_d", QueueTypeDelayed, 1)
	assert.Error(t, err)
}

// TestQueueHandler_LengthDelayed 验证 Length 对延时队列分支
func TestQueueHandler_LengthDelayed(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{})
	defer client.Close()
	ctx := context.Background()

	require.NoError(t, queue.Enqueue(ctx, "len_d", QueueTypeDelayed, &QueueItem{Data: "d", DelayTime: 5}))
	length, err := queue.Length(ctx, "len_d", QueueTypeDelayed)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), length)
}

// TestQueueHandler_ContainsError 验证 Contains 在 Length/Peek 失败时返回错误
func TestQueueHandler_ContainsError(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	queue := NewQueueHandler(client, "test", QueueConfig{})
	ctx := context.Background()

	// 入队一个任务使长度>0
	require.NoError(t, queue.Enqueue(ctx, "contains_err", QueueTypeFIFO, &QueueItem{Data: "x"}))
	// 关闭 miniredis 使 Length 或 Peek 失败
	mr.Close()
	_, err := queue.Contains(ctx, "contains_err", QueueTypeFIFO, "x")
	assert.Error(t, err)
}

// TestQueueHandler_DequeueNonBlockingRedisError 验证非阻塞出队在 Redis 错误时返回错误
// 覆盖 FIFO/LIFO 的 LPop 错误分支和 Priority 的 ZPopMax 错误分支
func TestQueueHandler_DequeueNonBlockingRedisError(t *testing.T) {
	ctx := context.Background()

	t.Run("FIFO LPop错误", func(t *testing.T) {
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		queue := NewQueueHandler(client, "test", QueueConfig{})
		// 预先入队一个任务使队列非空
		require.NoError(t, queue.Enqueue(ctx, "fifo_err", QueueTypeFIFO, &QueueItem{Data: "x"}))
		// 关闭 miniredis 使 LPop 返回连接错误（非 redis.Nil）
		mr.Close()
		_, err := queue.DequeueNonBlocking(ctx, "fifo_err", QueueTypeFIFO)
		assert.Error(t, err)
	})

	t.Run("LIFO LPop错误", func(t *testing.T) {
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		queue := NewQueueHandler(client, "test", QueueConfig{})
		require.NoError(t, queue.Enqueue(ctx, "lifo_err", QueueTypeLIFO, &QueueItem{Data: "x"}))
		mr.Close()
		_, err := queue.DequeueNonBlocking(ctx, "lifo_err", QueueTypeLIFO)
		assert.Error(t, err)
	})

	t.Run("Priority ZPopMax错误", func(t *testing.T) {
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		queue := NewQueueHandler(client, "test", QueueConfig{})
		require.NoError(t, queue.Enqueue(ctx, "pri_err", QueueTypePriority, &QueueItem{Data: "x", Priority: 1}))
		mr.Close()
		_, err := queue.DequeueNonBlocking(ctx, "pri_err", QueueTypePriority)
		assert.Error(t, err)
	})
}

// TestQueueHandler_DequeueBLPopError 验证 Dequeue（timeout>0）在 BLPop 非 redis.Nil 错误时返回错误
func TestQueueHandler_DequeueBLPopError(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	queue := NewQueueHandler(client, "test", QueueConfig{})
	ctx := context.Background()
	// 关闭 miniredis 使 BLPop 返回连接错误（非 redis.Nil）
	mr.Close()
	_, err := queue.Dequeue(ctx, "blpop_err", QueueTypeFIFO)
	assert.Error(t, err)
}

// TestQueueHandler_DequeueBLPopShortResult 验证 BLPop 返回长度不足时返回 nil
// 通过 hook 使 BLPop 返回单元素切片，触发 len(result) < 2 分支
func TestQueueHandler_DequeueBLPopShortResult(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{})
	defer client.Close()
	ctx := context.Background()

	// 入队一个任务使队列非空
	require.NoError(t, queue.Enqueue(ctx, "short_blpop", QueueTypeFIFO, &QueueItem{Data: "x"}))

	// 添加 hook 使 BLPop 返回长度为 1 的切片
	client.AddHook(&shortBLPopResultHook{})

	item, err := queue.Dequeue(ctx, "short_blpop", QueueTypeFIFO)
	assert.NoError(t, err)
	assert.Nil(t, item, "BLPop 返回长度不足时应返回 nil")
}

// TestQueueHandler_BatchDequeuePriorityError 验证 BatchDequeue 对 Priority 在 Redis 错误时返回错误
func TestQueueHandler_BatchDequeuePriorityError(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	queue := NewQueueHandler(client, "test", QueueConfig{BatchSize: 10})
	ctx := context.Background()
	// 预先入队使队列非空
	require.NoError(t, queue.Enqueue(ctx, "batch_pri_err", QueueTypePriority, &QueueItem{Data: "x", Priority: 1}))
	// 关闭 miniredis 使 DequeueNonBlocking 中的 ZPopMax 失败
	mr.Close()
	_, err := queue.BatchDequeue(ctx, "batch_pri_err", QueueTypePriority, 5)
	assert.Error(t, err)
}

// TestQueueHandler_BatchDequeueLIFO 验证 BatchDequeue 对 LIFO 队列的 Lua 批量出队路径
func TestQueueHandler_BatchDequeueLIFO(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{BatchSize: 10})
	defer client.Close()
	ctx := context.Background()
	queueName := "batch_lifo"

	// 入队 3 个任务
	for i := 0; i < 3; i++ {
		require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypeLIFO, &QueueItem{Data: fmt.Sprintf("item%d", i)}))
	}

	// 批量出队（LIFO 走 batchDequeueLua 的 LIFO 分支）
	items, err := queue.BatchDequeue(ctx, queueName, QueueTypeLIFO, 5)
	assert.NoError(t, err)
	assert.Len(t, items, 3)
	// LIFO: 后入先出
	assert.Equal(t, "item2", items[0].Data)
	assert.Equal(t, "item1", items[1].Data)
	assert.Equal(t, "item0", items[2].Data)
}

// TestQueueHandler_BatchDequeueLuaUnmarshalError 验证 batchDequeueLua 在 JSON 反序列化失败时跳过
func TestQueueHandler_BatchDequeueLuaUnmarshalError(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{BatchSize: 10})
	defer client.Close()
	ctx := context.Background()
	queueName := "batch_lua_bad"

	// 直接推入非法 JSON 到 FIFO 队列
	queueKey := fmt.Sprintf("test:queue:%s:%s", string(QueueTypeFIFO), queueName)
	require.NoError(t, client.RPush(ctx, queueKey, "not-json").Err())
	// 再推入合法 JSON
	validItem, _ := json.Marshal(&QueueItem{Data: "valid"})
	require.NoError(t, client.RPush(ctx, queueKey, validItem).Err())

	// BatchDequeue 应跳过非法 JSON，只返回合法项
	items, err := queue.BatchDequeue(ctx, queueName, QueueTypeFIFO, 5)
	assert.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "valid", items[0].Data)
}

// TestQueueHandler_BatchDequeueLuaUnexpectedResult 验证 batchDequeueLua 在 Eval 返回非切片类型时报错
func TestQueueHandler_BatchDequeueLuaUnexpectedResult(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{BatchSize: 10})
	defer client.Close()
	ctx := context.Background()
	queueName := "batch_lua_unexpected"

	// 预先入队一个任务使队列非空
	require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypeFIFO, &QueueItem{Data: "x"}))

	// 添加 hook 使 EVAL 返回 int64（非 []interface{}）
	client.AddHook(&evalResultHook{result: int64(42)})

	_, err := queue.BatchDequeue(ctx, queueName, QueueTypeFIFO, 5)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected lua result type")
}

// TestQueueHandler_BatchDequeueLuaNonStringElement 验证 batchDequeueLua 在元素类型断言失败时跳过
func TestQueueHandler_BatchDequeueLuaNonStringElement(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{BatchSize: 10})
	defer client.Close()
	ctx := context.Background()
	queueName := "batch_lua_nonstr"

	// 预先入队一个任务使队列非空
	require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypeFIFO, &QueueItem{Data: "x"}))

	// 添加 hook 使 EVAL 返回包含非字符串元素的切片
	client.AddHook(&evalResultHook{result: []interface{}{int64(42), "valid-json"}})

	// 应跳过非字符串元素，继续处理合法的
	items, err := queue.BatchDequeue(ctx, queueName, QueueTypeFIFO, 5)
	assert.NoError(t, err)
	// "valid-json" 不是合法的 QueueItem JSON，也会被 Unmarshal 跳过
	assert.Empty(t, items)
}

// TestQueueHandler_PeekUnmarshalError 验证 Peek 在 JSON 反序列化失败时跳过
func TestQueueHandler_PeekUnmarshalError(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{BatchSize: 10})
	defer client.Close()
	ctx := context.Background()
	queueName := "peek_bad"

	// 直接推入非法 JSON 到 FIFO 队列
	queueKey := fmt.Sprintf("test:queue:%s:%s", string(QueueTypeFIFO), queueName)
	require.NoError(t, client.RPush(ctx, queueKey, "not-json").Err())
	// 再推入合法 JSON
	validItem, _ := json.Marshal(&QueueItem{Data: "valid"})
	require.NoError(t, client.RPush(ctx, queueKey, validItem).Err())

	// Peek 应跳过非法 JSON，只返回合法项
	items, err := queue.Peek(ctx, queueName, QueueTypeFIFO, 5)
	assert.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "valid", items[0].Data)
}

// TestQueueHandler_ContainsPeekError 验证 Contains 在 Length 成功但 Peek 失败时返回错误
// 使用 hook 使第一条命令（Length）正常、第二条命令（Peek）失败
func TestQueueHandler_ContainsPeekError(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{BatchSize: 10})
	defer client.Close()
	ctx := context.Background()
	queueName := "contains_peek_err"

	// 入队一个任务使 Length > 0
	require.NoError(t, queue.Enqueue(ctx, queueName, QueueTypeFIFO, &QueueItem{Data: "x"}))

	// 添加 hook：第 1 条命令（LLen=Length）正常，第 2 条命令（LRange=Peek）失败
	client.AddHook(&failNthCmdHook{failAt: 2, err: errors.New("injected peek error")})

	_, err := queue.Contains(ctx, queueName, QueueTypeFIFO, "any")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "injected peek error")
}

// TestQueueHandler_GetStatsError 验证 GetStats 在 Length 失败时返回错误
func TestQueueHandler_GetStatsError(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	queue := NewQueueHandler(client, "test", QueueConfig{})
	ctx := context.Background()

	// 关闭 miniredis 使 Length 失败
	mr.Close()
	_, err := queue.GetStats(ctx, "any", QueueTypeFIFO)
	assert.Error(t, err)
}

// TestQueueHandler_DequeueNonBlockingPriorityNil 验证 Priority ZPopMax 返回 redis.Nil 时返回 nil
// 通过 hook 使 ZPopMax 返回 redis.Nil，触发 zErr == redis.Nil 分支
func TestQueueHandler_DequeueNonBlockingPriorityNil(t *testing.T) {
	queue, client, _ := newTestQueue(t, "test", QueueConfig{BatchSize: 10})
	defer client.Close()
	ctx := context.Background()

	// 预先入队一个任务使队列非空
	require.NoError(t, queue.Enqueue(ctx, "pri_nil", QueueTypePriority, &QueueItem{Data: "x", Priority: 1}))

	// 添加 hook 使 ZPopMax 返回 redis.Nil 错误
	client.AddHook(&cmdErrorHook{cmdName: "zpopmax", err: redis.Nil})

	item, err := queue.DequeueNonBlocking(ctx, "pri_nil", QueueTypePriority)
	assert.NoError(t, err)
	assert.Nil(t, item, "ZPopMax 返回 redis.Nil 时应返回 nil")
}

// 基准测试
func BenchmarkQueueHandler_FIFOEnqueue(b *testing.B) {
	client := setupRedisClient(&testing.T{})
	defer client.Close()

	config := QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second,
		BatchSize:       10,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := NewQueueHandler(client, "benchmark", config)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		item := &QueueItem{Data: fmt.Sprintf("任务%d", i)}
		queue.Enqueue(ctx, "bench_queue", QueueTypeFIFO, item)
	}
}

func BenchmarkQueueHandler_FIFODequeue(b *testing.B) {
	client := setupRedisClient(&testing.T{})
	defer client.Close()

	config := QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second,
		BatchSize:       10,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := NewQueueHandler(client, "benchmark", config)
	ctx := context.Background()

	// 预先填充队列
	for i := 0; i < b.N; i++ {
		item := &QueueItem{Data: fmt.Sprintf("任务%d", i)}
		queue.Enqueue(ctx, "bench_queue", QueueTypeFIFO, item)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		queue.Dequeue(ctx, "bench_queue", QueueTypeFIFO)
	}
}

// Lua批量出队性能测试
func BenchmarkQueueHandler_BatchDequeue(b *testing.B) {
	client := setupRedisClient(&testing.T{})
	defer client.Close()

	config := QueueConfig{BatchSize: 10}
	queue := NewQueueHandler(client, "bench", config)
	ctx := context.Background()

	b.Run("Batch10_FIFO", func(b *testing.B) {
		qName := "bench_batch_10"
		// 预填充足够多的元素（b.N * 10）
		for j := 0; j < b.N*10; j++ {
			queue.Enqueue(ctx, qName, QueueTypeFIFO, &QueueItem{Data: fmt.Sprintf("item%d", j)})
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			queue.BatchDequeue(ctx, qName, QueueTypeFIFO, 10)
		}
	})

	b.Run("Batch50_FIFO", func(b *testing.B) {
		qName := "bench_batch_50"
		// 预填充足够多的元素
		for j := 0; j < b.N*50; j++ {
			queue.Enqueue(ctx, qName, QueueTypeFIFO, &QueueItem{Data: fmt.Sprintf("item%d", j)})
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			queue.BatchDequeue(ctx, qName, QueueTypeFIFO, 50)
		}
	})

	b.Run("Batch100_FIFO", func(b *testing.B) {
		qName := "bench_batch_100"
		// 预填充足够多的元素
		for j := 0; j < b.N*100; j++ {
			queue.Enqueue(ctx, qName, QueueTypeFIFO, &QueueItem{Data: fmt.Sprintf("item%d", j)})
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			queue.BatchDequeue(ctx, qName, QueueTypeFIFO, 100)
		}
	})

	b.Run("Batch10_LIFO", func(b *testing.B) {
		qName := "bench_batch_lifo_10"
		// 预填充足够多的元素
		for j := 0; j < b.N*10; j++ {
			queue.Enqueue(ctx, qName, QueueTypeLIFO, &QueueItem{Data: fmt.Sprintf("item%d", j)})
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			queue.BatchDequeue(ctx, qName, QueueTypeLIFO, 10)
		}
	})
}

// Peek性能测试
func BenchmarkQueueHandler_Peek(b *testing.B) {
	client := setupRedisClient(&testing.T{})
	defer client.Close()

	config := QueueConfig{BatchSize: 10}
	queue := NewQueueHandler(client, "bench", config)
	ctx := context.Background()

	b.Run("Peek10_FIFO", func(b *testing.B) {
		qName := "bench_peek_fifo"
		// 预填充100个元素
		for j := 0; j < 100; j++ {
			queue.Enqueue(ctx, qName, QueueTypeFIFO, &QueueItem{Data: fmt.Sprintf("item%d", j)})
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			queue.Peek(ctx, qName, QueueTypeFIFO, 10)
		}
	})

	b.Run("Peek10_LIFO", func(b *testing.B) {
		qName := "bench_peek_lifo"
		// 预填充100个元素
		for j := 0; j < 100; j++ {
			queue.Enqueue(ctx, qName, QueueTypeLIFO, &QueueItem{Data: fmt.Sprintf("item%d", j)})
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			queue.Peek(ctx, qName, QueueTypeLIFO, 10)
		}
	})

	b.Run("Peek50_FIFO", func(b *testing.B) {
		qName := "bench_peek_fifo_50"
		// 预填充100个元素
		for j := 0; j < 100; j++ {
			queue.Enqueue(ctx, qName, QueueTypeFIFO, &QueueItem{Data: fmt.Sprintf("item%d", j)})
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			queue.Peek(ctx, qName, QueueTypeFIFO, 50)
		}
	})
}
