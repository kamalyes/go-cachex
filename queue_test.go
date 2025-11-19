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
	"fmt"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func setupRedisClient(t *testing.T) *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr:            "120.79.25.168:16389",
		Password:        "M5Pi9YW6u",
		DB:              1,
		DialTimeout:     10 * time.Second, // 增加拨号超时
		ReadTimeout:     5 * time.Second,  // 增加读超时
		WriteTimeout:    5 * time.Second,  // 增加写超时
		PoolTimeout:     10 * time.Second, // 增加池超时
		PoolSize:        10,               // 恢复正常连接池大小
		MinIdleConns:    2,                // 最小空闲连接
		MaxRetries:      3,                // 增加重试次数
		DisableIdentity: true,
	})

	// 增加连接测试超时
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 测试连接
	err := client.Ping(ctx).Err()
	if err != nil {
		client.Close()
		t.Skipf("Redis不可用，跳过测试: %v", err)
		return nil
	}

	// 清理测试数据
	client.FlushDB(ctx)

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
		{Data: "延时3秒任务", DelayTime: 3}, // 3秒后执行
		{Data: "延时1秒任务", DelayTime: 1}, // 1秒后执行
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

	// 等待1.5秒后再次获取（应该能获取到1秒延时的任务）
	time.Sleep(time.Millisecond * 1500)
	item, err = queue.Dequeue(ctx, queueName, QueueTypeDelayed)
	assert.NoError(t, err)
	assert.NotNil(t, item, "应该能获取到1秒延时的任务")
	assert.Equal(t, "延时1秒任务", item.Data)

	// 现在不应该有可用的任务
	item, err = queue.Dequeue(ctx, queueName, QueueTypeDelayed)
	assert.NoError(t, err)
	assert.Nil(t, item, "3秒任务还没到时间，不应该有可用任务")
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
	queueName := "test_peek"

	// 入队几个任务
	for i := 1; i <= 3; i++ {
		item := &QueueItem{Data: fmt.Sprintf("任务%d", i)}
		err := queue.Enqueue(ctx, queueName, QueueTypeFIFO, item)
		assert.NoError(t, err)
	}

	// Peek应该能看到队列头部的任务，但不移除它们
	items, err := queue.Peek(ctx, queueName, QueueTypeFIFO, 2)
	assert.NoError(t, err)
	assert.Len(t, items, 2, "Peek应该返回2个任务")

	// 队列长度应该没有变化
	length, err := queue.Length(ctx, queueName, QueueTypeFIFO)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), length, "Peek不应该改变队列长度")

	// 出队一个任务后，Peek的结果应该改变
	_, err = queue.Dequeue(ctx, queueName, QueueTypeFIFO)
	assert.NoError(t, err)

	items, err = queue.Peek(ctx, queueName, QueueTypeFIFO, 2)
	assert.NoError(t, err)
	assert.Len(t, items, 2, "出队后Peek应该返回剩余的2个任务")
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
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			item := &QueueItem{Data: fmt.Sprintf("任务%d", i)}
			queue.Enqueue(ctx, "bench_queue", QueueTypeFIFO, item)
			i++
		}
	})
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
