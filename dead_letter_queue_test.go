/**
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-03-06 15:15:17
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-03-06 15:55:17
 * @FilePath: \go-cachex\dead_letter_queue_test.go
 * @Description: 死信队列测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */

package cachex

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 测试用队列键常量
const (
	testQueueKeyUserOffline  = "message:user_offline"
	testQueueKeyTest         = "message:test"
	testQueueKeyNetworkError = "message:network_error"
	testQueueKeyTimeout      = "message:timeout"
)

// TestData 测试数据结构
type TestData struct {
	ID      string
	Message string
	Time    time.Time
}

// setupDLQTest 设置死信队列测试环境
func setupDLQTest(t *testing.T) (*QueueHandler, DeadLetterQueue[*TestData], func()) {
	client := setupRedisClient(t)

	ctx := context.Background()

	queueConfig := QueueConfig{
		MaxRetries:  3,
		RetryDelay:  time.Second,
		BatchSize:   10,
		LockTimeout: time.Minute,
	}
	queueHandler := NewQueueHandler(client, "test:dlq:", queueConfig)

	dlqConfig := DeadLetterQueueConfig{
		MaxSize:           10,
		WarningThreshold:  0.6,
		ErrorThreshold:    0.8,
		CriticalThreshold: 0.95,
	}
	dlq := NewDeadLetterQueue[*TestData](queueHandler, dlqConfig)

	cleanup := func() {
		// 清理测试数据
		keys, _ := client.Keys(ctx, "test:dlq:*").Result()
		if len(keys) > 0 {
			client.Del(ctx, keys...)
		}
		client.Close()
	}

	return queueHandler, dlq, cleanup
}

func TestDeadLetterQueue_Push(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	// 场景1：成功推送数据
	data := &TestData{
		ID:      "test-1",
		Message: "test message",
		Time:    time.Now(),
	}
	err := dlq.Push(ctx, testQueueKeyUserOffline, data)
	assert.NoError(t, err)

	// 场景2：推送 nil 数据应该失败
	err = dlq.Push(ctx, testQueueKeyUserOffline, nil)
	require.Error(t, err, "Push nil data should return error")
	assert.Contains(t, err.Error(), "nil")

	// 场景3：验证队列长度（应该只有1个，nil没有被推入）
	length, err := dlq.GetLength(ctx, testQueueKeyUserOffline)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), length)
}

func TestDeadLetterQueue_GetItems(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	// 推送测试数据
	for i := range 5 {
		data := &TestData{
			ID:      fmt.Sprintf("test-%d", i),
			Message: fmt.Sprintf("message %d", i),
			Time:    time.Now(),
		}
		err := dlq.Push(ctx, testQueueKeyTest, data)
		require.NoError(t, err)
	}

	// 场景1：获取指定数量的数据
	items, err := dlq.GetItems(ctx, testQueueKeyTest, 3)
	assert.NoError(t, err)
	assert.Len(t, items, 3)

	// 场景2：获取全部数据
	items, err = dlq.GetItems(ctx, testQueueKeyTest, 10)
	assert.NoError(t, err)
	assert.Len(t, items, 5)

	// 场景3：验证数据顺序（FIFO）
	require.NotEmpty(t, items, "items should not be empty")
	if len(items) >= 2 {
		assert.Equal(t, "test-0", items[0].ID)
		assert.Equal(t, "test-1", items[1].ID)
	}

	// 场景4：获取后队列长度不变
	length, err := dlq.GetLength(ctx, testQueueKeyTest)
	assert.NoError(t, err)
	assert.Equal(t, int64(5), length)
}

func TestDeadLetterQueue_Remove(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	// 推送测试数据
	for i := range 5 {
		data := &TestData{
			ID:      fmt.Sprintf("test-%d", i),
			Message: fmt.Sprintf("message %d", i),
			Time:    time.Now(),
		}
		err := dlq.Push(ctx, testQueueKeyTest, data)
		require.NoError(t, err)
	}

	// 场景1：移除指定数量
	err := dlq.Remove(ctx, testQueueKeyTest, 2)
	assert.NoError(t, err)

	// 场景2：验证队列长度
	length, err := dlq.GetLength(ctx, testQueueKeyTest)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), length)

	// 场景3：验证移除的是头部数据
	items, err := dlq.GetItems(ctx, testQueueKeyTest, 1)
	assert.NoError(t, err)
	require.NotEmpty(t, items, "items should not be empty after remove")
	assert.Equal(t, "test-2", items[0].ID)
}

func TestDeadLetterQueue_Clear(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	// 推送测试数据
	for i := range 5 {
		data := &TestData{
			ID:      fmt.Sprintf("test-%d", i),
			Message: fmt.Sprintf("message %d", i),
			Time:    time.Now(),
		}
		err := dlq.Push(ctx, testQueueKeyTest, data)
		require.NoError(t, err)
	}

	// 场景1：清空队列
	err := dlq.Clear(ctx, testQueueKeyTest)
	assert.NoError(t, err)

	// 场景2：验证队列长度为 0
	length, err := dlq.GetLength(ctx, testQueueKeyTest)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), length)
}

func TestDeadLetterQueue_MaxSize(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	// 推送超过 maxSize 的数据（maxSize = 10）
	for i := range 15 {
		data := &TestData{
			ID:      fmt.Sprintf("test-%d", i),
			Message: fmt.Sprintf("message %d", i),
			Time:    time.Now(),
		}
		err := dlq.Push(ctx, testQueueKeyTest, data)
		require.NoError(t, err)
	}

	// 验证队列长度不超过 maxSize
	length, err := dlq.GetLength(ctx, testQueueKeyTest)
	assert.NoError(t, err)
	assert.LessOrEqual(t, length, int64(10))

	// 验证保留的是最新的数据
	items, err := dlq.GetItems(ctx, testQueueKeyTest, 1)
	assert.NoError(t, err)
	require.NotEmpty(t, items, "items should not be empty")
	assert.Equal(t, "test-5", items[0].ID) // 前 5 个被移除
}

func TestDeadLetterQueue_AlertCallback(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	// 设置预警回调
	var alertCount atomic.Int32
	var lastEvent AlertEvent
	dlq.SetAlertCallback(func(event AlertEvent) {
		alertCount.Add(1)
		lastEvent = event
	})

	// 推送数据触发警告阈值（60% = 6）
	for i := range 6 {
		data := &TestData{
			ID:      fmt.Sprintf("test-%d", i),
			Message: fmt.Sprintf("message %d", i),
			Time:    time.Now(),
		}
		err := dlq.Push(ctx, testQueueKeyTest, data)
		require.NoError(t, err)
	}

	// 等待回调执行
	time.Sleep(100 * time.Millisecond)

	// 验证触发了预警
	assert.Greater(t, alertCount.Load(), int32(0))
	assert.Equal(t, AlertLevelWarning, lastEvent.Level)
	assert.Equal(t, testQueueKeyTest, lastEvent.QueueKey)
	assert.Equal(t, int64(6), lastEvent.Length)
}

func TestDeadLetterQueue_SetAlertThresholds(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	// 设置自定义阈值
	dlq.SetAlertThresholds(0.5, 0.7, 0.9)

	var alertLevel AlertLevel
	dlq.SetAlertCallback(func(event AlertEvent) {
		alertLevel = event.Level
	})

	// 推送数据触发警告阈值（50% = 5）
	for i := range 5 {
		data := &TestData{
			ID:      fmt.Sprintf("test-%d", i),
			Message: fmt.Sprintf("message %d", i),
			Time:    time.Now(),
		}
		err := dlq.Push(ctx, testQueueKeyTest, data)
		require.NoError(t, err)
	}

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, AlertLevelWarning, alertLevel)
}

func TestDeadLetterQueue_MultipleQueues(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	// 推送到不同的队列
	queues := []string{testQueueKeyUserOffline, testQueueKeyNetworkError, testQueueKeyTimeout}
	for _, queue := range queues {
		for i := range 3 {
			data := &TestData{
				ID:      fmt.Sprintf("%s-%d", queue, i),
				Message: fmt.Sprintf("message %d", i),
				Time:    time.Now(),
			}
			err := dlq.Push(ctx, queue, data)
			require.NoError(t, err)
		}
	}

	// 验证每个队列的长度
	for _, queue := range queues {
		length, err := dlq.GetLength(ctx, queue)
		assert.NoError(t, err)
		assert.Equal(t, int64(3), length)
	}

	// 验证队列之间互不影响
	err := dlq.Clear(ctx, testQueueKeyUserOffline)
	assert.NoError(t, err)

	length, err := dlq.GetLength(ctx, testQueueKeyUserOffline)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), length)

	length, err = dlq.GetLength(ctx, testQueueKeyNetworkError)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), length)
}
