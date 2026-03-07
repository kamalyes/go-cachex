/**
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-03-06 22:30:15
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-03-06 23:10:00
 * @FilePath: \go-cachex\durable_channel.go
 * @Description: 分布式可恢复的持久化 Channel - 通用的持久化消息队列组件
 *
 * 这是一个通用的持久化 Channel 实现，可用于任何需要可靠消息传递的场景：
 * - 微服务间异步通信
 * - 任务队列
 * - 事件总线
 * - WebSocket 消息队列
 * - 日志收集
 * - 数据管道
 *
 * 特性：
 * - 像普通 channel 一样使用，但具备持久化能力
 * - 支持任意类型（泛型）
 * - 三种持久化模式（同步/异步/混合）
 * - 自动故障恢复
 * - 分布式支持（多节点）
 * - 可选压缩
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */

package cachex

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/contextx"
	"github.com/kamalyes/go-toolbox/pkg/mathx"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
	"github.com/kamalyes/go-toolbox/pkg/zipx"
	"github.com/redis/go-redis/v9"
)

// DurableChannelConfig 持久化 Channel 配置
//
// 通用配置，适用于各种场景：
// - Name: 队列名称，建议使用有意义的命名（如 "user:123:messages", "tasks:worker-1"）
// - Namespace: Redis key 前缀，用于隔离不同应用（默认 "dchan"）
// - WorkerID: 工作节点标识，用于分布式场景下的节点隔离（默认 0）
//   - 分布式场景：每个节点使用不同的 WorkerID（如 0, 1, 2...）
//   - 单机场景：所有实例使用相同的 WorkerID（默认 0）
//   - 推荐使用 osx.GetWorkerId() 自动获取（支持 K8s Pod 序号）
//
// - PersistenceMode: 根据场景选择（决定是否启动消费者）：
//   - PersistSync: 只写 Redis，不启动消费者（生产者模式）
//   - PersistAsync: 异步写入 + 启动消费者（快速响应）
//   - PersistHybrid: 先写 Redis + 启动消费者（推荐，平衡可靠性和性能）
//
// - WorkerCount: 消费者数量（PersistSync 模式下自动为 0，其他模式默认 1）
// - QueryTimeout: 查询操作超时时间（Len/RedisLen，默认 3s）
type DurableChannelConfig struct {
	Name               string        // Channel 名称（必填）
	Namespace          string        // Redis key 命名空间（默认 "dchan"）
	WorkerID           int64         // 工作节点标识（默认 0，用于分布式场景节点隔离）
	BufferSize         int           // 内存缓冲区大小（默认 100）
	PersistenceMode    PersistMode   // 持久化模式（默认 PersistHybrid，决定是否启动消费者）
	WorkerCount        int           // 消费者 Worker 数量（PersistSync 下自动为 0，其他模式默认 1）
	PollInterval       time.Duration // Redis 轮询间隔（默认 100ms）
	RecoveryOnStart    bool          // 启动时是否恢复未消费消息（默认 true）
	TTL                time.Duration // Redis 队列过期时间（默认 24h，0 表示永不过期）
	EnableCompression  bool          // 是否启用压缩（默认 false，大消息建议启用）
	CompressionMinSize int           // 压缩阈值（默认 1KB）
	QueryTimeout       time.Duration // 查询操作超时时间（Len/RedisLen，默认 3s）
}

// PersistMode 持久化模式
type PersistMode int

const (
	// PersistSync 同步持久化 - 只写 Redis，不启动消费者（生产者模式）
	// 适用场景：只负责发送消息的服务
	PersistSync PersistMode = iota

	// PersistAsync 异步持久化 - 先写内存，异步写 Redis，启动消费者（消费者模式）
	// 适用场景：快速响应，可容忍少量丢失
	PersistAsync

	// PersistHybrid 混合模式 - 先写 Redis，再刷内存，启动消费者（完整模式，推荐）
	// 适用场景：大多数需要可靠性和性能平衡的场景
	PersistHybrid
)

// String 返回持久化模式的字符串表示
func (pm PersistMode) String() string {
	switch pm {
	case PersistSync:
		return "PersistSync"
	case PersistAsync:
		return "PersistAsync"
	case PersistHybrid:
		return "PersistHybrid"
	default:
		return "Unknown"
	}
}

// DurableChannel 错误定义
var (
	ErrChannelClosed      = fmt.Errorf("channel is closed")
	ErrCompressFailed     = fmt.Errorf("compress data failed")
	ErrPersistFailed      = fmt.Errorf("persist to redis failed")
	ErrRPushFailed        = fmt.Errorf("rpush failed")
	ErrPullFailed         = fmt.Errorf("pull from redis failed")
	ErrInvalidBLPopResult = fmt.Errorf("invalid blpop result")
	ErrDecompressFailed   = fmt.Errorf("decompress data failed")
	ErrUnknownPersistMode = fmt.Errorf("unknown persistence mode")
	ErrGetRedisLenFailed  = fmt.Errorf("failed to get redis queue length")
)

// DefaultDurableChannelConfig 默认配置
func DefaultDurableChannelConfig(name string) DurableChannelConfig {
	return DurableChannelConfig{
		Name:               name,
		Namespace:          "dchan",
		WorkerID:           0,
		BufferSize:         100,
		PersistenceMode:    PersistHybrid,
		WorkerCount:        1,
		PollInterval:       100 * time.Millisecond,
		RecoveryOnStart:    true,
		TTL:                24 * time.Hour,
		EnableCompression:  false,
		CompressionMinSize: 1024,
		QueryTimeout:       3 * time.Second,
	}
}

// DurableChannel 分布式可恢复的持久化 Channel
type DurableChannel[T any] struct {
	config      DurableChannelConfig
	client      redis.UniversalClient
	queueKey    string
	memChan     chan T
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	closed      atomic.Bool
	logger      logger.ILogger    // 日志记录器
	persistPool *syncx.WorkerPool // 持久化池（用于 PersistAsync 异步持久化）
}

// NewDurableChannel 创建持久化 Channel
//
// 这是一个通用的持久化消息队列，适用于多种场景：
//
// 示例 1：WebSocket 消息队列（单机模式）
//
//	ch := cachex.NewDurableChannel[*Message](redisClient, cachex.DurableChannelConfig{
//	    Name: "user:123:messages",
//	    BufferSize: 100,
//	    PersistenceMode: cachex.PersistHybrid,
//	})
//	defer ch.Close()
//
// 示例 2：分布式任务队列（使用 WorkerID 隔离）
//
//	import "github.com/kamalyes/go-toolbox/pkg/osx"
//
//	ch := cachex.NewDurableChannel[*Task](redisClient, cachex.DurableChannelConfig{
//	    Name: "worker:tasks",
//	    WorkerID: osx.GetWorkerId(), // 自动获取节点 ID（支持 K8s）
//	    WorkerCount: 5,
//	    PersistenceMode: cachex.PersistHybrid,
//	})
//
// 示例 3：事件总线
//
//	ch := cachex.NewDurableChannel[Event](redisClient, cachex.DurableChannelConfig{
//	    Name: "events:user-actions",
//	    Namespace: "myapp",
//	})
//
// 示例 4：日志收集
//
//	ch := cachex.NewDurableChannel[LogEntry](redisClient, cachex.DurableChannelConfig{
//	    Name: "logs:application",
//	    BufferSize: 1000,
//	    PersistenceMode: cachex.PersistAsync,
//	})
//
// 使用方式：
//
//	// 发送消息
//	ch.Send(ctx, data)
//
//	// 接收消息
//	msg := <-ch.Receive()
func NewDurableChannel[T any](client redis.UniversalClient, config DurableChannelConfig) *DurableChannel[T] {
	if config.Name == "" {
		panic("DurableChannel name is required")
	}

	// 设置默认值
	config.Namespace = mathx.IfEmpty(config.Namespace, "dchan")
	config.BufferSize = mathx.IfLeZero(config.BufferSize, 100)
	config.PollInterval = mathx.IfLeZero(config.PollInterval, 100*time.Millisecond)
	config.TTL = mathx.IfLeZero(config.TTL, 24*time.Hour)
	config.CompressionMinSize = mathx.IfLeZero(config.CompressionMinSize, 1024)
	config.QueryTimeout = mathx.IfLeZero(config.QueryTimeout, 3*time.Second)

	shouldStartWorker := config.PersistenceMode != PersistSync
	if !shouldStartWorker {
		config.WorkerCount = 0
	} else if config.WorkerCount == 0 {
		config.WorkerCount = 1 // 消费者模式默认启动 1 个 Worker
	}

	ctx, cancel := context.WithCancel(context.Background())

	dc := &DurableChannel[T]{
		config:  config,
		client:  client,
		memChan: make(chan T, config.BufferSize),
		ctx:     ctx,
		cancel:  cancel,
		logger:  NewDefaultCachexLogger(),
	}
	dc.queueKey = dc.GetQueueKey()

	// 创建持久化 Worker 池（仅用于 PersistAsync 模式，避免无限创建 goroutine）
	if config.PersistenceMode == PersistAsync {
		dc.persistPool = syncx.NewWorkerPool(config.WorkerCount, config.BufferSize)
	}

	// 恢复未消费的消息（在启动 worker 之前）
	if config.RecoveryOnStart && shouldStartWorker {
		dc.recoverMessages()
	}

	// 启动消费者 Workers
	for i := 0; i < config.WorkerCount; i++ {
		dc.startWorker(i)
	}

	return dc
}

// Send 发送消息（类似 channel <-)
func (dc *DurableChannel[T]) Send(ctx context.Context, data T) error {
	if dc.closed.Load() {
		return ErrChannelClosed
	}

	switch dc.config.PersistenceMode {
	case PersistSync:
		// 同步模式：先写 Redis
		return dc.persistToRedis(ctx, data)

	case PersistAsync:
		// 异步模式：先写内存，异步持久化
		select {
		case dc.memChan <- data:
			// 使用 WorkerPool 异步持久化（避免无限创建 goroutine）
			dc.persistPool.SubmitNonBlocking(func() {
				dc.persistToRedis(ctx, data)
			})
			return nil
		case <-dc.ctx.Done():
			return ErrChannelClosed
		case <-ctx.Done():
			return ctx.Err()
		}

	case PersistHybrid:
		// 混合模式：先写 Redis，再刷内存
		if err := dc.persistToRedis(ctx, data); err != nil {
			return err
		}

		// 再次检查是否已关闭（避免在 persistToRedis 期间被关闭）
		if dc.closed.Load() {
			return ErrChannelClosed
		}

		// 非阻塞写入内存（如果内存满了就跳过，因为已经持久化到 Redis）
		select {
		case dc.memChan <- data:
			return nil
		case <-dc.ctx.Done():
			return ErrChannelClosed
		default:
			// 内存满了，但数据已经在 Redis 中，worker 会拉取
			return nil
		}

	default:
		return fmt.Errorf("%w: %d", ErrUnknownPersistMode, dc.config.PersistenceMode)
	}
}

// Receive 接收消息（类似 <-channel）
func (dc *DurableChannel[T]) Receive() <-chan T {
	return dc.memChan
}

// TryReceive 非阻塞接收（类似 select case）
func (dc *DurableChannel[T]) TryReceive(ctx context.Context) (T, bool) {
	select {
	case data := <-dc.memChan:
		return data, true
	case <-ctx.Done():
		var zero T
		return zero, false
	default:
		var zero T
		return zero, false
	}
}

// Len 获取总队列长度（内存 + Redis）
func (dc *DurableChannel[T]) Len() (int64, error) {
	ctx, cancel := context.WithTimeout(contextx.OrBackground(dc.ctx), dc.config.QueryTimeout)
	defer cancel()

	redisLen, err := dc.client.LLen(ctx, dc.queueKey).Result()
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrGetRedisLenFailed, err)
	}
	return int64(len(dc.memChan)) + redisLen, nil
}

// MemLen 获取内存队列长度
func (dc *DurableChannel[T]) MemLen() int {
	return len(dc.memChan)
}

// RedisLen 获取 Redis 队列长度
func (dc *DurableChannel[T]) RedisLen() (int64, error) {
	ctx, cancel := context.WithTimeout(contextx.OrBackground(dc.ctx), dc.config.QueryTimeout)
	defer cancel()

	length, err := dc.client.LLen(ctx, dc.queueKey).Result()
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrGetRedisLenFailed, err)
	}
	return length, nil
}

// GetQueueKey 获取当前配置的队列 Key
func (dc *DurableChannel[T]) GetQueueKey() string {
	return fmt.Sprintf("%s:%s:%d:queue", dc.config.Namespace, dc.config.Name, dc.config.WorkerID)
}

// GetConfig 获取配置信息
func (dc *DurableChannel[T]) GetConfig() DurableChannelConfig {
	return dc.config
}

// Close 关闭 Channel
func (dc *DurableChannel[T]) Close() error {
	if !dc.closed.CompareAndSwap(false, true) {
		return nil // 已经关闭
	}

	dc.cancel()

	// 等待所有 worker 完成
	dc.wg.Wait()

	// 关闭持久化池（PersistAsync 模式）
	if dc.persistPool != nil {
		dc.persistPool.Close()
	}

	// 关闭内存 channel
	close(dc.memChan)

	return nil
}

// persistToRedis 持久化到 Redis
func (dc *DurableChannel[T]) persistToRedis(ctx context.Context, data T) error {
	dc.logger.Debugf("开始持久化消息到 Redis: %s", dc.queueKey)

	// 序列化
	compressed, err := zipx.ZlibCompressObject(data)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCompressFailed, err)
	}
	dc.logger.Debugf("消息序列化成功，压缩后大小: %d bytes", len(compressed))

	// 直接使用传入的 ctx，如果 ctx 被取消则返回错误
	// 写入 Redis
	pipe := dc.client.Pipeline()
	rpushCmd := pipe.RPush(ctx, dc.queueKey, compressed)
	expireCmd := pipe.Expire(ctx, dc.queueKey, dc.config.TTL)

	dc.logger.Debugf("执行 Pipeline: RPush to %s, Expire %v", dc.queueKey, dc.config.TTL)
	cmds, err := pipe.Exec(ctx)
	if err != nil {
		dc.logger.Errorf("Persist to redis failed: %v", err)
		return fmt.Errorf("%w: %w", ErrPersistFailed, err)
	}
	dc.logger.Debugf("Pipeline 执行成功，返回 %d 个命令结果", len(cmds))

	// 检查单个命令的错误
	if err := rpushCmd.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrRPushFailed, err)
	}
	rpushResult, _ := rpushCmd.Result()
	dc.logger.Debugf("RPush 成功，当前队列长度: %d", rpushResult)

	if err := expireCmd.Err(); err != nil {
		dc.logger.Warnf("Expire failed: %v", err)
		// Expire 失败不影响数据写入，只记录警告
	} else {
		expireResult, _ := expireCmd.Result()
		dc.logger.Debugf("Expire 成功: %v", expireResult)
	}

	dc.logger.Debugf("消息持久化完成: %s", dc.queueKey)
	return nil
}

// pullFromRedis 从 Redis 拉取消息（阻塞式）
func (dc *DurableChannel[T]) pullFromRedis(ctx context.Context) (T, error) {
	var zero T

	// 使用 BLPOP 阻塞式拉取（传入 ctx 确保可以被取消）
	result, err := dc.client.BLPop(ctx, dc.config.PollInterval, dc.queueKey).Result()
	if err != nil {
		if err == redis.Nil {
			return zero, nil // 超时，无数据
		}
		// 检查是否是 context 取消
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		return zero, fmt.Errorf("%w: %w", ErrPullFailed, err)
	}

	if len(result) < 2 {
		return zero, ErrInvalidBLPopResult
	}

	// 解压缩
	data, err := zipx.ZlibDecompressObject[T]([]byte(result[1]))
	if err != nil {
		return zero, fmt.Errorf("%w: %w", ErrDecompressFailed, err)
	}

	return data, nil
}

// pullFromRedisNonBlocking 从 Redis 非阻塞拉取消息（用于恢复）
func (dc *DurableChannel[T]) pullFromRedisNonBlocking(ctx context.Context) (T, error) {
	var zero T

	// 使用 LPOP 非阻塞拉取
	result, err := dc.client.LPop(ctx, dc.queueKey).Result()
	if err != nil {
		if err == redis.Nil {
			return zero, nil // 队列为空
		}
		return zero, fmt.Errorf("%w: %w", ErrPullFailed, err)
	}

	// 解压缩
	data, err := zipx.ZlibDecompressObject[T]([]byte(result))
	if err != nil {
		return zero, fmt.Errorf("%w: %w", ErrDecompressFailed, err)
	}

	return data, nil
}

// startWorker 启动消费者 Worker
func (dc *DurableChannel[T]) startWorker(id int) {
	dc.wg.Add(1)
	syncx.Go(dc.ctx).
		OnPanic(func(r any) {
			dc.logger.Errorf("Worker %d panic: %v", id, r)
		}).
		Exec(func() {
			defer dc.wg.Done()

			dc.logger.Debugf("Worker %d started for channel: %s", id, dc.config.Name)

			for {
				// 优先检查 context 是否已取消
				select {
				case <-dc.ctx.Done():
					dc.logger.Debugf("Worker %d stopped", id)
					return
				default:
				}

				// 从 Redis 拉取消息（使用 dc.ctx，确保可以被取消）
				data, err := dc.pullFromRedis(dc.ctx)
				if err != nil {
					// 检查是否是 context 取消导致的错误
					if dc.ctx.Err() != nil {
						dc.logger.Debugf("Worker %d stopped due to context cancellation", id)
						return
					}
					dc.logger.Warnf("Worker %d pull failed: %v", id, err)
					time.Sleep(time.Second)
					continue
				}

				// 检查是否为零值（超时）
				var zero T
				if fmt.Sprintf("%v", data) == fmt.Sprintf("%v", zero) {
					continue
				}

				// 推送到内存 channel
				select {
				case dc.memChan <- data:
					// 成功
				case <-dc.ctx.Done():
					// 放回 Redis（dc.ctx 已取消，persistToRedis 会快速返回错误）
					if err := dc.persistToRedis(dc.ctx, data); err != nil {
						dc.logger.Warnf("Worker %d failed to return message to Redis: %v", id, err)
					}
					dc.logger.Debugf("Worker %d stopped", id)
					return
				}
			}
		})
}

// recoverMessages 恢复未消费的消息
func (dc *DurableChannel[T]) recoverMessages() {
	// 直接使用 dc.ctx，如果已取消则快速返回
	if dc.ctx.Err() != nil {
		dc.logger.Debugf("Context cancelled, skip recovery")
		return
	}

	length, err := dc.client.LLen(dc.ctx, dc.queueKey).Result()
	if err != nil {
		dc.logger.Warnf("Failed to get queue length: %v", err)
		return
	}

	if length > 0 {
		dc.logger.Debugf("Recovering %d messages from Redis for channel: %s", length, dc.config.Name)

		recovered := 0
	RecoveryLoop:
		for i := int64(0); i < length && i < int64(dc.config.BufferSize); i++ {
			// 使用非阻塞拉取
			data, err := dc.pullFromRedisNonBlocking(dc.ctx)
			if err != nil {
				dc.logger.Warnf("Failed to recover message: %v", err)
				break
			}

			// 检查是否为零值（队列为空）
			var zero T
			if fmt.Sprintf("%v", data) == fmt.Sprintf("%v", zero) {
				break
			}

			// 推送到内存 channel
			select {
			case dc.memChan <- data:
				recovered++
			default:
				// 内存满了，放回 Redis
				if err := dc.persistToRedis(dc.ctx, data); err != nil {
					dc.logger.Warnf("Failed to persist message back: %v", err)
				}
				break RecoveryLoop
			}
		}

		dc.logger.Debugf("Recovered %d messages for channel: %s", recovered, dc.config.Name)
	}
}
