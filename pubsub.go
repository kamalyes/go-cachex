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
	"github.com/redis/go-redis/v9"
	"log"
	"sync"
	"time"
)

// MessageHandler 消息处理器接口
type MessageHandler func(ctx context.Context, channel string, message string) error

// TypedMessageHandler 泛型消息处理器
type TypedMessageHandler[T any] func(ctx context.Context, channel string, message T) error

// PubSubConfig 发布订阅配置
type PubSubConfig struct {
	Namespace     string        // 命名空间
	MaxRetries    int           // 最大重试次数
	RetryDelay    time.Duration // 重试延迟
	BufferSize    int           // 消息缓冲区大小
	EnableLogging bool          // 是否启用日志
	PingInterval  time.Duration // 心跳间隔
}

// DefaultPubSubConfig 默认配置
func DefaultPubSubConfig() PubSubConfig {
	return PubSubConfig{
		Namespace:     "pubsub",
		MaxRetries:    2,                      // 减少重试次数
		RetryDelay:    time.Millisecond * 100, // 大幅减少重试延迟
		BufferSize:    100,
		EnableLogging: false,            // 默认关闭日志避免额外开销
		PingInterval:  time.Second * 10, // 减少心跳间隔
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
}

// NewPubSub 创建发布订阅实例
func NewPubSub(client *redis.Client, config ...PubSubConfig) *PubSub {
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

	channelKey := p.getChannelKey(channel)

	// 重试发布
	var lastErr error
	for i := 0; i <= p.config.MaxRetries; i++ {
		if err := p.client.Publish(ctx, channelKey, data).Err(); err != nil {
			lastErr = err
			if p.config.EnableLogging {
				log.Printf("Publish attempt %d failed for channel %s: %v", i+1, channel, err)
			}

			if i < p.config.MaxRetries {
				time.Sleep(p.config.RetryDelay)
				continue
			}
		} else {
			if p.config.EnableLogging && i > 0 {
				log.Printf("Publish succeeded for channel %s after %d retries", channel, i)
			}
			return nil
		}
	}

	return fmt.Errorf("failed to publish after %d retries: %w", p.config.MaxRetries+1, lastErr)
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
			if p.config.EnableLogging {
				log.Printf("Failed to unmarshal message from channel %s: %v", channel, err)
			}
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
	go s.messageLoop()

	if s.config.EnableLogging {
		if s.isPattern {
			log.Printf("Started pattern subscription for: %v", s.patterns)
		} else {
			log.Printf("Started subscription for channels: %v", s.channels)
		}
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
				if s.config.EnableLogging {
					log.Println("Subscription channel closed")
				}
				return
			}

			if msg == nil {
				continue
			}

			// 在单独的goroutine中处理消息，避免阻塞
			go s.handleMessage(msg)

		case <-s.stopChan:
			if s.config.EnableLogging {
				log.Println("Subscription stopped")
			}
			return

		case <-s.pubsub.ctx.Done():
			if s.config.EnableLogging {
				log.Println("PubSub context cancelled")
			}
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

	// 创建处理上下文
	ctx, cancel := context.WithTimeout(s.pubsub.ctx, time.Minute)
	defer cancel()

	// 重试处理消息
	var lastErr error
	for i := 0; i <= s.config.MaxRetries; i++ {
		if err := s.handler(ctx, channel, msg.Payload); err != nil {
			lastErr = err
			if s.config.EnableLogging {
				log.Printf("Message handler failed (attempt %d) for channel %s: %v", i+1, channel, err)
			}

			if i < s.config.MaxRetries {
				time.Sleep(s.config.RetryDelay)
				continue
			}
		} else {
			if s.config.EnableLogging && i > 0 {
				log.Printf("Message handled successfully for channel %s after %d retries", channel, i)
			}
			return
		}
	}

	if s.config.EnableLogging {
		log.Printf("Failed to handle message for channel %s after %d retries: %v", channel, s.config.MaxRetries+1, lastErr)
	}
}

// Stop 停止订阅
func (s *Subscriber) Stop() {
	s.once.Do(func() {
		close(s.stopChan)
	})
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
			if p.config.EnableLogging {
				log.Printf("Failed to broadcast to channel %s: %v", channel, err)
			}
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
