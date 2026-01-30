/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-19 21:58:11
 * @FilePath: \go-cachex\pubsub_test.go
 * @Description: 发布订阅测试
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPubSub_BasicPublishSubscribe(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	// 创建发布订阅实例
	pubsub := NewPubSub(client)
	defer pubsub.Close()

	// 用于收集消息的通道
	messages := make(chan string, 10)
	received := make(map[string]bool)
	var mu sync.Mutex

	// 消息处理器
	handler := func(ctx context.Context, channel string, message string) error {
		mu.Lock()
		received[message] = true
		mu.Unlock()

		select {
		case messages <- message:
		default:
		}
		return nil
	}

	// 订阅频道
	subscriber, err := pubsub.Subscribe([]string{"test_channel"}, handler)
	assert.NoError(t, err)
	require.NotNil(t, subscriber)
	defer subscriber.Stop()

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发布消息
	testMessages := []string{"消息1", "消息2", "消息3"}
	for _, msg := range testMessages {
		err := pubsub.Publish(ctx, "test_channel", msg)
		assert.NoError(t, err)
	}

	// 等待消息处理
	time.Sleep(time.Millisecond * 500)

	// 验证消息接收
	for _, expectedMsg := range testMessages {
		mu.Lock()
		received := received[expectedMsg]
		mu.Unlock()
		assert.True(t, received, "应该收到消息: %s", expectedMsg)
	}

	// 验证订阅者状态
	assert.True(t, subscriber.IsActive(), "订阅者应该处于活跃状态")
	assert.Equal(t, []string{"test_channel"}, subscriber.GetChannels())
}

func TestPubSub_JSONMessages(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	// 创建发布订阅实例
	pubsub := NewPubSub(client)
	defer pubsub.Close()

	// 测试数据结构
	type TestData struct {
		ID      int    `json:"id"`
		Message string `json:"message"`
		Time    int64  `json:"time"`
	}

	// 收集接收到的数据
	received := make(chan TestData, 5)

	// JSON消息处理器
	jsonHandler := func(ctx context.Context, channel string, data TestData) error {
		select {
		case received <- data:
		default:
		}
		return nil
	}

	// 订阅JSON消息
	subscriber, err := SubscribeJSON[TestData](pubsub, []string{"json_channel"}, jsonHandler)
	assert.NoError(t, err)
	require.NotNil(t, subscriber)
	defer subscriber.Stop()

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发布JSON数据
	testData := []TestData{
		{ID: 1, Message: "第一条消息", Time: time.Now().Unix()},
		{ID: 2, Message: "第二条消息", Time: time.Now().Unix()},
		{ID: 3, Message: "第三条消息", Time: time.Now().Unix()},
	}

	for _, data := range testData {
		err := PublishJSON[TestData](pubsub, ctx, "json_channel", data)
		assert.NoError(t, err)
	}

	// 验证接收到的数据
	for i, expected := range testData {
		select {
		case receivedData := <-received:
			assert.Equal(t, expected.ID, receivedData.ID, "消息%d的ID应该匹配", i+1)
			assert.Equal(t, expected.Message, receivedData.Message, "消息%d的内容应该匹配", i+1)
			assert.Equal(t, expected.Time, receivedData.Time, "消息%d的时间应该匹配", i+1)
		case <-time.After(time.Second * 2):
			t.Fatalf("等待消息%d超时", i+1)
		}
	}
}

func TestPubSub_MultipleChannels(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	pubsub := NewPubSub(client)
	defer pubsub.Close()

	// 收集消息的map
	messages := make(map[string][]string)
	var mu sync.Mutex // 多频道消息处理器
	handler := func(ctx context.Context, channel string, message string) error {
		mu.Lock()
		if _, exists := messages[channel]; !exists {
			messages[channel] = make([]string, 0)
		}
		messages[channel] = append(messages[channel], message)
		mu.Unlock()
		return nil
	}

	// 订阅多个频道
	channels := []string{"channel1", "channel2", "channel3"}
	subscriber, err := pubsub.Subscribe(channels, handler)
	assert.NoError(t, err)
	require.NotNil(t, subscriber)
	defer subscriber.Stop()

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 向不同频道发送消息
	for i, channel := range channels {
		for j := 1; j <= 3; j++ {
			msg := fmt.Sprintf("频道%d消息%d", i+1, j)
			err := pubsub.Publish(ctx, channel, msg)
			assert.NoError(t, err)
		}
	}

	// 等待消息处理
	time.Sleep(time.Millisecond * 500)

	// 验证每个频道都收到了正确的消息
	mu.Lock()
	defer mu.Unlock()

	for i, channel := range channels {
		channelMessages, exists := messages[channel]
		assert.True(t, exists, "频道%s应该有消息", channel)
		assert.Len(t, channelMessages, 3, "频道%s应该有3条消息", channel)

		for j, msg := range channelMessages {
			expected := fmt.Sprintf("频道%d消息%d", i+1, j+1)
			assert.Equal(t, expected, msg, "频道%s的消息%d不匹配", channel, j+1)
		}
	}
}

func TestPubSub_PatternSubscribe(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	pubsub := NewPubSub(client)
	defer pubsub.Close()

	// 收集消息
	messages := make([]string, 0)
	var mu sync.Mutex

	// 模式匹配处理器
	handler := func(ctx context.Context, channel string, message string) error {
		mu.Lock()
		messages = append(messages, fmt.Sprintf("%s:%s", channel, message))
		mu.Unlock()
		return nil
	}

	// 订阅模式
	subscriber, err := pubsub.SubscribePattern([]string{"test.*"}, handler)
	assert.NoError(t, err)
	require.NotNil(t, subscriber)
	defer subscriber.Stop()

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发布到匹配的频道
	matchingChannels := []string{"test.1", "test.2", "test.abc"}
	for i, channel := range matchingChannels {
		msg := fmt.Sprintf("消息%d", i+1)
		err := pubsub.Publish(ctx, channel, msg)
		assert.NoError(t, err)
	}

	// 发布到不匹配的频道
	err = pubsub.Publish(ctx, "other.channel", "不应该收到")
	assert.NoError(t, err)

	// 等待消息处理
	time.Sleep(time.Millisecond * 500)

	// 验证只收到匹配的消息
	mu.Lock()
	defer mu.Unlock()

	assert.Len(t, messages, 3, "应该只收到3条匹配的消息")

	for i, expectedChannel := range matchingChannels {
		expectedMsg := fmt.Sprintf("%s:消息%d", expectedChannel, i+1)
		assert.Contains(t, messages, expectedMsg, "应该包含消息: %s", expectedMsg)
	}
}

func TestPubSub_Unsubscribe(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	pubsub := NewPubSub(client)
	defer pubsub.Close()

	messageCount := 0
	var mu sync.Mutex

	handler := func(ctx context.Context, channel string, message string) error {
		mu.Lock()
		messageCount++
		mu.Unlock()
		return nil
	}

	// 订阅频道
	subscriber, err := pubsub.Subscribe([]string{"unsub_test"}, handler)
	assert.NoError(t, err)
	require.NotNil(t, subscriber)

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发布第一条消息
	err = pubsub.Publish(ctx, "unsub_test", "消息1")
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 100)

	// 取消订阅
	err = pubsub.Unsubscribe("unsub_test")
	assert.NoError(t, err)

	// 等待取消订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发布第二条消息（不应该收到）
	err = pubsub.Publish(ctx, "unsub_test", "消息2")
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 100)

	// 验证只收到一条消息
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, messageCount, "取消订阅后不应该收到新消息")

	// 验证订阅者已停止
	assert.False(t, subscriber.IsActive(), "取消订阅后订阅者应该不活跃")
}

func TestSubscriber_Unsubscribe(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	config := PubSubConfig{
		Namespace:    "subscriber_unsub_test",
		MaxRetries:   2,
		RetryDelay:   time.Millisecond * 50,
		BufferSize:   10,
		PingInterval: time.Second,
	}

	pubsub := NewPubSub(client, config)
	defer pubsub.Close()

	messageCount := 0
	var mu sync.Mutex

	handler := func(ctx context.Context, channel string, message string) error {
		mu.Lock()
		messageCount++
		mu.Unlock()
		return nil
	}

	// 订阅多个频道
	subscriber, err := pubsub.Subscribe([]string{"sub_test1", "sub_test2"}, handler)
	assert.NoError(t, err)
	require.NotNil(t, subscriber)

	// 验证订阅者活跃
	assert.True(t, subscriber.IsActive(), "订阅者应该处于活跃状态")

	// 验证已注册
	assert.Equal(t, 2, pubsub.GetSubscribers(), "应该有2个频道订阅")

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发布消息到两个频道
	err = pubsub.Publish(ctx, "sub_test1", "消息1")
	assert.NoError(t, err)
	err = pubsub.Publish(ctx, "sub_test2", "消息2")
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 200)

	// 验证收到消息
	mu.Lock()
	beforeCount := messageCount
	mu.Unlock()
	assert.Equal(t, 2, beforeCount, "应该收到2条消息")

	// 调用 Subscriber.Unsubscribe()
	err = subscriber.Unsubscribe()
	assert.NoError(t, err)

	// 验证订阅者已停止
	assert.False(t, subscriber.IsActive(), "取消订阅后订阅者应该不活跃")

	// 验证从注册表中移除
	assert.Equal(t, 0, pubsub.GetSubscribers(), "应该没有活跃订阅者")

	// 等待取消订阅生效
	time.Sleep(time.Millisecond * 100)

	// 再次发布消息（不应该收到）
	err = pubsub.Publish(ctx, "sub_test1", "消息3")
	assert.NoError(t, err)
	err = pubsub.Publish(ctx, "sub_test2", "消息4")
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 200)

	// 验证没有收到新消息
	mu.Lock()
	finalCount := messageCount
	mu.Unlock()
	assert.Equal(t, beforeCount, finalCount, "取消订阅后不应该收到新消息")
}

func TestSubscriber_UnsubscribePattern(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	config := PubSubConfig{
		Namespace:    "pattern_unsub_test",
		MaxRetries:   2,
		RetryDelay:   time.Millisecond * 50,
		BufferSize:   10,
		PingInterval: time.Second,
	}

	pubsub := NewPubSub(client, config)
	defer pubsub.Close()

	messageCount := 0
	var mu sync.Mutex

	handler := func(ctx context.Context, channel string, message string) error {
		mu.Lock()
		messageCount++
		mu.Unlock()
		return nil
	}

	// 订阅模式
	subscriber, err := pubsub.SubscribePattern([]string{"pattern.*", "test.*"}, handler)
	assert.NoError(t, err)
	require.NotNil(t, subscriber)

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发布匹配消息
	err = pubsub.Publish(ctx, "pattern.1", "消息1")
	assert.NoError(t, err)
	err = pubsub.Publish(ctx, "test.2", "消息2")
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 200)

	// 验证收到消息
	mu.Lock()
	beforeCount := messageCount
	mu.Unlock()
	assert.Equal(t, 2, beforeCount, "应该收到2条模式匹配消息")

	// 取消模式订阅
	err = subscriber.Unsubscribe()
	assert.NoError(t, err)

	// 验证订阅者已停止
	assert.False(t, subscriber.IsActive(), "取消订阅后订阅者应该不活跃")

	// 等待取消订阅生效
	time.Sleep(time.Millisecond * 100)

	// 再次发布匹配消息（不应该收到）
	err = pubsub.Publish(ctx, "pattern.3", "消息3")
	assert.NoError(t, err)
	err = pubsub.Publish(ctx, "test.4", "消息4")
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 200)

	// 验证没有收到新消息
	mu.Lock()
	finalCount := messageCount
	mu.Unlock()
	assert.Equal(t, beforeCount, finalCount, "取消订阅后不应该收到新消息")
}

func TestSubscriber_GetSubscriptionInfo(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	config := PubSubConfig{
		Namespace:    "info_test",
		MaxRetries:   2,
		RetryDelay:   time.Millisecond * 50,
		BufferSize:   10,
		PingInterval: time.Second,
	}

	pubsub := NewPubSub(client, config)
	defer pubsub.Close()

	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	// 测试普通频道订阅信息
	subscriber1, err := pubsub.Subscribe([]string{"info_ch1", "info_ch2", "info_ch3"}, handler)
	assert.NoError(t, err)
	defer subscriber1.Stop()

	info1 := subscriber1.GetSubscriptionInfo()
	assert.NotNil(t, info1)
	assert.False(t, info1.IsPattern, "应该不是模式订阅")
	assert.True(t, info1.IsActive, "应该处于活跃状态")
	assert.Equal(t, 3, info1.ChannelCount, "应该有3个频道")
	assert.Len(t, info1.Channels, 3, "频道列表应该有3个元素")
	assert.Contains(t, info1.Channels, "info_ch1")
	assert.Contains(t, info1.Channels, "info_ch2")
	assert.Contains(t, info1.Channels, "info_ch3")
	assert.Equal(t, config.Namespace, info1.Config.Namespace, "配置应该匹配")

	// 测试模式订阅信息
	subscriber2, err := pubsub.SubscribePattern([]string{"pattern.*", "test.*"}, handler)
	assert.NoError(t, err)
	defer subscriber2.Stop()

	info2 := subscriber2.GetSubscriptionInfo()
	assert.NotNil(t, info2)
	assert.True(t, info2.IsPattern, "应该是模式订阅")
	assert.True(t, info2.IsActive, "应该处于活跃状态")
	assert.Equal(t, 2, info2.ChannelCount, "应该有2个模式")
	assert.Len(t, info2.Channels, 2, "模式列表应该有2个元素")
	assert.Contains(t, info2.Channels, "pattern.*")
	assert.Contains(t, info2.Channels, "test.*")

	// 停止订阅后检查信息
	subscriber1.Stop()
	time.Sleep(time.Millisecond * 100)

	info3 := subscriber1.GetSubscriptionInfo()
	assert.False(t, info3.IsActive, "停止后应该不活跃")
	assert.Equal(t, 3, info3.ChannelCount, "频道数不变")
}

func TestSubscriber_Resubscribe(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	config := PubSubConfig{
		Namespace:    "resubscribe_test",
		MaxRetries:   2,
		RetryDelay:   time.Millisecond * 50,
		BufferSize:   10,
		PingInterval: time.Second,
	}

	pubsub := NewPubSub(client, config)
	defer pubsub.Close()

	messageCount := 0
	var mu sync.Mutex

	handler := func(ctx context.Context, channel string, message string) error {
		mu.Lock()
		messageCount++
		mu.Unlock()
		return nil
	}

	// 初始订阅
	subscriber, err := pubsub.Subscribe([]string{"resub_test"}, handler)
	assert.NoError(t, err)
	assert.True(t, subscriber.IsActive(), "初始订阅应该活跃")

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发送第一条消息
	err = pubsub.Publish(ctx, "resub_test", "消息1")
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 200)

	// 验证收到消息
	mu.Lock()
	firstCount := messageCount
	mu.Unlock()
	assert.Equal(t, 1, firstCount, "应该收到第一条消息")

	// 停止订阅
	subscriber.Stop()
	time.Sleep(time.Millisecond * 100)
	assert.False(t, subscriber.IsActive(), "停止后应该不活跃")

	// 尝试在活跃时重新订阅（应该失败）
	// 先重新订阅
	err = subscriber.Resubscribe()
	assert.NoError(t, err)
	assert.True(t, subscriber.IsActive(), "重新订阅后应该活跃")

	// 再次尝试重新订阅（应该失败）
	err = subscriber.Resubscribe()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already active")

	// 等待重新订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发送第二条消息
	err = pubsub.Publish(ctx, "resub_test", "消息2")
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 200)

	// 验证重新订阅后收到消息
	mu.Lock()
	finalCount := messageCount
	mu.Unlock()
	assert.Equal(t, 2, finalCount, "重新订阅后应该收到第二条消息")

	// 清理
	subscriber.Stop()
}

func TestSubscriber_ResubscribePattern(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	config := PubSubConfig{
		Namespace:    "resubscribe_pattern_test",
		MaxRetries:   2,
		RetryDelay:   time.Millisecond * 50,
		BufferSize:   10,
		PingInterval: time.Second,
	}

	pubsub := NewPubSub(client, config)
	defer pubsub.Close()

	messageCount := 0
	var mu sync.Mutex

	handler := func(ctx context.Context, channel string, message string) error {
		mu.Lock()
		messageCount++
		mu.Unlock()
		return nil
	}

	// 模式订阅
	subscriber, err := pubsub.SubscribePattern([]string{"resubpat.*"}, handler)
	assert.NoError(t, err)
	assert.True(t, subscriber.IsActive(), "初始订阅应该活跃")

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发送匹配消息
	err = pubsub.Publish(ctx, "resubpat.1", "消息1")
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 200)

	mu.Lock()
	firstCount := messageCount
	mu.Unlock()
	assert.Equal(t, 1, firstCount, "应该收到第一条消息")

	// 停止并重新订阅
	subscriber.Stop()
	time.Sleep(time.Millisecond * 100)

	err = subscriber.Resubscribe()
	assert.NoError(t, err)
	assert.True(t, subscriber.IsActive(), "重新订阅后应该活跃")

	// 等待重新订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发送第二条匹配消息
	err = pubsub.Publish(ctx, "resubpat.2", "消息2")
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 200)

	mu.Lock()
	finalCount := messageCount
	mu.Unlock()
	assert.Equal(t, 2, finalCount, "重新订阅后应该收到第二条消息")

	// 验证订阅信息
	info := subscriber.GetSubscriptionInfo()
	assert.True(t, info.IsPattern, "应该是模式订阅")
	assert.True(t, info.IsActive, "应该活跃")

	subscriber.Stop()
}

func TestPubSub_BroadcastMessage(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	pubsub := NewPubSub(client)
	defer pubsub.Close()

	// 收集消息
	receivedChannels := make(map[string]bool)
	var mu sync.Mutex

	handler := func(ctx context.Context, channel string, message string) error {
		mu.Lock()
		receivedChannels[channel] = true
		mu.Unlock()
		return nil
	}

	// 订阅多个频道
	channels := []string{"broadcast1", "broadcast2", "broadcast3"}
	for _, channel := range channels {
		_, err := pubsub.Subscribe([]string{channel}, handler)
		assert.NoError(t, err)
	}

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 广播消息到所有频道
	err := pubsub.BroadcastMessage(ctx, channels, "广播消息")
	assert.NoError(t, err)

	// 等待消息处理
	time.Sleep(time.Millisecond * 500)

	// 验证所有频道都收到了消息
	mu.Lock()
	defer mu.Unlock()

	for _, channel := range channels {
		assert.True(t, receivedChannels[channel], "频道%s应该收到广播消息", channel)
	}

	assert.Len(t, receivedChannels, 3, "应该有3个频道收到消息")
}

func TestPubSub_RequestResponse(t *testing.T) {
	t.Skip("Skipping RequestResponse test - may cause timeout due to Redis connection issues")

	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	pubsub := NewPubSub(client)
	defer pubsub.Close()

	// 模拟服务端：监听请求并发送响应
	go func() {
		handler := func(ctx context.Context, channel string, message string) error {
			// 模拟处理请求
			response := fmt.Sprintf("响应:%s", message)
			return pubsub.Publish(ctx, "response_channel", response)
		}

		subscriber, err := pubsub.Subscribe([]string{"request_channel"}, handler)
		if err != nil {
			t.Logf("服务端订阅失败: %v", err)
			return
		}
		defer subscriber.Stop()

		// 保持服务端运行
		time.Sleep(time.Second * 3)
	}()

	// 等待服务端启动
	time.Sleep(time.Millisecond * 200)

	// 发送请求并等待响应
	response, err := pubsub.RequestResponse(
		ctx,
		"request_channel",
		"response_channel",
		"测试请求",
		time.Second*2,
	)

	assert.NoError(t, err)
	assert.Equal(t, "响应:测试请求", response)
}

func TestPubSub_RequestResponseTimeout(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	pubsub := NewPubSub(client)
	defer pubsub.Close()

	// 不启动服务端，直接发送请求（应该超时）
	_, err := pubsub.RequestResponse(
		ctx,
		"timeout_request",
		"timeout_response",
		"超时测试",
		time.Millisecond*100, // 很短的超时时间
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout", "应该是超时错误")
}

func TestPubSub_Stats(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	// 创建单独的PubSub实例避免与其他测试冲突
	config := PubSubConfig{
		Namespace:    "stats_test",
		MaxRetries:   2,
		RetryDelay:   time.Millisecond * 50,
		BufferSize:   10,
		PingInterval: time.Second,
	}

	pubsub := NewPubSub(client, config)
	defer pubsub.Close()

	// 初始统计应该为空
	stats := pubsub.GetStats()
	assert.Equal(t, 0, stats.ActiveSubscribers)
	assert.Len(t, stats.Channels, 0)
	assert.Len(t, stats.Patterns, 0)

	// 添加普通订阅
	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	subscriber1, err := pubsub.Subscribe([]string{"stats_channel1", "stats_channel2"}, handler)
	assert.NoError(t, err)
	defer subscriber1.Stop()

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 添加模式订阅
	subscriber2, err := pubsub.SubscribePattern([]string{"stats.*"}, handler)
	assert.NoError(t, err)
	defer subscriber2.Stop()

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 检查更新后的统计
	stats = pubsub.GetStats()
	assert.Equal(t, 2, stats.ActiveSubscribers, "应该有2个活跃订阅者")
	assert.Contains(t, stats.Channels, "stats_channel1")
	assert.Contains(t, stats.Channels, "stats_channel2")
	assert.Contains(t, stats.Patterns, "stats.*")
}

func TestPubSub_ErrorHandling(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	config := PubSubConfig{
		Namespace:     "test",
		MaxRetries:    2,
		RetryDelay:    time.Millisecond * 50,
		BufferSize:    10,
		PingInterval:  time.Second,
	}

	pubsub := NewPubSub(client, config)
	defer pubsub.Close()

	// 测试处理器错误重试
	handlerCallCount := 0
	var mu sync.Mutex

	errorHandler := func(ctx context.Context, channel string, message string) error {
		mu.Lock()
		handlerCallCount++
		count := handlerCallCount
		mu.Unlock()

		if count <= 2 { // 前两次调用失败
			return fmt.Errorf("模拟处理错误")
		}
		return nil // 第三次成功
	}

	subscriber, err := pubsub.Subscribe([]string{"error_test"}, errorHandler)
	assert.NoError(t, err)
	defer subscriber.Stop()

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发送消息
	err = pubsub.Publish(ctx, "error_test", "错误测试消息")
	assert.NoError(t, err)

	// 等待重试完成
	time.Sleep(time.Millisecond * 500)

	// 验证重试次数
	mu.Lock()
	finalCount := handlerCallCount
	mu.Unlock()

	assert.GreaterOrEqual(t, finalCount, 3, "应该重试至少3次")
}

func TestSimpleFunctions(t *testing.T) {
	t.Skip("Skipping SimpleFunction test - may cause timeout due to Redis connection issues")

	client := setupRedisClient(t)
	defer client.Close()

	// 测试简单发布
	err := SimplePublish(client, "simple_test", "简单消息")
	assert.NoError(t, err)

	// 测试简单订阅
	received := make(chan string, 1)

	handler := func(ctx context.Context, channel string, message string) error {
		select {
		case received <- message:
		default:
		}
		return nil
	}

	subscriber, err := SimpleSubscribe(client, "simple_sub_test", handler)
	assert.NoError(t, err)
	require.NotNil(t, subscriber)
	defer subscriber.Stop()

	// 等待订阅生效
	time.Sleep(time.Millisecond * 100)

	// 发布消息
	err = SimplePublish(client, "simple_sub_test", "简单订阅测试")
	assert.NoError(t, err)

	// 验证接收
	select {
	case msg := <-received:
		assert.Equal(t, "简单订阅测试", msg)
	case <-time.After(time.Second):
		t.Fatal("未收到消息")
	}
}

// 基准测试
func BenchmarkPubSub_Publish(b *testing.B) {
	client := setupRedisClient(&testing.T{})
	defer client.Close()

	ctx := context.Background()
	pubsub := NewPubSub(client)
	defer pubsub.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			pubsub.Publish(ctx, "bench_channel", fmt.Sprintf("message_%d", i))
			i++
		}
	})
}

func BenchmarkPubSub_PublishJSON(b *testing.B) {
	client := setupRedisClient(&testing.T{})
	defer client.Close()

	ctx := context.Background()
	pubsub := NewPubSub(client)
	defer pubsub.Close()

	type BenchData struct {
		ID      int    `json:"id"`
		Message string `json:"message"`
		Time    int64  `json:"time"`
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			data := BenchData{
				ID:      i,
				Message: fmt.Sprintf("bench message %d", i),
				Time:    time.Now().Unix(),
			}
			PublishJSON[BenchData](pubsub, ctx, "bench_json", data)
			i++
		}
	})
}
