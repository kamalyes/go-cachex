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
	"sync"
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
	var mu sync.Mutex
	var lastEvent AlertEvent
	dlq.SetAlertCallback(func(event AlertEvent) {
		alertCount.Add(1)
		mu.Lock()
		lastEvent = event
		mu.Unlock()
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
	mu.Lock()
	event := lastEvent
	mu.Unlock()
	assert.Equal(t, AlertLevelWarning, event.Level)
	assert.Equal(t, testQueueKeyTest, event.QueueKey)
	assert.Equal(t, int64(6), event.Length)
}

func TestDeadLetterQueue_SetAlertThresholds(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	// 设置自定义阈值
	dlq.SetAlertThresholds(0.5, 0.7, 0.9)

	var mu sync.Mutex
	var alertLevel AlertLevel
	dlq.SetAlertCallback(func(event AlertEvent) {
		mu.Lock()
		alertLevel = event.Level
		mu.Unlock()
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
	mu.Lock()
	level := alertLevel
	mu.Unlock()
	assert.Equal(t, AlertLevelWarning, level)
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

// TestAlertLevel_String 测试 AlertLevel 的 String 方法
func TestAlertLevel_String(t *testing.T) {
	assert.Equal(t, "INFO", AlertLevelInfo.String())
	assert.Equal(t, "WARNING", AlertLevelWarning.String())
	assert.Equal(t, "ERROR", AlertLevelError.String())
	assert.Equal(t, "CRITICAL", AlertLevelCritical.String())
	assert.Equal(t, "UNKNOWN", AlertLevel(999).String())
}

// TestDeadLetterQueue_CheckAndAlert_CriticalLevel 测试 checkAndAlert 触发严重阈值
func TestDeadLetterQueue_CheckAndAlert_CriticalLevel(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	var mu sync.Mutex
	var alertLevel AlertLevel
	dlq.SetAlertCallback(func(event AlertEvent) {
		mu.Lock()
		alertLevel = event.Level
		mu.Unlock()
	})

	// maxSize=10, criticalThreshold=0.95 -> 9 个触发严重
	for i := range 10 {
		data := &TestData{
			ID:      fmt.Sprintf("crit-%d", i),
			Message: fmt.Sprintf("message %d", i),
			Time:    time.Now(),
		}
		err := dlq.Push(ctx, testQueueKeyTest, data)
		require.NoError(t, err)
	}

	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	level := alertLevel
	mu.Unlock()
	assert.Equal(t, AlertLevelCritical, level, "应触发严重级别预警")
}

// TestDeadLetterQueue_CheckAndAlert_ErrorLevel 测试 checkAndAlert 触发错误阈值
func TestDeadLetterQueue_CheckAndAlert_ErrorLevel(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	var mu sync.Mutex
	var alertLevel AlertLevel
	dlq.SetAlertCallback(func(event AlertEvent) {
		mu.Lock()
		alertLevel = event.Level
		mu.Unlock()
	})

	// maxSize=10, errorThreshold=0.8 -> 8 个触发错误
	for i := range 8 {
		data := &TestData{
			ID:      fmt.Sprintf("err-%d", i),
			Message: fmt.Sprintf("message %d", i),
			Time:    time.Now(),
		}
		err := dlq.Push(ctx, testQueueKeyTest, data)
		require.NoError(t, err)
	}

	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	level := alertLevel
	mu.Unlock()
	assert.Equal(t, AlertLevelError, level, "应触发错误级别预警")
}

// TestDeadLetterQueue_CheckAndAlert_NoThreshold 测试 checkAndAlert 未达到阈值时不触发
func TestDeadLetterQueue_CheckAndAlert_NoThreshold(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	var alertCount atomic.Int32
	dlq.SetAlertCallback(func(event AlertEvent) {
		alertCount.Add(1)
	})

	// 推送少量数据（不超过警告阈值 60%）
	for i := range 3 {
		data := &TestData{
			ID:      fmt.Sprintf("low-%d", i),
			Message: fmt.Sprintf("message %d", i),
			Time:    time.Now(),
		}
		err := dlq.Push(ctx, testQueueKeyTest, data)
		require.NoError(t, err)
	}

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), alertCount.Load(), "未达到阈值不应触发预警")
}

// TestDeadLetterQueue_Remove_ZeroCount 测试 Remove 在 count <= 0 时直接返回
func TestDeadLetterQueue_Remove_ZeroCount(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	// 推送数据
	for i := range 3 {
		data := &TestData{
			ID:      fmt.Sprintf("zero-%d", i),
			Message: fmt.Sprintf("message %d", i),
			Time:    time.Now(),
		}
		err := dlq.Push(ctx, testQueueKeyTest, data)
		require.NoError(t, err)
	}

	// count=0 应直接返回，不移除任何数据
	err := dlq.Remove(ctx, testQueueKeyTest, 0)
	assert.NoError(t, err)

	// 验证队列长度不变
	length, err := dlq.GetLength(ctx, testQueueKeyTest)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), length, "Remove(0) 不应移除任何数据")
}

// TestDeadLetterQueue_GetItems_DefaultCount 测试 GetItems 在 count <= 0 时使用默认值
func TestDeadLetterQueue_GetItems_DefaultCount(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	// 推送数据
	for i := range 5 {
		data := &TestData{
			ID:      fmt.Sprintf("default-%d", i),
			Message: fmt.Sprintf("message %d", i),
			Time:    time.Now(),
		}
		err := dlq.Push(ctx, testQueueKeyTest, data)
		require.NoError(t, err)
	}

	// count=0 应使用默认值 10
	items, err := dlq.GetItems(ctx, testQueueKeyTest, 0)
	assert.NoError(t, err)
	assert.Len(t, items, 5)
}

// TestDeadLetterQueue_GetItems_EmptyQueue 测试 GetItems 在空队列时返回空切片
func TestDeadLetterQueue_GetItems_EmptyQueue(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	ctx := context.Background()

	items, err := dlq.GetItems(ctx, "nonexistent_queue", 10)
	assert.NoError(t, err)
	assert.Empty(t, items)
}

// TestDeadLetterQueue_SetAlertThresholds_InvalidValues 测试 SetAlertThresholds 忽略无效值
func TestDeadLetterQueue_SetAlertThresholds_InvalidValues(t *testing.T) {
	_, dlq, cleanup := setupDLQTest(t)
	defer cleanup()

	// 设置无效阈值（0 或 >=1 应被忽略）
	dlq.SetAlertThresholds(0, 1.5, -0.5)

	// 不应 panic
	assert.NotNil(t, dlq)
}

// TestDeadLetterQueue_Clear_Error 测试 Clear 在 queueHandler 失败时返回错误
func TestDeadLetterQueue_Clear_Error(t *testing.T) {
	client := setupRedisClient(t)

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

	// 关闭客户端使 Clear 失败
	client.Close()

	err := dlq.Clear(context.Background(), testQueueKeyTest)
	assert.Error(t, err)
}

// TestDeadLetterQueue_GetLength_Error 测试 GetLength 在 queueHandler 失败时返回错误
func TestDeadLetterQueue_GetLength_Error(t *testing.T) {
	client := setupRedisClient(t)

	queueConfig := QueueConfig{
		MaxRetries:  3,
		RetryDelay:  time.Second,
		BatchSize:   10,
		LockTimeout: time.Minute,
	}
	queueHandler := NewQueueHandler(client, "test:dlq:", queueConfig)

	dlqConfig := DeadLetterQueueConfig{
		MaxSize: 10,
	}
	dlq := NewDeadLetterQueue[*TestData](queueHandler, dlqConfig)

	// 关闭客户端使 Length 失败
	client.Close()

	_, err := dlq.GetLength(context.Background(), testQueueKeyTest)
	assert.Error(t, err)
}

// unmarshalFailingType 是一个自定义类型，其 UnmarshalJSON 总是返回错误
type unmarshalFailingType struct {
	Value string
}

func (u *unmarshalFailingType) UnmarshalJSON(data []byte) error {
	return fmt.Errorf("unmarshal always fails")
}

// TestDeadLetterQueue_Push_EnqueueError 测试 Push 在 Enqueue 失败时返回错误
func TestDeadLetterQueue_Push_EnqueueError(t *testing.T) {
	client := setupRedisClient(t)

	queueConfig := QueueConfig{
		MaxRetries:  3,
		RetryDelay:  time.Second,
		BatchSize:   10,
		LockTimeout: time.Minute,
	}
	queueHandler := NewQueueHandler(client, "test:dlq:", queueConfig)

	dlq := NewDeadLetterQueue[*TestData](queueHandler, DeadLetterQueueConfig{MaxSize: 10})

	// 关闭客户端使 Enqueue 失败
	client.Close()

	err := dlq.Push(context.Background(), testQueueKeyTest, &TestData{ID: "1", Message: "test"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "push to dead letter queue failed")
}

// TestDeadLetterQueue_Push_LengthError 测试 Push 在 Enqueue 成功但 Length 失败时返回错误
func TestDeadLetterQueue_Push_LengthError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	// 使用 hook 使第 2 条命令（LLen）失败，第 1 条命令（RPush）正常
	client.AddHook(&failNthCmdHook{failAt: 2, err: fmt.Errorf("forced LLen error")})

	queueConfig := QueueConfig{
		MaxRetries:  3,
		RetryDelay:  time.Second,
		BatchSize:   10,
		LockTimeout: time.Minute,
	}
	queueHandler := NewQueueHandler(client, "test:dlq:", queueConfig)

	dlq := NewDeadLetterQueue[*TestData](queueHandler, DeadLetterQueueConfig{MaxSize: 10})

	err := dlq.Push(context.Background(), testQueueKeyTest, &TestData{ID: "1", Message: "test"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get queue length failed")
}

// TestDeadLetterQueue_GetItems_PeekError 测试 GetItems 在 Peek 失败时返回错误
func TestDeadLetterQueue_GetItems_PeekError(t *testing.T) {
	client := setupRedisClient(t)

	queueConfig := QueueConfig{
		MaxRetries:  3,
		RetryDelay:  time.Second,
		BatchSize:   10,
		LockTimeout: time.Minute,
	}
	queueHandler := NewQueueHandler(client, "test:dlq:", queueConfig)

	dlq := NewDeadLetterQueue[*TestData](queueHandler, DeadLetterQueueConfig{MaxSize: 10})

	// 先推送数据
	err := dlq.Push(context.Background(), testQueueKeyTest, &TestData{ID: "1", Message: "test"})
	require.NoError(t, err)

	// 关闭客户端使 Peek 失败
	client.Close()

	_, err = dlq.GetItems(context.Background(), testQueueKeyTest, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get dead letter items failed")
}

// TestDeadLetterQueue_GetItems_TypeAssertionSuccess 测试 GetItems 类型断言成功路径
func TestDeadLetterQueue_GetItems_TypeAssertionSuccess(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	queueConfig := QueueConfig{
		MaxRetries:  3,
		RetryDelay:  time.Second,
		BatchSize:   10,
		LockTimeout: time.Minute,
	}
	queueHandler := NewQueueHandler(client, "test:dlq:", queueConfig)

	// 使用 string 类型，JSON 往返后类型信息不丢失
	dlq := NewDeadLetterQueue[string](queueHandler, DeadLetterQueueConfig{MaxSize: 10})

	ctx := context.Background()

	// 推送字符串数据
	err := dlq.Push(ctx, testQueueKeyTest, "hello_world")
	require.NoError(t, err)

	// 获取数据，类型断言应成功
	items, err := dlq.GetItems(ctx, testQueueKeyTest, 10)
	assert.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "hello_world", items[0])
}

// TestDeadLetterQueue_GetItems_UnmarshalError 测试 GetItems JSON 反序列化失败时跳过数据
func TestDeadLetterQueue_GetItems_UnmarshalError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	queueConfig := QueueConfig{
		MaxRetries:  3,
		RetryDelay:  time.Second,
		BatchSize:   10,
		LockTimeout: time.Minute,
	}
	queueHandler := NewQueueHandler(client, "test:dlq:", queueConfig)

	// 使用自定义类型，其 UnmarshalJSON 总是失败
	dlq := NewDeadLetterQueue[*unmarshalFailingType](queueHandler, DeadLetterQueueConfig{MaxSize: 10})

	ctx := context.Background()

	// 推送数据（Marshal 使用默认行为，可以成功）
	err := dlq.Push(ctx, testQueueKeyTest, &unmarshalFailingType{Value: "test"})
	require.NoError(t, err)

	// 获取数据时，类型断言失败，JSON 重新反序列化也失败，应返回空切片
	items, err := dlq.GetItems(ctx, testQueueKeyTest, 10)
	assert.NoError(t, err)
	assert.Empty(t, items, "反序列化失败的数据应被跳过")
}

// TestDeadLetterQueue_Remove_BatchDequeueError 测试 Remove 在 BatchDequeue 失败时返回错误
func TestDeadLetterQueue_Remove_BatchDequeueError(t *testing.T) {
	client := setupRedisClient(t)

	queueConfig := QueueConfig{
		MaxRetries:  3,
		RetryDelay:  time.Second,
		BatchSize:   10,
		LockTimeout: time.Minute,
	}
	queueHandler := NewQueueHandler(client, "test:dlq:", queueConfig)

	dlq := NewDeadLetterQueue[*TestData](queueHandler, DeadLetterQueueConfig{MaxSize: 10})

	// 先推送数据
	err := dlq.Push(context.Background(), testQueueKeyTest, &TestData{ID: "1", Message: "test"})
	require.NoError(t, err)

	// 关闭客户端使 BatchDequeue 失败
	client.Close()

	err = dlq.Remove(context.Background(), testQueueKeyTest, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "remove dead letter items failed")
}
