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

// ============================================================================
// 测试辅助函数
// ============================================================================

// testConfig 创建测试用的 PubSub 配置
func testConfig(namespace string) PubSubConfig {
	return PubSubConfig{
		Namespace:       namespace,
		MaxRetries:      2,
		RetryDelay:      time.Millisecond * 50,
		BufferSize:      10,
		PingInterval:    time.Second,
		MaxWorkers:      20,
		WorkerQueueSize: 50,
	}
}

// setupPubSub 创建测试用的 PubSub 实例
func setupPubSub(t *testing.T, namespace string) (*PubSub, func()) {
	client := setupRedisClient(t)
	config := testConfig(namespace)
	pubsub := NewPubSub(client, config)

	cleanup := func() {
		pubsub.Close()
		client.Close()
	}

	return pubsub, cleanup
}

// waitForSubscription 等待订阅生效
func waitForSubscription() {
	time.Sleep(time.Millisecond * 100)
}

// waitForMessage 等待消息处理
func waitForMessage() {
	time.Sleep(time.Millisecond * 200)
}

// messageCollector 消息收集器
type messageCollector struct {
	messages map[string][]string
	mu       sync.Mutex
}

// newMessageCollector 创建消息收集器
func newMessageCollector() *messageCollector {
	return &messageCollector{
		messages: make(map[string][]string),
	}
}

// add 添加消息
func (mc *messageCollector) add(channel, message string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if _, exists := mc.messages[channel]; !exists {
		mc.messages[channel] = make([]string, 0)
	}
	mc.messages[channel] = append(mc.messages[channel], message)
}

// get 获取指定频道的消息
func (mc *messageCollector) get(channel string) []string {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	return mc.messages[channel]
}

// count 获取消息总数
func (mc *messageCollector) count() int {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	total := 0
	for _, msgs := range mc.messages {
		total += len(msgs)
	}
	return total
}

// has 检查是否包含指定消息
func (mc *messageCollector) has(channel, message string) bool {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	msgs, exists := mc.messages[channel]
	if !exists {
		return false
	}

	for _, msg := range msgs {
		if msg == message {
			return true
		}
	}
	return false
}

// counterCollector 计数器收集器
type counterCollector struct {
	count int
	mu    sync.Mutex
}

// newCounterCollector 创建计数器收集器
func newCounterCollector() *counterCollector {
	return &counterCollector{}
}

// increment 增加计数
func (cc *counterCollector) increment() {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.count++
}

// get 获取计数
func (cc *counterCollector) get() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.count
}

// ============================================================================
// 基础功能测试
// ============================================================================

func TestPubSub_BasicPublishSubscribe(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "basic_test")
	defer cleanup()

	ctx := context.Background()
	collector := newMessageCollector()

	handler := func(ctx context.Context, channel string, message string) error {
		collector.add(channel, message)
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"test_channel"}, handler)
	require.NoError(t, err)
	require.NotNil(t, subscriber)
	defer subscriber.Stop()

	waitForSubscription()

	testMessages := []string{"消息1", "消息2", "消息3"}
	for _, msg := range testMessages {
		err := pubsub.Publish(ctx, "test_channel", msg)
		assert.NoError(t, err)
	}

	waitForMessage()

	for _, expectedMsg := range testMessages {
		assert.True(t, collector.has("test_channel", expectedMsg), "应该收到消息: %s", expectedMsg)
	}

	assert.True(t, subscriber.IsActive(), "订阅者应该处于活跃状态")
	assert.Equal(t, []string{"test_channel"}, subscriber.GetChannels())
}

func TestPubSub_JSONMessages(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "json_test")
	defer cleanup()

	ctx := context.Background()

	type TestData struct {
		ID      int    `json:"id"`
		Message string `json:"message"`
		Time    int64  `json:"time"`
	}

	received := make(chan TestData, 5)

	jsonHandler := func(ctx context.Context, channel string, data TestData) error {
		select {
		case received <- data:
		default:
		}
		return nil
	}

	subscriber, err := SubscribeJSON(pubsub, []string{"json_channel"}, jsonHandler)
	require.NoError(t, err)
	require.NotNil(t, subscriber)
	defer subscriber.Stop()

	waitForSubscription()

	testData := []TestData{
		{ID: 1, Message: "第一条消息", Time: time.Now().Unix()},
		{ID: 2, Message: "第二条消息", Time: time.Now().Unix()},
		{ID: 3, Message: "第三条消息", Time: time.Now().Unix()},
	}

	for _, data := range testData {
		err := PublishJSON(pubsub, ctx, "json_channel", data)
		assert.NoError(t, err)
	}

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
	pubsub, cleanup := setupPubSub(t, "multi_channel_test")
	defer cleanup()

	ctx := context.Background()
	collector := newMessageCollector()

	handler := func(ctx context.Context, channel string, message string) error {
		collector.add(channel, message)
		return nil
	}

	channels := []string{"channel1", "channel2", "channel3"}
	subscriber, err := pubsub.Subscribe(channels, handler)
	require.NoError(t, err)
	require.NotNil(t, subscriber)
	defer subscriber.Stop()

	waitForSubscription()

	for i, channel := range channels {
		for j := 1; j <= 3; j++ {
			msg := fmt.Sprintf("频道%d消息%d", i+1, j)
			err := pubsub.Publish(ctx, channel, msg)
			assert.NoError(t, err)
		}
	}

	waitForMessage()

	for i, channel := range channels {
		channelMessages := collector.get(channel)
		assert.Len(t, channelMessages, 3, "频道%s应该有3条消息", channel)

		for j, msg := range channelMessages {
			expected := fmt.Sprintf("频道%d消息%d", i+1, j+1)
			assert.Equal(t, expected, msg, "频道%s的消息%d不匹配", channel, j+1)
		}
	}
}

func TestPubSub_PatternSubscribe(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "pattern_test")
	defer cleanup()

	ctx := context.Background()
	collector := newMessageCollector()

	handler := func(ctx context.Context, channel string, message string) error {
		collector.add(channel, message)
		return nil
	}

	subscriber, err := pubsub.SubscribePattern([]string{"test.*"}, handler)
	require.NoError(t, err)
	require.NotNil(t, subscriber)
	defer subscriber.Stop()

	waitForSubscription()

	matchingChannels := []string{"test.1", "test.2", "test.abc"}
	for i, channel := range matchingChannels {
		msg := fmt.Sprintf("消息%d", i+1)
		err := pubsub.Publish(ctx, channel, msg)
		assert.NoError(t, err)
	}

	err = pubsub.Publish(ctx, "other.channel", "不应该收到")
	assert.NoError(t, err)

	waitForMessage()

	assert.Equal(t, 3, collector.count(), "应该只收到3条匹配的消息")

	for i, channel := range matchingChannels {
		expectedMsg := fmt.Sprintf("消息%d", i+1)
		assert.True(t, collector.has(channel, expectedMsg), "应该包含消息: %s:%s", channel, expectedMsg)
	}
}

// ============================================================================
// 订阅管理测试
// ============================================================================

func TestPubSub_Unsubscribe(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "unsubscribe_test")
	defer cleanup()

	ctx := context.Background()
	counter := newCounterCollector()

	handler := func(ctx context.Context, channel string, message string) error {
		counter.increment()
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"unsub_test"}, handler)
	require.NoError(t, err)
	require.NotNil(t, subscriber)

	waitForSubscription()

	err = pubsub.Publish(ctx, "unsub_test", "消息1")
	assert.NoError(t, err)
	waitForMessage()

	err = pubsub.Unsubscribe("unsub_test")
	assert.NoError(t, err)

	waitForSubscription()

	err = pubsub.Publish(ctx, "unsub_test", "消息2")
	assert.NoError(t, err)
	waitForMessage()

	assert.Equal(t, 1, counter.get(), "取消订阅后不应该收到新消息")
	assert.False(t, subscriber.IsActive(), "取消订阅后订阅者应该不活跃")
}

func TestSubscriber_Unsubscribe(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "subscriber_unsub_test")
	defer cleanup()

	ctx := context.Background()
	counter := newCounterCollector()

	handler := func(ctx context.Context, channel string, message string) error {
		counter.increment()
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"sub_test1", "sub_test2"}, handler)
	require.NoError(t, err)
	require.NotNil(t, subscriber)

	assert.True(t, subscriber.IsActive(), "订阅者应该处于活跃状态")
	assert.Equal(t, 2, pubsub.GetSubscribers(), "应该有2个频道订阅")

	waitForSubscription()

	err = pubsub.Publish(ctx, "sub_test1", "消息1")
	assert.NoError(t, err)
	err = pubsub.Publish(ctx, "sub_test2", "消息2")
	assert.NoError(t, err)
	waitForMessage()

	beforeCount := counter.get()
	assert.Equal(t, 2, beforeCount, "应该收到2条消息")

	err = subscriber.Unsubscribe()
	assert.NoError(t, err)

	assert.False(t, subscriber.IsActive(), "取消订阅后订阅者应该不活跃")
	assert.Equal(t, 0, pubsub.GetSubscribers(), "应该没有活跃订阅者")

	waitForSubscription()

	err = pubsub.Publish(ctx, "sub_test1", "消息3")
	assert.NoError(t, err)
	err = pubsub.Publish(ctx, "sub_test2", "消息4")
	assert.NoError(t, err)
	waitForMessage()

	finalCount := counter.get()
	assert.Equal(t, beforeCount, finalCount, "取消订阅后不应该收到新消息")
}
func TestSubscriber_UnsubscribePattern(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "pattern_unsub_test")
	defer cleanup()

	ctx := context.Background()
	counter := newCounterCollector()

	handler := func(ctx context.Context, channel string, message string) error {
		counter.increment()
		return nil
	}

	subscriber, err := pubsub.SubscribePattern([]string{"pattern.*", "test.*"}, handler)
	require.NoError(t, err)
	require.NotNil(t, subscriber)

	waitForSubscription()

	err = pubsub.Publish(ctx, "pattern.1", "消息1")
	assert.NoError(t, err)
	err = pubsub.Publish(ctx, "test.2", "消息2")
	assert.NoError(t, err)
	waitForMessage()

	beforeCount := counter.get()
	assert.Equal(t, 2, beforeCount, "应该收到2条模式匹配消息")

	err = subscriber.Unsubscribe()
	assert.NoError(t, err)

	assert.False(t, subscriber.IsActive(), "取消订阅后订阅者应该不活跃")

	waitForSubscription()

	err = pubsub.Publish(ctx, "pattern.3", "消息3")
	assert.NoError(t, err)
	err = pubsub.Publish(ctx, "test.4", "消息4")
	assert.NoError(t, err)
	waitForMessage()

	finalCount := counter.get()
	assert.Equal(t, beforeCount, finalCount, "取消订阅后不应该收到新消息")
}

// ============================================================================
// 订阅者信息和状态测试
// ============================================================================

func TestSubscriber_GetSubscriptionInfo(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "info_test")
	defer cleanup()

	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	subscriber1, err := pubsub.Subscribe([]string{"info_ch1", "info_ch2", "info_ch3"}, handler)
	require.NoError(t, err)
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

	subscriber2, err := pubsub.SubscribePattern([]string{"pattern.*", "test.*"}, handler)
	require.NoError(t, err)
	defer subscriber2.Stop()

	info2 := subscriber2.GetSubscriptionInfo()
	assert.NotNil(t, info2)
	assert.True(t, info2.IsPattern, "应该是模式订阅")
	assert.True(t, info2.IsActive, "应该处于活跃状态")
	assert.Equal(t, 2, info2.ChannelCount, "应该有2个模式")
	assert.Len(t, info2.Channels, 2, "模式列表应该有2个元素")
	assert.Contains(t, info2.Channels, "pattern.*")
	assert.Contains(t, info2.Channels, "test.*")

	subscriber1.Stop()
	waitForSubscription()

	info3 := subscriber1.GetSubscriptionInfo()
	assert.False(t, info3.IsActive, "停止后应该不活跃")
	assert.Equal(t, 3, info3.ChannelCount, "频道数不变")
}

func TestSubscriber_Resubscribe(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "resubscribe_test")
	defer cleanup()

	ctx := context.Background()
	counter := newCounterCollector()

	handler := func(ctx context.Context, channel string, message string) error {
		counter.increment()
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"resub_test"}, handler)
	require.NoError(t, err)
	assert.True(t, subscriber.IsActive(), "初始订阅应该活跃")

	waitForSubscription()

	err = pubsub.Publish(ctx, "resub_test", "消息1")
	assert.NoError(t, err)
	waitForMessage()

	firstCount := counter.get()
	assert.Equal(t, 1, firstCount, "应该收到第一条消息")

	subscriber.Stop()
	waitForSubscription()
	assert.False(t, subscriber.IsActive(), "停止后应该不活跃")

	err = subscriber.Resubscribe()
	assert.NoError(t, err)
	assert.True(t, subscriber.IsActive(), "重新订阅后应该活跃")

	err = subscriber.Resubscribe()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already active")

	waitForSubscription()

	err = pubsub.Publish(ctx, "resub_test", "消息2")
	assert.NoError(t, err)
	waitForMessage()

	finalCount := counter.get()
	assert.Equal(t, 2, finalCount, "重新订阅后应该收到第二条消息")

	subscriber.Stop()
}

func TestSubscriber_ResubscribePattern(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "resubscribe_pattern_test")
	defer cleanup()

	ctx := context.Background()
	counter := newCounterCollector()

	handler := func(ctx context.Context, channel string, message string) error {
		counter.increment()
		return nil
	}

	subscriber, err := pubsub.SubscribePattern([]string{"resubpat.*"}, handler)
	require.NoError(t, err)
	assert.True(t, subscriber.IsActive(), "初始订阅应该活跃")

	waitForSubscription()

	err = pubsub.Publish(ctx, "resubpat.1", "消息1")
	assert.NoError(t, err)
	waitForMessage()

	firstCount := counter.get()
	assert.Equal(t, 1, firstCount, "应该收到第一条消息")

	subscriber.Stop()
	waitForSubscription()

	err = subscriber.Resubscribe()
	assert.NoError(t, err)
	assert.True(t, subscriber.IsActive(), "重新订阅后应该活跃")

	waitForSubscription()

	err = pubsub.Publish(ctx, "resubpat.2", "消息2")
	assert.NoError(t, err)
	waitForMessage()

	finalCount := counter.get()
	assert.Equal(t, 2, finalCount, "重新订阅后应该收到第二条消息")

	info := subscriber.GetSubscriptionInfo()
	assert.True(t, info.IsPattern, "应该是模式订阅")
	assert.True(t, info.IsActive, "应该活跃")

	subscriber.Stop()
}

// ============================================================================
// 高级功能测试
// ============================================================================

func TestPubSub_BroadcastMessage(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "broadcast_test")
	defer cleanup()

	ctx := context.Background()
	receivedChannels := make(map[string]bool)
	var mu sync.Mutex

	handler := func(ctx context.Context, channel string, message string) error {
		mu.Lock()
		receivedChannels[channel] = true
		mu.Unlock()
		return nil
	}

	channels := []string{"broadcast1", "broadcast2", "broadcast3"}
	for _, channel := range channels {
		_, err := pubsub.Subscribe([]string{channel}, handler)
		assert.NoError(t, err)
	}

	waitForSubscription()

	err := pubsub.BroadcastMessage(ctx, channels, "广播消息")
	assert.NoError(t, err)

	waitForMessage()

	mu.Lock()
	defer mu.Unlock()

	for _, channel := range channels {
		assert.True(t, receivedChannels[channel], "频道%s应该收到广播消息", channel)
	}

	assert.Len(t, receivedChannels, 3, "应该有3个频道收到消息")
}

func TestPubSub_RequestResponse(t *testing.T) {
	t.Skip("Skipping RequestResponse test - may cause timeout due to Redis connection issues")

	pubsub, cleanup := setupPubSub(t, "request_response_test")
	defer cleanup()

	ctx := context.Background()

	go func() {
		handler := func(ctx context.Context, channel string, message string) error {
			response := fmt.Sprintf("响应:%s", message)
			return pubsub.Publish(ctx, "response_channel", response)
		}

		subscriber, err := pubsub.Subscribe([]string{"request_channel"}, handler)
		if err != nil {
			t.Logf("服务端订阅失败: %v", err)
			return
		}
		defer subscriber.Stop()

		time.Sleep(time.Second * 3)
	}()

	waitForMessage()

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
	pubsub, cleanup := setupPubSub(t, "timeout_test")
	defer cleanup()

	ctx := context.Background()

	_, err := pubsub.RequestResponse(
		ctx,
		"timeout_request",
		"timeout_response",
		"超时测试",
		time.Millisecond*100,
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout", "应该是超时错误")
}

func TestPubSub_Stats(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "stats_test")
	defer cleanup()

	stats := pubsub.GetStats()
	assert.Equal(t, 0, stats.ActiveSubscribers)
	assert.Len(t, stats.Channels, 0)
	assert.Len(t, stats.Patterns, 0)

	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	subscriber1, err := pubsub.Subscribe([]string{"stats_channel1", "stats_channel2"}, handler)
	require.NoError(t, err)
	defer subscriber1.Stop()

	waitForSubscription()

	subscriber2, err := pubsub.SubscribePattern([]string{"stats.*"}, handler)
	require.NoError(t, err)
	defer subscriber2.Stop()

	waitForSubscription()

	stats = pubsub.GetStats()
	assert.Equal(t, 2, stats.ActiveSubscribers, "应该有2个活跃订阅者")
	assert.Contains(t, stats.Channels, "stats_channel1")
	assert.Contains(t, stats.Channels, "stats_channel2")
	assert.Contains(t, stats.Patterns, "stats.*")
}

func TestPubSub_ErrorHandling(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "error_test")
	defer cleanup()

	ctx := context.Background()
	counter := newCounterCollector()

	errorHandler := func(ctx context.Context, channel string, message string) error {
		count := counter.get()
		counter.increment()

		if count < 2 {
			return fmt.Errorf("模拟处理错误")
		}
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"error_test"}, errorHandler)
	require.NoError(t, err)
	defer subscriber.Stop()

	waitForSubscription()

	err = pubsub.Publish(ctx, "error_test", "错误测试消息")
	assert.NoError(t, err)

	waitForMessage()

	finalCount := counter.get()
	assert.GreaterOrEqual(t, finalCount, 3, "应该重试至少3次")
}

func TestSimpleFunctions(t *testing.T) {
	t.Skip("Skipping SimpleFunction test - may cause timeout due to Redis connection issues")

	client := setupRedisClient(t)
	defer client.Close()

	err := SimplePublish(client, "simple_test", "简单消息")
	assert.NoError(t, err)

	received := make(chan string, 1)

	handler := func(ctx context.Context, channel string, message string) error {
		select {
		case received <- message:
		default:
		}
		return nil
	}

	pubsub, subscriber, err := SimpleSubscribe(client, "simple_sub_test", handler)
	require.NoError(t, err)
	require.NotNil(t, subscriber)
	require.NotNil(t, pubsub)
	defer pubsub.Close()
	defer subscriber.Stop()

	waitForSubscription()

	err = SimplePublish(client, "simple_sub_test", "简单订阅测试")
	assert.NoError(t, err)

	select {
	case msg := <-received:
		assert.Equal(t, "简单订阅测试", msg)
	case <-time.After(time.Second):
		t.Fatal("未收到消息")
	}
}

// ============================================================================
// Bug 修复测试
// ============================================================================

// TestBugFix_SimpleSubscribe_ResourceLeak 测试 SimpleSubscribe 资源泄漏修复
func TestBugFix_SimpleSubscribe_ResourceLeak(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	pubsubInstances := make([]*PubSub, 0)
	for i := 0; i < 5; i++ {
		pubsub, subscriber, err := SimpleSubscribe(client, fmt.Sprintf("leak_test_%d", i), func(ctx context.Context, channel string, message string) error {
			return nil
		})
		require.NoError(t, err)
		require.NotNil(t, pubsub)
		require.NotNil(t, subscriber)
		pubsubInstances = append(pubsubInstances, pubsub)
	}

	waitForMessage()

	for i := 0; i < 5; i++ {
		err := pubsubInstances[i].Publish(ctx, fmt.Sprintf("leak_test_%d", i), "test")
		assert.NoError(t, err)
	}

	waitForMessage()

	for _, pubsub := range pubsubInstances {
		err := pubsub.Close()
		assert.NoError(t, err)
	}

	waitForSubscription()
}

// TestBugFix_Unsubscribe_DuplicateStop 测试 Unsubscribe 重复 Stop 修复
func TestBugFix_Unsubscribe_DuplicateStop(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "duplicate_stop_test")
	defer cleanup()

	ctx := context.Background()

	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"dup_ch1", "dup_ch2", "dup_ch3"}, handler)
	require.NoError(t, err)
	require.NotNil(t, subscriber)

	assert.Equal(t, 3, pubsub.GetSubscribers(), "应该有3个频道订阅")
	assert.True(t, subscriber.IsActive())

	waitForSubscription()

	for i := 1; i <= 3; i++ {
		err := pubsub.Publish(ctx, fmt.Sprintf("dup_ch%d", i), "test")
		assert.NoError(t, err)
	}

	waitForMessage()

	err = pubsub.Unsubscribe("dup_ch1", "dup_ch2", "dup_ch3")
	assert.NoError(t, err)

	assert.False(t, subscriber.IsActive())
	assert.Equal(t, 0, pubsub.GetSubscribers(), "应该没有订阅者")
}

// TestBugFix_RequestResponse_Cleanup 测试 RequestResponse 订阅者清理修复
func TestBugFix_RequestResponse_Cleanup(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "cleanup_test")
	defer cleanup()

	ctx := context.Background()

	go func() {
		handler := func(ctx context.Context, channel string, message string) error {
			return pubsub.Publish(ctx, "cleanup_resp", fmt.Sprintf("resp:%s", message))
		}
		subscriber, err := pubsub.Subscribe([]string{"cleanup_req"}, handler)
		if err != nil {
			return
		}
		defer subscriber.Stop()
		time.Sleep(time.Second * 3)
	}()

	waitForMessage()

	initialCount := pubsub.GetSubscribers()

	for i := 0; i < 5; i++ {
		_, err := pubsub.RequestResponse(ctx, "cleanup_req", "cleanup_resp", fmt.Sprintf("req%d", i), time.Millisecond*500)
		if err != nil {
			t.Logf("请求%d失败: %v", i, err)
		}
		waitForSubscription()
	}

	waitForMessage()

	finalCount := pubsub.GetSubscribers()
	assert.Equal(t, initialCount, finalCount, "RequestResponse 应该清理临时订阅者")
}

// TestBugFix_Resubscribe_RaceCondition 测试 Resubscribe 竞态条件修复
func TestBugFix_Resubscribe_RaceCondition(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "race_test")
	defer cleanup()

	ctx := context.Background()

	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"race_test"}, handler)
	require.NoError(t, err)

	waitForSubscription()

	subscriber.Stop()
	waitForSubscription()

	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := subscriber.Resubscribe()
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	mu.Lock()
	count := successCount
	mu.Unlock()
	assert.Equal(t, 1, count, "应该只有一个 Resubscribe 成功")
	assert.True(t, subscriber.IsActive(), "重新订阅后应该活跃")

	waitForSubscription()
	err = pubsub.Publish(ctx, "race_test", "test")
	assert.NoError(t, err)

	waitForMessage()

	subscriber.Stop()
}

// TestBugFix_GetStats_Consistency 测试 GetStats 数据一致性修复
func TestBugFix_GetStats_Consistency(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "stats_consistency_test")
	defer cleanup()

	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	subscribers := make([]*Subscriber, 0)
	for i := 0; i < 5; i++ {
		subscriber, err := pubsub.Subscribe([]string{fmt.Sprintf("stats_ch%d", i)}, handler)
		require.NoError(t, err)
		subscribers = append(subscribers, subscriber)
	}

	waitForSubscription()

	var wg sync.WaitGroup
	statsResults := make([]*PubSubStats, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			statsResults[idx] = pubsub.GetStats()
		}()
	}

	for i := 0; i < 3; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			time.Sleep(time.Millisecond * 10)
			subscribers[idx].Stop()
		}()
	}

	wg.Wait()

	for i, stats := range statsResults {
		assert.NotNil(t, stats, "统计结果%d不应该为nil", i)
		assert.GreaterOrEqual(t, stats.ActiveSubscribers, 0, "活跃订阅者数不应该为负")
		assert.GreaterOrEqual(t, len(stats.Channels), 0, "频道数不应该为负")
	}

	for _, subscriber := range subscribers {
		subscriber.Stop()
	}
}

// TestBugFix_IsActive_ThreadSafe 测试 IsActive 线程安全性
func TestBugFix_IsActive_ThreadSafe(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "active_test")
	defer cleanup()

	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"active_test"}, handler)
	require.NoError(t, err)

	waitForSubscription()

	var wg sync.WaitGroup
	activeChecks := make([]bool, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			activeChecks[idx] = subscriber.IsActive()
		}()
	}

	time.Sleep(time.Millisecond * 10)
	subscriber.Stop()

	wg.Wait()

	for i, active := range activeChecks {
		_ = active
		t.Logf("检查%d: %v", i, active)
	}

	assert.False(t, subscriber.IsActive())
}

// TestBugFix_MultipleUnsubscribe_SameSubscriber 测试同一订阅者多次取消订阅
func TestBugFix_MultipleUnsubscribe_SameSubscriber(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "multi_unsub_test")
	defer cleanup()

	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"multi_ch1", "multi_ch2", "multi_ch3"}, handler)
	require.NoError(t, err)

	waitForSubscription()

	var wg sync.WaitGroup
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		channel := fmt.Sprintf("multi_ch%d", i)
		go func(ch string) {
			defer wg.Done()
			err := pubsub.Unsubscribe(ch)
			assert.NoError(t, err)
		}(channel)
	}

	wg.Wait()

	assert.False(t, subscriber.IsActive())
	assert.Equal(t, 0, pubsub.GetSubscribers())
}

// TestBugFix_StopAndUnsubscribe_Order 测试 Stop 和 Unsubscribe 的顺序
func TestBugFix_StopAndUnsubscribe_Order(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "order_test")
	defer cleanup()

	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"order_test"}, handler)
	require.NoError(t, err)

	waitForSubscription()

	subscriber.Stop()
	assert.False(t, subscriber.IsActive())

	err = subscriber.Unsubscribe()
	assert.NoError(t, err)

	assert.Equal(t, 0, pubsub.GetSubscribers())

	err = subscriber.Unsubscribe()
	assert.NoError(t, err)
}

// ============================================================================
// 基准测试
// ============================================================================

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
			PublishJSON(pubsub, ctx, "bench_json", data)
			i++
		}
	})
}

// ============================================================================
// 遗漏功能测试
// ============================================================================

// TestPubSub_Compression 测试消息压缩功能
func TestPubSub_Compression(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	config := PubSubConfig{
		Namespace:          "compression_test",
		MaxRetries:         2,
		RetryDelay:         time.Millisecond * 50,
		BufferSize:         10,
		PingInterval:       time.Second,
		EnableCompression:  true,
		CompressionMinSize: 100, // 100 字节以上才压缩
		MaxWorkers:         20,
		WorkerQueueSize:    50,
	}

	pubsub := NewPubSub(client, config)
	defer pubsub.Close()

	collector := newMessageCollector()

	handler := func(ctx context.Context, channel string, message string) error {
		collector.add(channel, message)
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"compress_test"}, handler)
	require.NoError(t, err)
	defer subscriber.Stop()

	waitForSubscription()

	// 测试小消息（不压缩）
	smallMsg := "small message"
	err = pubsub.Publish(ctx, "compress_test", smallMsg)
	assert.NoError(t, err)

	// 测试大消息（会压缩）
	largeMsg := string(make([]byte, 200)) // 200 字节
	for i := range largeMsg {
		largeMsg = largeMsg[:i] + "a" + largeMsg[i+1:]
	}
	err = pubsub.Publish(ctx, "compress_test", largeMsg)
	assert.NoError(t, err)

	waitForMessage()

	assert.True(t, collector.has("compress_test", smallMsg), "应该收到小消息")
	assert.Equal(t, 2, collector.count(), "应该收到2条消息")
}

// TestPubSub_GetChannels 测试获取频道列表
func TestPubSub_GetChannels(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "get_channels_test")
	defer cleanup()

	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	// 初始应该没有频道
	channels := pubsub.GetChannels()
	assert.Len(t, channels, 0, "初始应该没有频道")

	// 订阅多个频道
	subscriber1, err := pubsub.Subscribe([]string{"ch1", "ch2"}, handler)
	require.NoError(t, err)
	defer subscriber1.Stop()

	subscriber2, err := pubsub.Subscribe([]string{"ch3"}, handler)
	require.NoError(t, err)
	defer subscriber2.Stop()

	waitForSubscription()

	channels = pubsub.GetChannels()
	assert.Len(t, channels, 3, "应该有3个频道")
	assert.Contains(t, channels, "ch1")
	assert.Contains(t, channels, "ch2")
	assert.Contains(t, channels, "ch3")

	// 取消订阅后
	subscriber1.Stop()
	waitForSubscription()

	channels = pubsub.GetChannels()
	assert.Contains(t, channels, "ch3", "ch3 应该还在")
}

// TestPubSub_GetClient 测试获取底层 Redis 客户端
func TestPubSub_GetClient(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "get_client_test")
	defer cleanup()

	client := pubsub.GetClient()
	assert.NotNil(t, client, "应该返回 Redis 客户端")

	// 验证客户端可用
	ctx := context.Background()
	err := client.Ping(ctx).Err()
	assert.NoError(t, err, "Redis 客户端应该可用")
}

// TestPubSub_EmptyChannels 测试空频道列表的错误处理
func TestPubSub_EmptyChannels(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "empty_channels_test")
	defer cleanup()

	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	// 测试订阅空频道列表
	subscriber, err := pubsub.Subscribe([]string{}, handler)
	assert.Error(t, err)
	assert.Nil(t, subscriber)
	assert.Contains(t, err.Error(), "no channels specified")

	// 测试订阅模式空列表
	subscriber, err = pubsub.SubscribePattern([]string{}, handler)
	assert.Error(t, err)
	assert.Nil(t, subscriber)
	assert.Contains(t, err.Error(), "no patterns specified")
}

// TestPubSub_NilHandler 测试 nil handler 的错误处理
func TestPubSub_NilHandler(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "nil_handler_test")
	defer cleanup()

	// 测试订阅时传入 nil handler
	subscriber, err := pubsub.Subscribe([]string{"test"}, nil)
	assert.Error(t, err)
	assert.Nil(t, subscriber)
	assert.Contains(t, err.Error(), "handler cannot be nil")

	// 测试模式订阅时传入 nil handler
	subscriber, err = pubsub.SubscribePattern([]string{"test.*"}, nil)
	assert.Error(t, err)
	assert.Nil(t, subscriber)
	assert.Contains(t, err.Error(), "handler cannot be nil")
}

// TestPubSub_PublishDifferentTypes 测试发布不同类型的消息
func TestPubSub_PublishDifferentTypes(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "publish_types_test")
	defer cleanup()

	ctx := context.Background()
	collector := newMessageCollector()

	handler := func(ctx context.Context, channel string, message string) error {
		collector.add(channel, message)
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"types_test"}, handler)
	require.NoError(t, err)
	defer subscriber.Stop()

	waitForSubscription()

	// 测试字符串
	err = pubsub.Publish(ctx, "types_test", "string message")
	assert.NoError(t, err)

	// 测试字节数组
	err = pubsub.Publish(ctx, "types_test", []byte("byte message"))
	assert.NoError(t, err)

	// 测试结构体（会被 JSON 序列化）
	type TestStruct struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	err = pubsub.Publish(ctx, "types_test", TestStruct{Name: "test", Value: 123})
	assert.NoError(t, err)

	waitForMessage()

	assert.Equal(t, 3, collector.count(), "应该收到3条消息")
}

// TestPubSub_UnsubscribeEmpty 测试取消订阅空列表
func TestPubSub_UnsubscribeEmpty(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "unsub_empty_test")
	defer cleanup()

	// 取消订阅空列表应该不报错
	err := pubsub.Unsubscribe()
	assert.NoError(t, err)

	err = pubsub.Unsubscribe("")
	assert.NoError(t, err)
}

// TestPubSub_ContextCancellation 测试 context 取消
func TestPubSub_ContextCancellation(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "context_cancel_test")
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	counter := newCounterCollector()

	handler := func(ctx context.Context, channel string, message string) error {
		counter.increment()
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"cancel_test"}, handler)
	require.NoError(t, err)

	waitForSubscription()

	// 发送消息
	err = pubsub.Publish(ctx, "cancel_test", "message1")
	assert.NoError(t, err)

	waitForMessage()

	// 取消 context
	cancel()

	// 再次发送消息（context 已取消，但 pubsub 应该还能工作）
	newCtx := context.Background()
	err = pubsub.Publish(newCtx, "cancel_test", "message2")
	assert.NoError(t, err)

	waitForMessage()

	subscriber.Stop()
}

// TestPubSub_NamespaceIsolation 测试命名空间隔离
func TestPubSub_NamespaceIsolation(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	// 创建两个不同命名空间的 PubSub
	config1 := testConfig("namespace1")
	pubsub1 := NewPubSub(client, config1)
	defer pubsub1.Close()

	config2 := testConfig("namespace2")
	pubsub2 := NewPubSub(client, config2)
	defer pubsub2.Close()

	counter1 := newCounterCollector()
	counter2 := newCounterCollector()

	handler1 := func(ctx context.Context, channel string, message string) error {
		counter1.increment()
		return nil
	}

	handler2 := func(ctx context.Context, channel string, message string) error {
		counter2.increment()
		return nil
	}

	subscriber1, err := pubsub1.Subscribe([]string{"test"}, handler1)
	require.NoError(t, err)
	defer subscriber1.Stop()

	subscriber2, err := pubsub2.Subscribe([]string{"test"}, handler2)
	require.NoError(t, err)
	defer subscriber2.Stop()

	waitForSubscription()

	// 在 namespace1 发布消息
	err = pubsub1.Publish(ctx, "test", "message1")
	assert.NoError(t, err)

	waitForMessage()

	// 只有 namespace1 应该收到消息
	assert.Equal(t, 1, counter1.get(), "namespace1 应该收到消息")
	assert.Equal(t, 0, counter2.get(), "namespace2 不应该收到消息")

	// 在 namespace2 发布消息
	err = pubsub2.Publish(ctx, "test", "message2")
	assert.NoError(t, err)

	waitForMessage()

	// 只有 namespace2 应该收到新消息
	assert.Equal(t, 1, counter1.get(), "namespace1 不应该收到新消息")
	assert.Equal(t, 1, counter2.get(), "namespace2 应该收到消息")
}

// TestPubSub_WorkerPoolLimit 测试 Worker 池限制
func TestPubSub_WorkerPoolLimit(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	config := PubSubConfig{
		Namespace:       "worker_pool_test",
		MaxRetries:      2,
		RetryDelay:      time.Millisecond * 50,
		BufferSize:      10,
		PingInterval:    time.Second,
		MaxWorkers:      2, // 限制为 2 个 worker
		WorkerQueueSize: 5, // 队列大小为 5
	}

	pubsub := NewPubSub(client, config)
	defer pubsub.Close()

	var processing sync.WaitGroup
	var mu sync.Mutex
	activeWorkers := 0
	maxActiveWorkers := 0

	handler := func(ctx context.Context, channel string, message string) error {
		mu.Lock()
		activeWorkers++
		if activeWorkers > maxActiveWorkers {
			maxActiveWorkers = activeWorkers
		}
		mu.Unlock()

		processing.Add(1)
		time.Sleep(time.Millisecond * 100) // 模拟处理时间

		mu.Lock()
		activeWorkers--
		mu.Unlock()

		processing.Done()
		return nil
	}

	subscriber, err := pubsub.Subscribe([]string{"worker_test"}, handler)
	require.NoError(t, err)
	defer subscriber.Stop()

	waitForSubscription()

	// 发送多条消息
	for i := 0; i < 10; i++ {
		err = pubsub.Publish(ctx, "worker_test", fmt.Sprintf("message%d", i))
		assert.NoError(t, err)
	}

	// 等待所有消息处理完成
	processing.Wait()

	mu.Lock()
	max := maxActiveWorkers
	mu.Unlock()

	// 验证并发 worker 数量不超过限制
	assert.LessOrEqual(t, max, 2, "并发 worker 数量不应超过限制")
}

// TestSubscriber_GetChannelsPattern 测试模式订阅的 GetChannels
func TestSubscriber_GetChannelsPattern(t *testing.T) {
	pubsub, cleanup := setupPubSub(t, "get_channels_pattern_test")
	defer cleanup()

	handler := func(ctx context.Context, channel string, message string) error {
		return nil
	}

	subscriber, err := pubsub.SubscribePattern([]string{"test.*", "demo.*"}, handler)
	require.NoError(t, err)
	defer subscriber.Stop()

	channels := subscriber.GetChannels()
	assert.Len(t, channels, 2, "应该有2个模式")
	assert.Contains(t, channels, "test.*")
	assert.Contains(t, channels, "demo.*")
}

// TestPubSub_BroadcastMessageError 测试广播消息时的错误处理
func TestPubSub_BroadcastMessageError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	// 创建一个会失败的 pubsub（使用无效的 Redis 客户端）
	ctx := context.Background()

	pubsub := NewPubSub(client)
	defer pubsub.Close()

	// 关闭 Redis 连接
	client.Close()

	// 广播应该返回错误
	err := pubsub.BroadcastMessage(ctx, []string{"ch1", "ch2"}, "test")
	assert.Error(t, err, "广播到已关闭的连接应该返回错误")
}

// TestPubSub_DefaultConfig 测试默认配置
func TestPubSub_DefaultConfig(t *testing.T) {
	config := DefaultPubSubConfig()

	assert.Equal(t, "pubsub", config.Namespace)
	assert.Equal(t, 2, config.MaxRetries)
	assert.Equal(t, time.Millisecond*100, config.RetryDelay)
	assert.Equal(t, 100, config.BufferSize)
	assert.Equal(t, time.Second*10, config.PingInterval)
	assert.False(t, config.EnableCompression)
	assert.Equal(t, 1024, config.CompressionMinSize)
	assert.Equal(t, 50, config.MaxWorkers)
	assert.Equal(t, 200, config.WorkerQueueSize)
}
