/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-19 00:00:00
 * @FilePath: \go-cachex\pubsub.go
 * @Description: Redis 发布订阅功能封装，提供傻瓜式调用接口
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/mathx"
	"github.com/kamalyes/go-toolbox/pkg/retry"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
	"github.com/kamalyes/go-toolbox/pkg/zipx"
	"github.com/redis/go-redis/v9"
)

// MessageHandler 消息处理器接口
type MessageHandler func(ctx context.Context, channel string, message string) error

// TypedMessageHandler 泛型消息处理器
type TypedMessageHandler[T any] func(ctx context.Context, channel string, message T) error

// 压缩相关常量（使用 zipx 的常量）
const (
	CompressionPrefix    = zipx.GzipPrefix    // 压缩消息前缀标记
	CompressionPrefixLen = zipx.GzipPrefixLen // 压缩前缀长度
)

// PubSubConfig 发布订阅配置
type PubSubConfig struct {
	Namespace          string         // 命名空间
	MaxRetries         int            // 最大重试次数
	RetryDelay         time.Duration  // 重试延迟
	BufferSize         int            // 消息缓冲区大小
	Logger             logger.ILogger // 日志记录器（可选，不设置则使用 NoOpLogger）
	PingInterval       time.Duration  // 心跳间隔
	EnableCompression  bool           // 是否启用消息压缩（使用 gzip）
	CompressionMinSize int            // 压缩阈值（字节），小于此值不压缩，默认 1KB
}

// DefaultPubSubConfig 默认配置
func DefaultPubSubConfig() PubSubConfig {
	return PubSubConfig{
		Namespace:          "pubsub",
		MaxRetries:         2,                      // 减少重试次数
		RetryDelay:         time.Millisecond * 100, // 大幅减少重试延迟
		BufferSize:         100,
		Logger:             NewDefaultCachexLogger(), // 默认使用空日志器
		PingInterval:       time.Second * 10,         // 减少心跳间隔
		EnableCompression:  false,                    // 默认关闭压缩
		CompressionMinSize: 1024,                     // 默认1KB以上才压缩
	}
}

// PubSub Redis发布订阅封装
type PubSub struct {
	client      *redis.Client
	config      PubSubConfig
	subscribers map[string]*Subscriber
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	logger      logger.ILogger
}

// NewPubSub 创建发布订阅实例
func NewPubSub(redisClient redis.UniversalClient, config ...PubSubConfig) *PubSub {
	// 类型断言为*redis.Client
	client, ok := redisClient.(*redis.Client)
	if !ok {
		panic("PubSub requires *redis.Client, cluster mode not supported yet")
	}

	cfg := DefaultPubSubConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &PubSub{
		client:      client,
		config:      cfg,
		subscribers: make(map[string]*Subscriber),
		ctx:         ctx,
		cancel:      cancel,
		logger:      mathx.IfEmpty(cfg.Logger, NewDefaultCachexLogger()),
	}
}

// getChannelKey 获取带命名空间的频道名
func (p *PubSub) getChannelKey(channel string) string {
	if p.config.Namespace == "" {
		return channel
	}
	return fmt.Sprintf("%s:%s", p.config.Namespace, channel)
}

// Publish 发布消息
func (p *PubSub) Publish(ctx context.Context, channel string, message interface{}) error {
	var data string

	switch v := message.(type) {
	case string:
		data = v
	case []byte:
		data = string(v)
	default:
		// JSON序列化
		jsonData, err := json.Marshal(message)
		if err != nil {
			return fmt.Errorf("failed to marshal message: %w", err)
		}
		data = string(jsonData)
	}

	// 如果启用压缩且消息超过阈值，则压缩
	if p.config.EnableCompression && len(data) >= p.config.CompressionMinSize {
		compressed, err := zipx.GzipCompressWithPrefix([]byte(data))
		if err != nil {
			p.logger.Warnf("Failed to compress message: %v, sending uncompressed", err)
		} else {
			originalLen := len(data)
			data = string(compressed)
			p.logger.Debugf("Compressed message from %d to %d bytes (%.1f%% reduction)",
				originalLen, len(compressed)-CompressionPrefixLen,
				100.0*(1-float64(len(compressed)-CompressionPrefixLen)/float64(originalLen)))
		}
	}

	channelKey := p.getChannelKey(channel)

	// 使用 retry 包重试发布
	retrier := retry.NewRetryWithCtx(ctx).
		SetAttemptCount(p.config.MaxRetries + 1).
		SetInterval(p.config.RetryDelay).
		SetCaller(fmt.Sprintf("PubSub.Publish(%s)", channel))

	retrier.SetErrCallback(func(nowAttemptCount, remainCount int, err error, funcName ...string) {
		p.logger.Warnf("Publish attempt %d failed for channel %s: %v", nowAttemptCount, channel, err)
	}).SetSuccessCallback(func(funcName ...string) {
		p.logger.Debugf("Publish succeeded for channel %s", channel)
	})

	return retrier.Do(func() error {
		return p.client.Publish(ctx, channelKey, data).Err()
	})
}

// PublishJSON 发布JSON消息（泛型版本）
func PublishJSON[T any](p *PubSub, ctx context.Context, channel string, message T) error {
	return p.Publish(ctx, channel, message)
}

// Subscribe 订阅频道
func (p *PubSub) Subscribe(channels []string, handler MessageHandler) (*Subscriber, error) {
	if len(channels) == 0 {
		return nil, fmt.Errorf("no channels specified")
	}
	if handler == nil {
		return nil, fmt.Errorf("handler cannot be nil")
	}

	// 转换频道名
	channelKeys := make([]string, len(channels))
	for i, channel := range channels {
		channelKeys[i] = p.getChannelKey(channel)
	}

	// 创建订阅者
	subscriber := &Subscriber{
		pubsub:      p,
		channels:    channels,
		channelKeys: channelKeys,
		handler:     handler,
		stopChan:    make(chan struct{}),
		config:      p.config,
	}

	// 注册订阅者
	p.mu.Lock()
	for _, channel := range channels {
		p.subscribers[channel] = subscriber
	}
	p.mu.Unlock()

	// 启动订阅
	if err := subscriber.start(); err != nil {
		// 清理注册的订阅者
		p.mu.Lock()
		for _, channel := range channels {
			delete(p.subscribers, channel)
		}
		p.mu.Unlock()
		return nil, err
	}

	return subscriber, nil
}

// SubscribeJSON 订阅JSON消息（泛型版本）
func SubscribeJSON[T any](p *PubSub, channels []string, handler TypedMessageHandler[T]) (*Subscriber, error) {
	jsonHandler := func(ctx context.Context, channel string, message string) error {
		var data T
		if err := json.Unmarshal([]byte(message), &data); err != nil {
			p.logger.Errorf("Failed to unmarshal message from channel %s: %v", channel, err)
			return err
		}
		return handler(ctx, channel, data)
	}

	return p.Subscribe(channels, jsonHandler)
}

// SubscribePattern 订阅模式匹配的频道
func (p *PubSub) SubscribePattern(patterns []string, handler MessageHandler) (*Subscriber, error) {
	if len(patterns) == 0 {
		return nil, fmt.Errorf("no patterns specified")
	}
	if handler == nil {
		return nil, fmt.Errorf("handler cannot be nil")
	}

	// 转换模式名
	patternKeys := make([]string, len(patterns))
	for i, pattern := range patterns {
		patternKeys[i] = p.getChannelKey(pattern)
	}

	// 创建订阅者
	subscriber := &Subscriber{
		pubsub:      p,
		patterns:    patterns,
		patternKeys: patternKeys,
		handler:     handler,
		stopChan:    make(chan struct{}),
		config:      p.config,
		isPattern:   true,
	}

	// 注册订阅者
	p.mu.Lock()
	for _, pattern := range patterns {
		p.subscribers[pattern] = subscriber
	}
	p.mu.Unlock()

	// 启动订阅
	if err := subscriber.start(); err != nil {
		// 清理注册的订阅者
		p.mu.Lock()
		for _, pattern := range patterns {
			delete(p.subscribers, pattern)
		}
		p.mu.Unlock()
		return nil, err
	}

	return subscriber, nil
}

// Unsubscribe 取消订阅
func (p *PubSub) Unsubscribe(channels ...string) error {
	if len(channels) == 0 {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, channel := range channels {
		if subscriber, exists := p.subscribers[channel]; exists {
			subscriber.Stop()
			delete(p.subscribers, channel)
		}
	}

	return nil
}

// GetSubscribers 获取活跃的订阅者数量
func (p *PubSub) GetSubscribers() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.subscribers)
}

// GetChannels 获取已订阅的频道列表
func (p *PubSub) GetChannels() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	channels := make([]string, 0, len(p.subscribers))
	for channel := range p.subscribers {
		channels = append(channels, channel)
	}
	return channels
}

// Close 关闭发布订阅
func (p *PubSub) Close() error {
	p.cancel()

	// 停止所有订阅者
	p.mu.Lock()
	for _, subscriber := range p.subscribers {
		subscriber.Stop()
	}
	p.subscribers = make(map[string]*Subscriber)
	p.mu.Unlock()

	// 等待所有goroutine结束
	p.wg.Wait()

	return nil
}

// Subscriber 订阅者
type Subscriber struct {
	pubsub      *PubSub
	channels    []string
	patterns    []string
	channelKeys []string
	patternKeys []string
	handler     MessageHandler
	stopChan    chan struct{}
	config      PubSubConfig
	isPattern   bool
	pubSubConn  *redis.PubSub
	once        sync.Once
}

// start 启动订阅
func (s *Subscriber) start() error {
	var err error

	if s.isPattern {
		s.pubSubConn = s.pubsub.client.PSubscribe(s.pubsub.ctx, s.patternKeys...)
	} else {
		s.pubSubConn = s.pubsub.client.Subscribe(s.pubsub.ctx, s.channelKeys...)
	}

	// 使用超时context测试订阅是否成功
	testCtx, cancel := context.WithTimeout(s.pubsub.ctx, time.Second*5)
	defer cancel()

	_, err = s.pubSubConn.Receive(testCtx)
	if err != nil {
		s.pubSubConn.Close()
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	// 启动消息接收goroutine
	s.pubsub.wg.Add(1)
	syncx.Go(s.pubsub.ctx).
		OnPanic(func(r interface{}) {
			s.config.Logger.Errorf("Panic in messageLoop: %v", r)
		}).
		Exec(s.messageLoop)

	if s.isPattern {
		s.config.Logger.Infof("Started pattern subscription for: %v", s.patterns)
	} else {
		s.config.Logger.Infof("Started subscription for channels: %v", s.channels)
	}

	return nil
}

// messageLoop 消息循环
func (s *Subscriber) messageLoop() {
	defer s.pubsub.wg.Done()
	defer func() {
		if s.pubSubConn != nil {
			s.pubSubConn.Close()
		}
	}()

	ch := s.pubSubConn.Channel()

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				s.config.Logger.Info("Subscription channel closed")
				return
			}

			if msg == nil {
				continue
			}

			// 在单独的goroutine中处理消息，避免阻塞
			syncx.Go(s.pubsub.ctx).
				OnPanic(func(r interface{}) {
					s.config.Logger.Errorf("Panic in handleMessage: %v", r)
				}).
				Exec(func() { s.handleMessage(msg) })

		case <-s.stopChan:
			s.config.Logger.Info("Subscription stopped")
			return

		case <-s.pubsub.ctx.Done():
			s.config.Logger.Info("PubSub context cancelled")
			return
		}
	}
}

// handleMessage 处理单条消息
func (s *Subscriber) handleMessage(msg *redis.Message) {
	if msg.Payload == "" {
		return
	}

	// 移除命名空间前缀
	channel := msg.Channel
	if s.pubsub.config.Namespace != "" {
		prefix := s.pubsub.config.Namespace + ":"
		if len(channel) > len(prefix) && channel[:len(prefix)] == prefix {
			channel = channel[len(prefix):]
		}
	}

	// 自动解压缩消息（如果有压缩标记）
	payload := msg.Payload
	payloadBytes := []byte(payload)
	if zipx.IsGzipCompressed(payloadBytes) {
		decompressed, err := zipx.GzipDecompressWithPrefix(payloadBytes)
		if err != nil {
			s.config.Logger.Warnf("Failed to decompress message from channel %s: %v", channel, err)
			// 解压失败，使用原始消息
		} else {
			payload = string(decompressed)
			s.config.Logger.Debugf("Decompressed message from %d to %d bytes", len(payloadBytes)-CompressionPrefixLen, len(payload))
		}
	}

	// 创建处理上下文
	ctx, cancel := context.WithTimeout(s.pubsub.ctx, time.Minute)
	defer cancel()

	// 使用 retry 包重试处理消息
	retrier := retry.NewRetryWithCtx(ctx).
		SetAttemptCount(s.config.MaxRetries + 1).
		SetInterval(s.config.RetryDelay).
		SetCaller(fmt.Sprintf("Subscriber.handleMessage(%s)", channel))

	retrier.SetErrCallback(func(nowAttemptCount, remainCount int, err error, funcName ...string) {
		s.config.Logger.Warnf("Message handler failed (attempt %d) for channel %s: %v", nowAttemptCount, channel, err)
	}).SetSuccessCallback(func(funcName ...string) {
		s.config.Logger.Debugf("Message handled successfully for channel %s", channel)
	})

	if err := retrier.Do(func() error {
		return s.handler(ctx, channel, payload)
	}); err != nil {
		s.config.Logger.Errorf("Failed to handle message for channel %s after all retries: %v", channel, err)
	}
}

// Stop 停止订阅（不从注册表中移除）
func (s *Subscriber) Stop() {
	s.once.Do(func() {
		close(s.stopChan)
	})
}

// Unsubscribe 取消订阅并从注册表中移除
func (s *Subscriber) Unsubscribe() error {
	// 先停止接收消息
	s.Stop()

	// 选择要移除的 key 列表（patterns 或 channels）
	keysToRemove := mathx.IF(s.isPattern, s.patterns, s.channels)

	// 批量从注册表中移除
	s.pubsub.mu.Lock()
	for _, key := range keysToRemove {
		delete(s.pubsub.subscribers, key)
	}
	s.pubsub.mu.Unlock()

	s.config.Logger.Infof("Unsubscribed from %d %s", len(keysToRemove),
		mathx.IF(s.isPattern, "patterns", "channels"))

	return nil
}

// GetSubscriptionInfo 获取订阅信息
func (s *Subscriber) GetSubscriptionInfo() *SubscriptionInfo {
	return &SubscriptionInfo{
		IsPattern:    s.isPattern,
		IsActive:     s.IsActive(),
		Channels:     mathx.IF(s.isPattern, s.patterns, s.channels),
		ChannelCount: mathx.IF(s.isPattern, len(s.patterns), len(s.channels)),
		Config:       s.config,
	}
}

// Resubscribe 重新订阅（如果已停止）
func (s *Subscriber) Resubscribe() error {
	if s.IsActive() {
		return fmt.Errorf("subscriber is already active")
	}

	// 重置 stopChan（需要新的 once）
	s.stopChan = make(chan struct{})
	s.once = sync.Once{}

	// 重新注册
	s.pubsub.mu.Lock()
	keysToRegister := mathx.IF(s.isPattern, s.patterns, s.channels)
	for _, key := range keysToRegister {
		s.pubsub.subscribers[key] = s
	}
	s.pubsub.mu.Unlock()

	// 重新启动订阅
	return s.start()
}

// IsActive 检查订阅是否活跃
func (s *Subscriber) IsActive() bool {
	select {
	case <-s.stopChan:
		return false
	default:
		return true
	}
}

// GetChannels 获取订阅的频道
func (s *Subscriber) GetChannels() []string {
	if s.isPattern {
		return s.patterns
	}
	return s.channels
}

// PubSubStats 发布订阅统计
type PubSubStats struct {
	ActiveSubscribers int      `json:"active_subscribers"`
	Channels          []string `json:"channels"`
	Patterns          []string `json:"patterns"`
}

// SubscriptionInfo 订阅信息
type SubscriptionInfo struct {
	IsPattern    bool         `json:"is_pattern"`
	IsActive     bool         `json:"is_active"`
	Channels     []string     `json:"channels"`
	ChannelCount int          `json:"channel_count"`
	Config       PubSubConfig `json:"config"`
}

// GetStats 获取统计信息
func (p *PubSub) GetStats() *PubSubStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// 使用set来避免重复计算订阅者
	uniqueSubscribers := make(map[*Subscriber]bool)
	channels := make([]string, 0)
	patterns := make([]string, 0)

	for _, subscriber := range p.subscribers {
		uniqueSubscribers[subscriber] = true
	}

	for subscriber := range uniqueSubscribers {
		if subscriber.IsActive() {
			if subscriber.isPattern {
				patterns = append(patterns, subscriber.patterns...)
			} else {
				channels = append(channels, subscriber.channels...)
			}
		}
	}

	stats := &PubSubStats{
		ActiveSubscribers: len(uniqueSubscribers),
		Channels:          channels,
		Patterns:          patterns,
	}

	return stats
}

// 便利函数

// SimplePublish 简单发布消息
func SimplePublish(client *redis.Client, channel string, message interface{}) error {
	pubsub := NewPubSub(client)
	defer pubsub.Close()

	return pubsub.Publish(context.Background(), channel, message)
}

// SimpleSubscribe 简单订阅消息
func SimpleSubscribe(client *redis.Client, channel string, handler MessageHandler) (*Subscriber, error) {
	pubsub := NewPubSub(client)
	return pubsub.Subscribe([]string{channel}, handler)
}

// BroadcastMessage 广播消息到多个频道
func (p *PubSub) BroadcastMessage(ctx context.Context, channels []string, message interface{}) error {
	var lastErr error
	for _, channel := range channels {
		if err := p.Publish(ctx, channel, message); err != nil {
			lastErr = err
			p.logger.Errorf("Failed to broadcast to channel %s: %v", channel, err)
		}
	}
	return lastErr
}

// RequestResponse 请求-响应模式（基于发布订阅）
func (p *PubSub) RequestResponse(ctx context.Context, requestChannel, responseChannel string, request interface{}, timeout time.Duration) (string, error) {
	// 创建响应接收器
	responseChan := make(chan string, 1)
	var subscriber *Subscriber
	var err error

	// 订阅响应频道
	subscriber, err = p.Subscribe([]string{responseChannel}, func(ctx context.Context, channel string, message string) error {
		select {
		case responseChan <- message:
		default:
			// 频道已满，忽略
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to subscribe to response channel: %w", err)
	}
	defer subscriber.Stop()

	// 发送请求
	if err := p.Publish(ctx, requestChannel, request); err != nil {
		return "", fmt.Errorf("failed to publish request: %w", err)
	}

	// 等待响应
	select {
	case response := <-responseChan:
		return response, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("request timeout after %v", timeout)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// ============================================================================
// Redis 客户端访问和常用操作
// ============================================================================

// GetClient 获取底层 Redis 客户端（用于高级操作）
func (p *PubSub) GetClient() *redis.Client {
	return p.client
}
