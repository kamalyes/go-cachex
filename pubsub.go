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
	MaxWorkers         int            // 消息处理 worker 数量，防止 goroutine 泄漏，默认 20
	WorkerQueueSize    int            // worker 队列大小，默认 100
}

// DefaultPubSubConfig 默认配置
// Namespace 默认为空，即频道名不加前缀；需要隔离的模块通过 WithPubSubNamespace 显式设置
func DefaultPubSubConfig() PubSubConfig {
	return PubSubConfig{
		Namespace:          "",
		MaxRetries:         2,                      // 减少重试次数
		RetryDelay:         time.Millisecond * 100, // 大幅减少重试延迟
		BufferSize:         100,
		PingInterval:       time.Second * 10, // 减少心跳间隔
		EnableCompression:  false,            // 默认关闭压缩
		CompressionMinSize: 1024,             // 默认1KB以上才压缩
		MaxWorkers:         50,               // 默认 50 个 worker 处理消息
		WorkerQueueSize:    200,              // 默认队列大小 200
	}
}

// PubSub Redis发布订阅封装
type PubSub struct {
	client      redis.UniversalClient  // Redis 客户端
	config      PubSubConfig           // 发布订阅配置
	subscribers map[string]*Subscriber // 订阅者注册表，key 为频道或模式名
	mu          sync.RWMutex           // 保护 subscribers 的读写锁
	ctx         context.Context        // 全局上下文
	cancel      context.CancelFunc     // 取消函数，用于关闭所有订阅
	wg          sync.WaitGroup         // 等待所有 goroutine 结束
	logger      logger.ILogger         // 日志记录器
}

// PubSubOption 发布订阅配置项
type PubSubOption func(*PubSubConfig)

// WithPubSubNamespace 设置命名空间
func WithPubSubNamespace(ns string) PubSubOption {
	return func(c *PubSubConfig) { c.Namespace = ns }
}

// WithPubSubMaxRetries 设置最大重试次数
func WithPubSubMaxRetries(n int) PubSubOption {
	return func(c *PubSubConfig) { c.MaxRetries = n }
}

// WithPubSubRetryDelay 设置重试延迟
func WithPubSubRetryDelay(d time.Duration) PubSubOption {
	return func(c *PubSubConfig) { c.RetryDelay = d }
}

// WithPubSubBufferSize 设置消息缓冲区大小
func WithPubSubBufferSize(n int) PubSubOption {
	return func(c *PubSubConfig) { c.BufferSize = n }
}

// WithPubSubLogger 设置日志记录器
func WithPubSubLogger(l logger.ILogger) PubSubOption {
	return func(c *PubSubConfig) { c.Logger = l }
}

// WithPubSubPingInterval 设置心跳间隔
func WithPubSubPingInterval(d time.Duration) PubSubOption {
	return func(c *PubSubConfig) { c.PingInterval = d }
}

// WithPubSubCompression 是否启用消息压缩
func WithPubSubCompression(enable bool) PubSubOption {
	return func(c *PubSubConfig) { c.EnableCompression = enable }
}

// WithPubSubCompressionMinSize 设置压缩阈值
func WithPubSubCompressionMinSize(n int) PubSubOption {
	return func(c *PubSubConfig) { c.CompressionMinSize = n }
}

// WithPubSubMaxWorkers 设置消息处理 worker 数量
func WithPubSubMaxWorkers(n int) PubSubOption {
	return func(c *PubSubConfig) { c.MaxWorkers = n }
}

// WithPubSubWorkerQueueSize 设置 worker 队列大小
func WithPubSubWorkerQueueSize(n int) PubSubOption {
	return func(c *PubSubConfig) { c.WorkerQueueSize = n }
}

// NewPubSub 创建发布订阅实例
func NewPubSub(client redis.UniversalClient, opts ...PubSubOption) *PubSub {
	cfg := DefaultPubSubConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &PubSub{
		client:      client,
		config:      cfg,
		subscribers: make(map[string]*Subscriber),
		ctx:         ctx,
		cancel:      cancel,
		logger:      mathx.IfEmpty(cfg.Logger, mathx.IfEmpty(globalLogger, NewDefaultCachexLogger())),
	}
}

// getChannelKey 获取带命名空间的频道名
// namespace 为空时直接返回原始频道名，避免加多余的 ':' 前缀
func (p *PubSub) getChannelKey(channel string) string {
	if p.config.Namespace == "" {
		return channel
	}
	return p.config.Namespace + ":" + channel
}

// Publish 发布消息
func (p *PubSub) Publish(ctx context.Context, channel string, message any) error {
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
		result, err := zipx.GzipCompressWithPrefixInfo([]byte(data))
		if err != nil {
			p.logger.Warnf("Failed to compress message: %v, sending uncompressed", err)
		}
		data = string(result.Data)
		p.logger.Debugf(result.String())
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
	syncx.WithLock(&p.mu, func() {
		for _, channel := range channels {
			p.subscribers[channel] = subscriber
		}
	})

	// 启动订阅
	if err := subscriber.start(); err != nil {
		// 清理注册的订阅者
		syncx.WithLock(&p.mu, func() {
			for _, channel := range channels {
				delete(p.subscribers, channel)
			}
		})
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
	syncx.WithLock(&p.mu, func() {
		for _, pattern := range patterns {
			p.subscribers[pattern] = subscriber
		}
	})

	// 启动订阅
	if err := subscriber.start(); err != nil {
		// 清理注册的订阅者
		syncx.WithLock(&p.mu, func() {
			for _, pattern := range patterns {
				delete(p.subscribers, pattern)
			}
		})
		return nil, err
	}

	return subscriber, nil
}

// Unsubscribe 取消订阅
func (p *PubSub) Unsubscribe(channels ...string) error {
	if len(channels) == 0 {
		return nil
	}

	// 收集唯一的订阅者并删除，避免重复 Stop
	uniqueSubscribers := syncx.WithLockReturnValue(&p.mu, func() map[*Subscriber]bool {
		uniqueSubscribers := make(map[*Subscriber]bool)
		for _, channel := range channels {
			if subscriber, exists := p.subscribers[channel]; exists {
				uniqueSubscribers[subscriber] = true
				delete(p.subscribers, channel)
			}
		}
		return uniqueSubscribers
	})

	// 只 Stop 一次每个订阅者
	for subscriber := range uniqueSubscribers {
		subscriber.Stop()
	}

	return nil
}

// GetSubscribers 获取活跃的订阅者数量
func (p *PubSub) GetSubscribers() int {
	return syncx.WithRLockReturnValue(&p.mu, func() int {
		return len(p.subscribers)
	})
}

// GetChannels 获取已订阅的频道列表
func (p *PubSub) GetChannels() []string {
	return syncx.WithRLockReturnValue(&p.mu, func() []string {
		channels := make([]string, 0, len(p.subscribers))
		for channel := range p.subscribers {
			channels = append(channels, channel)
		}
		return channels
	})
}

// Close 关闭发布订阅
func (p *PubSub) Close() error {
	p.cancel()

	// 先收集所有唯一订阅者并清空注册表（持锁时间短）
	// 不在持锁状态下调用 Stop，避免与 Resubscribe 的 stopMu → p.mu 形成锁顺序反转死锁
	subscribers := syncx.WithLockReturnValue(&p.mu, func() []*Subscriber {
		seen := make(map[*Subscriber]bool)
		result := make([]*Subscriber, 0, len(p.subscribers))
		for _, sub := range p.subscribers {
			if !seen[sub] {
				seen[sub] = true
				result = append(result, sub)
			}
		}
		p.subscribers = make(map[string]*Subscriber)
		return result
	})

	// 在锁外停止所有订阅者
	for _, sub := range subscribers {
		sub.Stop()
	}

	// 等待所有goroutine结束
	p.wg.Wait()

	return nil
}

// Subscriber 订阅者
type Subscriber struct {
	pubsub      *PubSub           // 所属的 PubSub 实例
	channels    []string          // 订阅的频道列表（原始名称，不含命名空间）
	patterns    []string          // 订阅的模式列表（原始名称，不含命名空间）
	channelKeys []string          // 带命名空间的频道名列表
	patternKeys []string          // 带命名空间的模式名列表
	handler     MessageHandler    // 消息处理器
	stopChan    chan struct{}     // 停止信号通道
	config      PubSubConfig      // 订阅配置
	isPattern   bool              // 是否为模式订阅
	pubSubConn  *redis.PubSub     // Redis 订阅连接
	pool        *syncx.WorkerPool // Worker 池，用于限制消息处理的并发数
	mu          sync.RWMutex      // 保护状态字段
	isActive    bool              // 明确的活跃状态标记
	stopMu      sync.Mutex        // 序列化 Stop/Resubscribe 操作，防止竞态
	loopWg      sync.WaitGroup    // 跟踪 messageLoop goroutine，确保退出后再重置
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

	// 创建 Worker 池处理消息，防止 goroutine 无限增长
	s.pool = syncx.NewWorkerPool(s.config.MaxWorkers, s.config.WorkerQueueSize)

	// 标记为活跃
	syncx.WithLock(&s.mu, func() {
		s.isActive = true
	})

	// 启动消息接收goroutine，使用 loopWg 跟踪以便 Stop 时等待退出
	s.loopWg.Add(1)
	s.pubsub.wg.Add(1)
	syncx.Go(s.pubsub.ctx).
		OnPanic(func(r interface{}) {
			s.pubsub.logger.Errorf("Panic in messageLoop: %v", r)
		}).
		Exec(s.messageLoop)

	s.pubsub.logger.Infof("Started subscription for %s: %v",
		mathx.IF(s.isPattern, "patterns", "channels"),
		mathx.IF(s.isPattern, s.patterns, s.channels))

	return nil
}

// messageLoop 消息循环
// 连接断开导致 Channel() 关闭时自动重连（指数退避），除非显式 Stop 或 PubSub 关闭。
// 背景：Redis 主从切换/连接被 LB 杀掉/网络中断后，go-redis 会关闭消息通道；
// 若 messageLoop 直接退出，订阅将永久失活（isActive=false），失活期间发布的
// 消息全部丢失（PubSub 至多一次投递），且调用方无感知
func (s *Subscriber) messageLoop() {
	// loopWg.Done 放在最前（最后执行），确保 pool 关闭和状态更新完成后才通知 Stop 可以返回
	defer s.loopWg.Done()
	defer s.pubsub.wg.Done()
	defer func() {
		// 永久退出（Stop/PubSub 关闭）才执行最终清理；重连路径不经过此处
		syncx.WithLock(&s.mu, func() {
			s.isActive = false
		})
		if s.pubSubConn != nil {
			s.pubSubConn.Close()
		}
		// 关闭 Worker 池，等待所有消息处理完成
		// pool.Close 内部使用 sync.Once，与 Stop 中的 Close 不会冲突
		if s.pool != nil {
			s.pool.Close()
		}
	}()

	ch := s.pubSubConn.Channel()

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				// 连接断开：自动重连（收到停止信号时才永久退出）
				newCh, stopped := s.reconnectLoop()
				if stopped {
					return
				}
				ch = newCh
				continue
			}

			if msg == nil {
				continue
			}

			// 使用 Worker 池处理消息，避免无限创建 goroutine
			// 如果队列满，会阻塞直到有空位
			if err := s.pool.Submit(s.pubsub.ctx, func() {
				s.handleMessage(msg)
			}); err != nil {
				s.pubsub.logger.Warnf("Failed to submit message to worker pool: %v", err)
			}

		case <-s.stopChan:
			s.pubsub.logger.Info("Subscription stopped")
			return

		case <-s.pubsub.ctx.Done():
			s.pubsub.logger.Info("PubSub context cancelled")
			return
		}
	}
}

// reconnectLoop 自动重连循环：指数退避重建订阅连接，直到成功或收到停止信号
// 在 messageLoop goroutine 内运行（pubSubConn 的读写仅发生在此 goroutine），
// 重连期间 isActive 保持 true（订阅者仍在服务，只是链路自愈中）
// 返回新的消息通道；stopped=true 表示收到 Stop/PubSub 关闭信号，调用方应永久退出
func (s *Subscriber) reconnectLoop() (ch <-chan *redis.Message, stopped bool) {
	delay := mathx.IfNotZero(s.config.RetryDelay, 100*time.Millisecond)
	const maxDelay = 5 * time.Second

	for attempt := 1; ; attempt++ {
		// 每次重试前检查停止信号（Stop 期间不应继续重连）
		select {
		case <-s.stopChan:
			return nil, true
		case <-s.pubsub.ctx.Done():
			return nil, true
		default:
		}

		// 关闭残留旧连接后重建订阅（复用 start 的订阅与连通性验证语义）
		if s.pubSubConn != nil {
			s.pubSubConn.Close()
		}
		if s.isPattern {
			s.pubSubConn = s.pubsub.client.PSubscribe(s.pubsub.ctx, s.patternKeys...)
		} else {
			s.pubSubConn = s.pubsub.client.Subscribe(s.pubsub.ctx, s.channelKeys...)
		}

		// 连通性验证：SUBSCRIBE 握手成功才算重连完成
		testCtx, cancel := context.WithTimeout(s.pubsub.ctx, 5*time.Second)
		_, err := s.pubSubConn.Receive(testCtx)
		cancel()
		if err == nil {
			s.pubsub.logger.Infof("Subscription auto-reconnected (attempt %d) for %s: %v",
				attempt,
				mathx.IF(s.isPattern, "patterns", "channels"),
				mathx.IF(s.isPattern, s.patterns, s.channels))
			return s.pubSubConn.Channel(), false
		}

		s.pubSubConn.Close()
		s.pubsub.logger.Warnf("Subscription reconnect attempt %d failed, retrying in %v: %v",
			attempt, delay, err)

		select {
		case <-time.After(delay):
		case <-s.stopChan:
			return nil, true
		case <-s.pubsub.ctx.Done():
			return nil, true
		}
		delay = min(delay*2, maxDelay)
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
		decompressed, err := zipx.GzipSmartDecompress(payloadBytes)
		if err != nil {
			s.pubsub.logger.Warnf("Failed to decompress message from channel %s: %v", channel, err)
			// 解压失败，使用原始消息
		}
		payload = string(decompressed)
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
		s.pubsub.logger.Warnf("Message handler failed (attempt %d) for channel %s: %v", nowAttemptCount, channel, err)
	}).SetSuccessCallback(func(funcName ...string) {
		s.pubsub.logger.Debugf("Message handled successfully for channel %s", channel)
	})

	if err := retrier.Do(func() error {
		return s.handler(ctx, channel, payload)
	}); err != nil {
		s.pubsub.logger.Errorf("Failed to handle message for channel %s after all retries: %v", channel, err)
	}
}

// Stop 停止订阅（不从注册表中移除）
// 该方法会阻塞直到 messageLoop goroutine 完全退出，防止 goroutine 泄漏。
// 使用 stopMu 序列化 Stop/Resubscribe，避免竞态。
func (s *Subscriber) Stop() {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()

	// 检查是否已停止
	s.mu.RLock()
	active := s.isActive
	s.mu.RUnlock()
	if !active {
		return
	}

	// 标记为非活跃
	syncx.WithLock(&s.mu, func() {
		s.isActive = false
	})

	// 关闭 stopChan 通知 messageLoop 退出
	select {
	case <-s.stopChan:
		// 已经关闭
	default:
		close(s.stopChan)
	}

	// 关闭 pool 以解除 messageLoop 中 pool.Submit 的阻塞
	// pool.Close 内部使用 sync.Once，messageLoop 的 defer 再次调用不会冲突
	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()
	if pool != nil {
		pool.Close()
	}

	// 等待 messageLoop goroutine 完全退出
	s.loopWg.Wait()
}

// Unsubscribe 取消订阅并从注册表中移除
func (s *Subscriber) Unsubscribe() error {
	// 先停止接收消息
	s.Stop()

	// 选择要移除的 key 列表（patterns 或 channels）
	keysToRemove := mathx.IF(s.isPattern, s.patterns, s.channels)

	// 批量从注册表中移除
	syncx.WithLock(&s.pubsub.mu, func() {
		for _, key := range keysToRemove {
			delete(s.pubsub.subscribers, key)
		}
	})

	s.pubsub.logger.Infof("Unsubscribed from %d %s", len(keysToRemove),
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
// 必须在 Stop() 之后调用。使用 stopMu 与 Stop 互斥，确保旧 goroutine
// 完全退出后才重置字段并启动新 goroutine，防止竞态和泄漏。
func (s *Subscriber) Resubscribe() error {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()

	// 检查是否仍在活跃状态
	s.mu.RLock()
	active := s.isActive
	s.mu.RUnlock()
	if active {
		return fmt.Errorf("subscriber is already active")
	}

	// 确保 messageLoop goroutine 已完全退出（防御性，Stop 已等待）
	s.loopWg.Wait()

	// 旧的 pool 已在 Stop 中关闭，清理引用
	s.mu.Lock()
	s.pool = nil
	// 重置 stopChan 以便新的 messageLoop 使用
	s.stopChan = make(chan struct{})
	s.isActive = true
	s.mu.Unlock()

	// 重新注册
	syncx.WithLock(&s.pubsub.mu, func() {
		keysToRegister := mathx.IF(s.isPattern, s.patterns, s.channels)
		for _, key := range keysToRegister {
			s.pubsub.subscribers[key] = s
		}
	})

	// 重新启动订阅
	err := s.start()
	if err != nil {
		// 如果启动失败，恢复状态
		syncx.WithLock(&s.mu, func() {
			s.isActive = false
		})
		return err
	}

	return nil
}

// IsActive 检查订阅是否活跃
func (s *Subscriber) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isActive
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
	// 先收集所有唯一订阅者
	uniqueSubscribers := syncx.WithRLockReturnValue(&p.mu, func() []*Subscriber {
		uniqueSubscribers := make([]*Subscriber, 0)
		seen := make(map[*Subscriber]bool)
		for _, subscriber := range p.subscribers {
			if !seen[subscriber] {
				seen[subscriber] = true
				uniqueSubscribers = append(uniqueSubscribers, subscriber)
			}
		}
		return uniqueSubscribers
	})

	// 过滤出活跃的订阅者
	activeSubscribers := mathx.FilterSlice(uniqueSubscribers, func(s *Subscriber) bool {
		return s.IsActive()
	})

	// 收集频道和模式
	channels := make([]string, 0)
	patterns := make([]string, 0)

	for _, subscriber := range activeSubscribers {
		if subscriber.isPattern {
			patterns = append(patterns, subscriber.patterns...)
		} else {
			channels = append(channels, subscriber.channels...)
		}
	}

	stats := &PubSubStats{
		ActiveSubscribers: len(activeSubscribers),
		Channels:          channels,
		Patterns:          patterns,
	}

	return stats
}

// 便利函数

// SimplePublish 简单发布消息
func SimplePublish(client redis.UniversalClient, channel string, message any) error {
	pubsub := NewPubSub(client)
	defer pubsub.Close()

	return pubsub.Publish(context.Background(), channel, message)
}

// SimpleSubscribe 简单订阅消息
//
// 警告：此函数创建的 PubSub 实例需要手动管理生命周期
// 返回 PubSub 实例和 Subscriber，使用完毕后需要调用 pubsub.Close()
//
// 推荐使用 NewPubSub() + Subscribe() 以便更好地管理生命周期
func SimpleSubscribe(client redis.UniversalClient, channel string, handler MessageHandler) (*PubSub, *Subscriber, error) {
	pubsub := NewPubSub(client)
	subscriber, err := pubsub.Subscribe([]string{channel}, handler)
	if err != nil {
		pubsub.Close()
		return nil, nil, err
	}
	return pubsub, subscriber, nil
}

// BroadcastMessage 广播消息到多个频道
func (p *PubSub) BroadcastMessage(ctx context.Context, channels []string, message any) error {
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
func (p *PubSub) RequestResponse(ctx context.Context, requestChannel, responseChannel string, request any, timeout time.Duration) (string, error) {
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
	// 使用 Unsubscribe 而不是 Stop，确保从注册表中移除
	defer subscriber.Unsubscribe()

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
func (p *PubSub) GetClient() redis.UniversalClient {
	return p.client
}
