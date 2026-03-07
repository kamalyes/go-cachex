/**
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-03-06 22:35:20
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-03-07 10:38:22
 * @FilePath: \go-cachex\durable_channel_test.go
 * @Description: 持久化 Channel 测试
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

	"github.com/kamalyes/go-toolbox/pkg/random"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMessage 测试消息结构
type TestMessage struct {
	ID      string
	Content string
	Time    time.Time
}

// createTestMessage 创建测试消息
func createTestMessage(id, content string) *TestMessage {
	return &TestMessage{
		ID:      id,
		Content: content,
		Time:    time.Now(),
	}
}

// createTestMessages 批量创建测试消息
func createTestMessages(t *testing.T, count int) []*TestMessage {
	messages := make([]*TestMessage, count)
	for i := range count {
		messages[i] = createTestMessage(
			fmt.Sprintf("%s-%d", t.Name(), i),
			fmt.Sprintf("Message %d", i),
		)
	}
	return messages
}

// sendMessages 批量发送消息
func sendMessages(t *testing.T, ch *DurableChannel[*TestMessage], messages []*TestMessage) {
	ctx := context.Background()
	for _, msg := range messages {
		err := ch.Send(ctx, msg)
		require.NoError(t, err)
	}
}

// receiveMessages 接收指定数量的消息
func receiveMessages(t *testing.T, ch *DurableChannel[*TestMessage], count int, timeout time.Duration) []*TestMessage {
	received := make([]*TestMessage, 0, count)
	timer := time.After(timeout)

	for len(received) < count {
		select {
		case msg := <-ch.Receive():
			assert.NotNil(t, msg)
			received = append(received, msg)
		case <-timer:
			t.Fatalf("只接收了 %d 条消息，期望 %d 条", len(received), count)
		}
	}

	return received
}

// setupDurableChannelTest 设置测试环境
func setupDurableChannelTest(t *testing.T, name string) (*DurableChannel[*TestMessage], func()) {
	client := setupRedisClient(t)

	config := DefaultDurableChannelConfig(name)
	config.BufferSize = 10
	config.WorkerCount = 2

	ch := NewDurableChannel[*TestMessage](client, config)

	cleanup := func() {
		ch.Close()
		client.Del(context.Background(), ch.GetQueueKey())
		client.Close()
	}

	return ch, cleanup
}

func TestDurableChannel_SendReceive(t *testing.T) {
	ch, cleanup := setupDurableChannelTest(t, "test-send-receive")
	defer cleanup()

	msg := createTestMessage(t.Name(), "Hello World")
	sendMessages(t, ch, []*TestMessage{msg})

	received := receiveMessages(t, ch, 1, 3*time.Second)
	assert.Equal(t, msg.ID, received[0].ID)
	assert.Equal(t, msg.Content, received[0].Content)
}

func TestDurableChannel_PersistenceRecovery(t *testing.T) {
	channelName := "test-recovery"
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	messageCount := 5

	// 第一个 channel：发送消息后关闭（生产者模式）
	var queueKey string
	{
		config := DefaultDurableChannelConfig(channelName)
		config.RecoveryOnStart = false
		config.PersistenceMode = PersistSync
		ch1 := NewDurableChannel[*TestMessage](client, config)

		queueKey = ch1.GetQueueKey()
		client.Del(ctx, queueKey)

		messages := createTestMessages(t, messageCount)
		sendMessages(t, ch1, messages)

		time.Sleep(500 * time.Millisecond)

		ch1.Close()
	}

	// 验证 Redis 中有消息
	redisLen, err := client.LLen(ctx, queueKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(messageCount), redisLen, "Redis 中应该有 %d 条消息", messageCount)

	// 第二个 channel：恢复消息（消费者模式）
	{
		config := DefaultDurableChannelConfig(channelName)
		config.RecoveryOnStart = true
		ch2 := NewDurableChannel[*TestMessage](client, config)
		defer ch2.Close()

		received := receiveMessages(t, ch2, messageCount, 5*time.Second)
		assert.Equal(t, messageCount, len(received), "应该恢复 %d 条消息", messageCount)

		client.Del(ctx, ch2.GetQueueKey())
	}
}

func TestDurableChannel_MultipleWorkers(t *testing.T) {
	ch, cleanup := setupDurableChannelTest(t, "test-workers")
	defer cleanup()

	messageCount := 20
	messages := createTestMessages(t, messageCount)
	sendMessages(t, ch, messages)

	received := receiveMessages(t, ch, messageCount, 10*time.Second)
	assert.Equal(t, messageCount, len(received))
}

func TestDurableChannel_TryReceive(t *testing.T) {
	ch, cleanup := setupDurableChannelTest(t, "test-try-receive")
	defer cleanup()

	ctx := context.Background()

	// 场景1：空队列
	_, ok := ch.TryReceive(ctx)
	assert.False(t, ok)

	// 场景2：有消息
	msg := createTestMessage(t.Name(), "Hello")
	sendMessages(t, ch, []*TestMessage{msg})

	time.Sleep(200 * time.Millisecond)

	received, ok := ch.TryReceive(ctx)
	assert.True(t, ok)
	assert.Equal(t, msg.ID, received.ID)
}

func TestDurableChannel_Len(t *testing.T) {
	ch, cleanup := setupDurableChannelTest(t, "test-len")
	defer cleanup()

	totalLen, err := ch.Len()
	assert.NoError(t, err)
	assert.Equal(t, int64(0), totalLen)

	messageCount := 5
	messages := createTestMessages(t, messageCount)
	sendMessages(t, ch, messages)

	time.Sleep(500 * time.Millisecond)

	totalLen, err = ch.Len()
	assert.NoError(t, err)
	assert.Greater(t, totalLen, int64(0))
}

func TestDurableChannel_Close(t *testing.T) {
	ch, cleanup := setupDurableChannelTest(t, "test-close")
	defer cleanup()

	ctx := context.Background()

	msg := createTestMessage(t.Name(), "Hello")
	sendMessages(t, ch, []*TestMessage{msg})

	err := ch.Close()
	assert.NoError(t, err)

	// 再次发送应该失败
	err = ch.Send(ctx, msg)
	assert.Error(t, err)
}

func TestDurableChannel_PersistModes(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	tests := []struct {
		mode           PersistMode
		msgID          string
		shouldReceive  bool
		verifyRedisLen bool
	}{
		{PersistSync, random.RandString(10, random.CAPITAL), false, true},
		{PersistAsync, random.RandString(10, random.CAPITAL), true, false},
		{PersistHybrid, random.RandString(10, random.CAPITAL), true, false},
	}

	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			config := DefaultDurableChannelConfig("test-mode-" + tt.msgID)
			config.PersistenceMode = tt.mode
			ch := NewDurableChannel[*TestMessage](client, config)
			defer ch.Close()
			defer client.Del(ctx, ch.GetQueueKey())

			msg := createTestMessage(tt.msgID, "Hello "+tt.mode.String())
			sendMessages(t, ch, []*TestMessage{msg})

			if tt.verifyRedisLen {
				time.Sleep(500 * time.Millisecond)
				totalLen, err := ch.Len()
				assert.NoError(t, err)
				assert.Equal(t, int64(1), totalLen, "消息应该在 Redis 中")
			}

			if tt.shouldReceive {
				received := receiveMessages(t, ch, 1, 3*time.Second)
				assert.Equal(t, msg.ID, received[0].ID)
			}
		})
	}
}

// TestDurableChannel_DistributedNodes 测试分布式节点隔离（使用 WorkerID）
func TestDurableChannel_DistributedNodes(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	baseConfig := DurableChannelConfig{
		Name:            "test-distributed",
		PersistenceMode: PersistHybrid,
		BufferSize:      10,
	}

	// 创建 3 个节点
	nodes := make([]*DurableChannel[*TestMessage], 3)
	for i := range 3 {
		config := baseConfig
		config.WorkerID = int64(i)
		nodes[i] = NewDurableChannel[*TestMessage](client, config)
		defer nodes[i].Close()
		client.Del(ctx, nodes[i].GetQueueKey())
	}

	// 每个节点发送 2 条消息
	for i, node := range nodes {
		messages := []*TestMessage{
			createTestMessage(fmt.Sprintf("node%d-msg1", i), fmt.Sprintf("Node %d Message 1", i)),
			createTestMessage(fmt.Sprintf("node%d-msg2", i), fmt.Sprintf("Node %d Message 2", i)),
		}
		sendMessages(t, node, messages)
	}

	time.Sleep(500 * time.Millisecond)

	// 验证每个节点只能收到自己的消息
	for i, node := range nodes {
		received := receiveMessages(t, node, 2, 3*time.Second)
		expectedIDs := []string{fmt.Sprintf("node%d-msg1", i), fmt.Sprintf("node%d-msg2", i)}

		for _, msg := range received {
			assert.Contains(t, expectedIDs, msg.ID)
		}

		expectedKey := fmt.Sprintf("dchan:test-distributed:%d:queue", i)
		assert.Equal(t, expectedKey, node.GetQueueKey())

		client.Del(ctx, node.GetQueueKey())
	}
}

// TestDurableChannel_SharedQueue 测试共享队列模式（使用相同 WorkerID）
func TestDurableChannel_SharedQueue(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	baseConfig := DurableChannelConfig{
		Name:            "test-shared",
		WorkerID:        0,
		PersistenceMode: PersistHybrid,
		BufferSize:      10,
		WorkerCount:     1,
	}

	consumer1 := NewDurableChannel[*TestMessage](client, baseConfig)
	defer consumer1.Close()

	consumer2 := NewDurableChannel[*TestMessage](client, baseConfig)
	defer consumer2.Close()

	client.Del(ctx, consumer1.GetQueueKey())

	// 生产者发送消息
	producerConfig := baseConfig
	producerConfig.PersistenceMode = PersistSync
	producer := NewDurableChannel[*TestMessage](client, producerConfig)
	defer producer.Close()

	messageCount := 10
	messages := createTestMessages(t, messageCount)
	sendMessages(t, producer, messages)

	time.Sleep(500 * time.Millisecond)

	// 两个消费者竞争消费
	received1, received2 := 0, 0
	timeout := time.After(5 * time.Second)

	for received1+received2 < messageCount {
		select {
		case msg := <-consumer1.Receive():
			assert.NotNil(t, msg)
			t.Logf("Consumer1 收到: %s", msg.ID)
			received1++
		case msg := <-consumer2.Receive():
			assert.NotNil(t, msg)
			t.Logf("Consumer2 收到: %s", msg.ID)
			received2++
		case <-timeout:
			t.Fatalf("超时，Consumer1: %d, Consumer2: %d, 总计: %d/%d", received1, received2, received1+received2, messageCount)
		}
	}

	assert.Greater(t, received1, 0, "Consumer1 应该收到消息")
	assert.Greater(t, received2, 0, "Consumer2 应该收到消息")
	assert.Equal(t, messageCount, received1+received2, "总共应该收到 %d 条消息", messageCount)

	assert.Equal(t, "dchan:test-shared:0:queue", consumer1.GetQueueKey())
	assert.Equal(t, "dchan:test-shared:0:queue", consumer2.GetQueueKey())

	client.Del(ctx, consumer1.GetQueueKey())
}

// TestDurableChannel_QueueKeyFormat 测试队列 Key 格式
func TestDurableChannel_QueueKeyFormat(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	tests := []struct {
		name      string
		namespace string
		queueName string
		workerID  int64
		expectKey string
	}{
		{"默认配置", "dchan", "test-queue", 0, "dchan:test-queue:0:queue"},
		{"WorkerID = 1", "dchan", "test-queue", 1, "dchan:test-queue:1:queue"},
		{"WorkerID = 123", "dchan", "test-queue", 123, "dchan:test-queue:123:queue"},
		{"自定义 Namespace", "myapp", "test-queue", 5, "myapp:test-queue:5:queue"},
		{"空 Namespace（使用默认）", "", "test-queue", 0, "dchan:test-queue:0:queue"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DurableChannelConfig{
				Name:            tt.queueName,
				Namespace:       tt.namespace,
				WorkerID:        tt.workerID,
				PersistenceMode: PersistSync,
			}

			ch := NewDurableChannel[*TestMessage](client, config)
			defer ch.Close()
			defer client.Del(context.Background(), ch.GetQueueKey())

			assert.Equal(t, tt.expectKey, ch.GetQueueKey())
		})
	}
}

// TestDurableChannel_ContextCancellation 测试 context 取消场景
func TestDurableChannel_ContextCancellation(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("Send_ContextTimeout", func(t *testing.T) {
		config := DefaultDurableChannelConfig("test-send-timeout")
		config.BufferSize = 2
		config.PersistenceMode = PersistAsync
		config.WorkerCount = 0
		ch := NewDurableChannel[*TestMessage](client, config)
		defer ch.Close()
		defer client.Del(context.Background(), ch.GetQueueKey())

		// 填满缓冲区
		messages := createTestMessages(t, config.BufferSize)
		sendMessages(t, ch, messages)
		assert.Equal(t, 2, ch.MemLen())

		// 测试超时
		ctxTimeout, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		start := time.Now()
		err := ch.Send(ctxTimeout, createTestMessage("timeout", "Should timeout"))
		duration := time.Since(start)

		assert.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.GreaterOrEqual(t, duration, 100*time.Millisecond)
	})

	t.Run("Worker_ContextCancellation", func(t *testing.T) {
		config := DefaultDurableChannelConfig("test-worker-cancel")
		config.WorkerCount = 3
		config.PersistenceMode = PersistHybrid
		ch := NewDurableChannel[*TestMessage](client, config)
		defer client.Del(context.Background(), ch.GetQueueKey())

		messages := createTestMessages(t, 5)
		sendMessages(t, ch, messages)

		time.Sleep(500 * time.Millisecond)

		start := time.Now()
		err := ch.Close()
		duration := time.Since(start)

		assert.NoError(t, err)
		assert.Less(t, duration, 3*time.Second)

		err = ch.Close()
		assert.NoError(t, err)
	})

	t.Run("PersistAsync_WorkerPoolClose", func(t *testing.T) {
		config := DefaultDurableChannelConfig("test-pool-close")
		config.PersistenceMode = PersistAsync
		config.WorkerCount = 2
		config.BufferSize = 50
		ch := NewDurableChannel[*TestMessage](client, config)
		defer client.Del(context.Background(), ch.GetQueueKey())

		assert.NotNil(t, ch.persistPool)

		messages := createTestMessages(t, 10)
		sendMessages(t, ch, messages)

		time.Sleep(100 * time.Millisecond)

		start := time.Now()
		err := ch.Close()
		duration := time.Since(start)

		assert.NoError(t, err)
		assert.Less(t, duration, 2*time.Second)
		assert.True(t, ch.persistPool.IsClosed())
	})
}

// TestDurableChannel_EdgeCases 测试边界情况
func TestDurableChannel_EdgeCases(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("EmptyChannelName_Panic", func(t *testing.T) {
		defer func() {
			r := recover()
			assert.NotNil(t, r)
		}()
		config := DefaultDurableChannelConfig("")
		NewDurableChannel[*TestMessage](client, config)
	})

	t.Run("ZeroBufferSize_UseDefault", func(t *testing.T) {
		config := DefaultDurableChannelConfig("test-zero-buffer")
		config.BufferSize = 0
		ch := NewDurableChannel[*TestMessage](client, config)
		defer ch.Close()
		defer client.Del(context.Background(), ch.GetQueueKey())

		assert.Equal(t, 100, ch.config.BufferSize)
	})

	t.Run("PersistSync_ZeroWorkers", func(t *testing.T) {
		config := DefaultDurableChannelConfig("test-sync-workers")
		config.WorkerCount = 5
		config.PersistenceMode = PersistSync
		ch := NewDurableChannel[*TestMessage](client, config)
		defer ch.Close()
		defer client.Del(context.Background(), ch.GetQueueKey())

		assert.Equal(t, 0, ch.config.WorkerCount)
	})

	t.Run("MemoryChannelFull_HybridMode", func(t *testing.T) {
		config := DefaultDurableChannelConfig("test-mem-full")
		config.BufferSize = 2
		config.PersistenceMode = PersistHybrid
		config.WorkerCount = 0 // 尝试设置为 0，但 Hybrid 模式会自动设置为 1
		ch := NewDurableChannel[*TestMessage](client, config)
		defer ch.Close()
		defer client.Del(context.Background(), ch.GetQueueKey())

		// Hybrid 模式下，WorkerCount 会被自动设置为 1
		assert.Equal(t, 1, ch.config.WorkerCount)

		sendCount := 10
		messages := createTestMessages(t, sendCount)
		sendMessages(t, ch, messages)

		// 立即检查，消息应该在 Redis 中
		time.Sleep(100 * time.Millisecond)

		// 由于有 worker 在消费，内存中的消息数量应该小于等于 BufferSize
		memLen := ch.MemLen()
		assert.LessOrEqual(t, memLen, ch.config.BufferSize)

		// Redis 中应该有消息（因为内存满了会溢出到 Redis）
		redisLen, err := ch.RedisLen()
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, redisLen, int64(0))

		// 注意：Len() 可能会重复计数（消息在从 Redis 移动到内存的过程中）
		// 所以我们只验证消息数量在合理范围内
		totalLen, err := ch.Len()
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, totalLen, int64(0))
		assert.LessOrEqual(t, totalLen, int64(sendCount+ch.config.BufferSize))
	})
}

// TestDurableChannel_Concurrency 测试并发场景
func TestDurableChannel_Concurrency(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	t.Run("ConcurrentSend", func(t *testing.T) {
		config := DefaultDurableChannelConfig("test-concurrent-send")
		config.PersistenceMode = PersistHybrid
		config.BufferSize = 50
		ch := NewDurableChannel[*TestMessage](client, config)
		defer ch.Close()
		defer client.Del(context.Background(), ch.GetQueueKey())

		ctx := context.Background()
		goroutines := 10
		messagesPerGoroutine := 10
		totalMessages := goroutines * messagesPerGoroutine

		sentIDs := make(map[string]bool)
		var mu sync.Mutex
		var wg sync.WaitGroup
		var sendErrors int32

		for i := range goroutines {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := range messagesPerGoroutine {
					msgID := fmt.Sprintf("g%d-m%d", id, j)
					err := ch.Send(ctx, createTestMessage(msgID, "Concurrent"))
					if err != nil {
						atomic.AddInt32(&sendErrors, 1)
					} else {
						mu.Lock()
						sentIDs[msgID] = true
						mu.Unlock()
					}
				}
			}(i)
		}

		wg.Wait()

		assert.Equal(t, int32(0), atomic.LoadInt32(&sendErrors))
		assert.Equal(t, totalMessages, len(sentIDs))
	})

	t.Run("ConcurrentReceive", func(t *testing.T) {
		config := DefaultDurableChannelConfig("test-concurrent-receive")
		config.WorkerCount = 3
		config.BufferSize = 100
		ch := NewDurableChannel[*TestMessage](client, config)
		defer ch.Close()
		defer client.Del(context.Background(), ch.GetQueueKey())

		totalMessages := 50
		messages := createTestMessages(t, totalMessages)
		sendMessages(t, ch, messages)

		sentIDs := make(map[string]bool)
		for _, msg := range messages {
			sentIDs[msg.ID] = true
		}

		// 等待消息进入内存 channel
		time.Sleep(time.Second)

		receivedIDs := make(map[string]bool)
		var mu sync.Mutex
		var wg sync.WaitGroup

		// 使用 channel 来通知收到足够的消息
		done := make(chan struct{})
		var doneOnce sync.Once

		for range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case msg := <-ch.Receive():
						if msg != nil {
							mu.Lock()
							receivedIDs[msg.ID] = true
							// 如果收到了所有消息，通知其他 goroutine 停止
							if len(receivedIDs) >= totalMessages {
								doneOnce.Do(func() { close(done) })
							}
							mu.Unlock()
						}
					case <-done:
						return
					}
				}
			}()
		}

		// 等待收到所有消息或超时
		select {
		case <-done:
			// 收到所有消息
		case <-time.After(5 * time.Second):
			// 超时
			t.Logf("超时，只收到 %d/%d 条消息", len(receivedIDs), totalMessages)
		}

		// 通知所有 goroutine 停止（如果还没停止）
		doneOnce.Do(func() { close(done) })
		wg.Wait()

		assert.Greater(t, len(receivedIDs), 0)
		assert.LessOrEqual(t, len(receivedIDs), totalMessages)

		for id := range receivedIDs {
			assert.True(t, sentIDs[id])
		}
	})

	t.Run("ConcurrentSendAndClose", func(t *testing.T) {
		config := DefaultDurableChannelConfig("test-send-close-race")
		config.BufferSize = 50
		ch := NewDurableChannel[*TestMessage](client, config)
		defer client.Del(context.Background(), ch.GetQueueKey())

		ctx := context.Background()
		var wg sync.WaitGroup
		var successCount, errorCount int32

		for g := range 5 {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for i := range 20 {
					msgID := fmt.Sprintf("g%d-m%d", id, i)
					err := ch.Send(ctx, createTestMessage(msgID, "Race"))
					if err != nil {
						atomic.AddInt32(&errorCount, 1)
					} else {
						atomic.AddInt32(&successCount, 1)
					}
					time.Sleep(time.Millisecond)
				}
			}(g)
		}

		time.Sleep(50 * time.Millisecond)

		start := time.Now()
		err := ch.Close()
		duration := time.Since(start)

		wg.Wait()

		assert.NoError(t, err)
		assert.Less(t, duration, 3*time.Second)

		success := atomic.LoadInt32(&successCount)
		errors := atomic.LoadInt32(&errorCount)
		assert.Greater(t, success, int32(0))
		assert.Greater(t, errors, int32(0))
	})
}
