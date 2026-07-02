/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-07-02 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-07-02 01:00:00
 * @FilePath: \go-cachex\delay_queue.go
 * @Description: 高级分布式延迟队列，基于 Redis ZSET + Lua 原子脚本
 *
 * 特性：
 *   - 延迟执行：任务在指定时间戳到期后才被消费
 *   - ACK 确认：任务出队后进入 processing，处理成功才 ACK 删除；未 ACK 的任务在可见性超时后回归队列
 *   - 崩溃恢复：Pod 崩溃/停止时，processing 中未 ACK 的任务由其它 Pod 的 requeueStale 自动接管
 *   - 原子出队：Lua 脚本保证 ZRANGEBYSCORE + ZREM + ZADD(processing) 原子性，多实例安全
 *   - 顺序保证：Ordered 模式下同一 queue 内任务按到期时间串行处理，避免乱序
 *   - 堆积监控：暴露 ReadyLength/ProcessingLength，超过 MaxReadySize 阈值时记录告警日志
 *   - 任务去重：通过唯一 Key 标识任务，重复入队会覆盖执行时间（重新调度语义）
 *   - 任务取消：支持通过 Key 取消未执行/处理中的延迟任务
 *   - 失败重试：处理失败自动 ACK 后重新入队，支持指数退避
 *   - 死信队列：超过最大重试次数进入死信列表，便于人工干预
 *   - 优雅关闭：Stop 等待所有在途任务处理完成，未 ACK 的任务靠可见性超时兜底
 *
 * 适用场景：
 *   - 定时状态流转（如订单超时关闭、维护计划状态自动更新）
 *   - 延迟通知、定时任务分发
 *   - 跨实例的分布式定时协调
 *
 * 不适用场景：
 *   - 强一致性事务（Redis 故障可能丢失任务，重要任务需配合持久化补偿）
 *   - 毫秒级精度的实时调度（受轮询间隔限制，默认 1s）
 *
 * 存储结构：
 *   - ZSET {ns}:zset:{queue}         ready，score=执行时间戳(ms)     member=taskKey
 *   - ZSET {ns}:processing:{queue}   processing，score=可见性超时戳  member=taskKey
 *   - HASH {ns}:hash:{queue}         field=taskKey                   value=任务JSON（ready/processing 共用，ACK 时删除）
 *   - LIST {ns}:dead:{queue}         死信队列（LPush）
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/mathx"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
	"github.com/redis/go-redis/v9"
)

// 延迟队列错误
var (
	ErrDelayQueueClosed  = errors.New("delay queue is closed")
	ErrDelayQueueRunning = errors.New("delay queue consumer is already running")
	ErrInvalidDelayTask  = errors.New("invalid delay task: key required and execute time required")
	ErrDelayTaskNotFound = errors.New("delay task not found")
)

// DelayQueueConfig 延迟队列配置
type DelayQueueConfig struct {
	Namespace         string        // Redis 键命名空间前缀，默认 "delayq"
	PollInterval      time.Duration // 消费者轮询间隔，默认 1s
	BatchSize         int64         // 每次轮询最多拉取的到期任务数，默认 10
	Concurrency       int           // 单实例内并发处理任务数，默认 5（Ordered=true 时忽略）
	MaxRetries        int           // 最大重试次数（不含首次执行），默认 3
	RetryDelay        time.Duration // 失败重试基础延迟，默认 5s（实际按指数退避：delay * 2^retry）
	VisibilityTimeout time.Duration // 可见性超时：任务出队后多久未 ACK 视为失败回归队列，默认 5m
	Ordered           bool          // 顺序模式：同一 queue 内任务按到期时间串行处理，禁用 WorkerPool 并发
	MaxReadySize      int64         // ready 堆积告警阈值，超过时记录 Warn 日志，默认 10000（0 表示不检查）
	Logger            logger.ILogger
}

// DefaultDelayQueueConfig 返回默认配置
func DefaultDelayQueueConfig() DelayQueueConfig {
	return DelayQueueConfig{
		Namespace:         "delayq",
		PollInterval:      time.Second,
		BatchSize:         10,
		Concurrency:       5,
		MaxRetries:        3,
		RetryDelay:        5 * time.Second,
		VisibilityTimeout: 5 * time.Minute,
		MaxReadySize:      10000,
		Logger:            NewDefaultCachexLogger(),
	}
}

// DelayTask 延迟任务（对外暴露）
type DelayTask[T any] struct {
	Key        string    `json:"key"`         // 唯一标识，用于去重和取消
	Data       T         `json:"data"`        // 任务数据
	ExecuteAt  time.Time `json:"execute_at"`  // 执行时间（到期时间）
	RetryCount int       `json:"retry_count"` // 已重试次数
}

// delayTaskEnvelope 内部任务封装（存储到 Redis Hash 的 value）
// 与 DelayTask 字段一致，独立定义以解耦存储格式
type delayTaskEnvelope[T any] struct {
	Key        string    `json:"key"`
	Data       T         `json:"data"`
	ExecuteAt  time.Time `json:"execute_at"`
	RetryCount int       `json:"retry_count"`
}

// DelayQueue 分布式延迟队列
//
// 基于 Redis ZSET（score=执行时间戳）+ Hash（存储任务体）实现，
// 通过 Lua 脚本保证多实例下的原子出队，适合跨实例的延迟任务协调。
type DelayQueue[T any] struct {
	client *redis.Client
	config DelayQueueConfig

	mu        sync.Mutex
	closed    bool
	wg        sync.WaitGroup
	consumers map[string]context.CancelFunc // queueName -> cancel
	logger    logger.ILogger
}

// NewDelayQueue 创建延迟队列
// client 必须是非 nil 的 Redis 客户端；config 为空时使用默认配置
func NewDelayQueue[T any](client *redis.Client, config ...DelayQueueConfig) *DelayQueue[T] {
	cfg := DefaultDelayQueueConfig()
	if len(config) > 0 {
		c := config[0]
		cfg.Namespace = mathx.IfNotEmpty(c.Namespace, cfg.Namespace)
		cfg.PollInterval = mathx.IfNotZero(c.PollInterval, cfg.PollInterval)
		cfg.BatchSize = mathx.IfLeZero(c.BatchSize, cfg.BatchSize)
		cfg.Concurrency = mathx.IfLeZero(c.Concurrency, cfg.Concurrency)
		cfg.MaxRetries = mathx.IfLeZero(c.MaxRetries, cfg.MaxRetries)
		cfg.RetryDelay = mathx.IfNotZero(c.RetryDelay, cfg.RetryDelay)
		cfg.VisibilityTimeout = mathx.IfNotZero(c.VisibilityTimeout, cfg.VisibilityTimeout)
		cfg.Ordered = c.Ordered
		if c.MaxReadySize != 0 {
			cfg.MaxReadySize = c.MaxReadySize
		}
		if c.Logger != nil {
			cfg.Logger = c.Logger
		}
	}

	return &DelayQueue[T]{
		client:    client,
		config:    cfg,
		consumers: make(map[string]context.CancelFunc),
		logger:    cfg.Logger,
	}
}

// ========== 键名生成 ==========

func (q *DelayQueue[T]) zsetKey(queueName string) string {
	return fmt.Sprintf("%s:zset:%s", q.config.Namespace, queueName)
}

func (q *DelayQueue[T]) hashKey(queueName string) string {
	return fmt.Sprintf("%s:hash:%s", q.config.Namespace, queueName)
}

func (q *DelayQueue[T]) deadKey(queueName string) string {
	return fmt.Sprintf("%s:dead:%s", q.config.Namespace, queueName)
}

// processingKey 处理中 ZSet 的 key，score = 可见性超时时间戳
// 任务从 ready 出队后进入 processing，ACK 后删除；超时未 ACK 的任务由 requeueStale 移回 ready
func (q *DelayQueue[T]) processingKey(queueName string) string {
	return fmt.Sprintf("%s:processing:%s", q.config.Namespace, queueName)
}

// ========== Lua 脚本 ==========

// enqueueLua 入队脚本：ZADD（覆盖更新 score）+ HSET 原子操作
// 同一 Key 重复入队会更新 ExecuteAt，实现"重新调度"语义
const enqueueLuaScript = `
	local zsetKey = KEYS[1]
	local hashKey = KEYS[2]
	local score = tonumber(ARGV[1])
	local member = ARGV[2]
	local taskData = ARGV[3]
	redis.call("ZADD", zsetKey, score, member)
	redis.call("HSET", hashKey, member, taskData)
	return 1
`

// dequeueLua 原子出队脚本（ACK 模式）：
// 从 ready ZSet 取 score <= now 的任务，移到 processing ZSet（score = 可见性超时时间），
// Hash 数据保留（ACK 时才删除），返回任务 JSON 数组
// Pod 崩溃/停止时，processing 中超时未 ACK 的任务由 requeueStaleLua 回收
const dequeueLuaScript = `
	local zsetKey = KEYS[1]
	local processingKey = KEYS[2]
	local hashKey = KEYS[3]
	local now = tonumber(ARGV[1])
	local batchSize = tonumber(ARGV[2])
	local visibleAt = tonumber(ARGV[3])

	local results = redis.call("ZRANGEBYSCORE", zsetKey, "-inf", now, "LIMIT", 0, batchSize)
	if #results == 0 then
		return false
	end

	local tasks = {}
	for i = 1, #results do
		local member = results[i]
		local taskData = redis.call("HGET", hashKey, member)
		redis.call("ZREM", zsetKey, member)
		redis.call("ZADD", processingKey, visibleAt, member)
		if taskData then
			table.insert(tasks, taskData)
		end
	end

	if #tasks == 0 then
		return false
	end
	return tasks
`

// ackLua 确认任务完成：从 processing ZSet 删除 + 从 Hash 删除（原子）
// 只有持有该任务的消费者能 ACK（通过 member 即 taskKey 定位，无需锁）
const ackLuaScript = `
	local processingKey = KEYS[1]
	local hashKey = KEYS[2]
	local member = ARGV[1]
	local removed = redis.call("ZREM", processingKey, member)
	if removed > 0 then
		redis.call("HDEL", hashKey, member)
	end
	return removed
`

// requeueStaleLua 回收超时未 ACK 任务：
// 从 processing ZSet 取 score <= now（已超时）的任务，移回 ready ZSet（score = now，立即可消费）
// 多实例同时执行由 ZREM 原子性保证不重复
const requeueStaleLuaScript = `
	local processingKey = KEYS[1]
	local zsetKey = KEYS[2]
	local now = tonumber(ARGV[1])
	local batchSize = tonumber(ARGV[2])

	local results = redis.call("ZRANGEBYSCORE", processingKey, "-inf", now, "LIMIT", 0, batchSize)
	if #results == 0 then
		return 0
	end

	for i = 1, #results do
		local member = results[i]
		redis.call("ZREM", processingKey, member)
		redis.call("ZADD", zsetKey, now, member)
	end

	return #results
`

// pollLua 合并轮询脚本（requeueStale + dequeueExpired 单次原子完成，减少 50% Redis 往返）：
// 1. 回收 processing 中超时未 ACK 的任务，移回 ready（score=now，立即可消费）
// 2. 拉取 ready 中到期任务，移到 processing（score=visibleAt），Hash 保留
// 多实例并发由 ZREM 原子性保证不重复
const pollLuaScript = `
	local readyKey = KEYS[1]
	local processingKey = KEYS[2]
	local hashKey = KEYS[3]
	local now = tonumber(ARGV[1])
	local batchSize = tonumber(ARGV[2])
	local visibleAt = tonumber(ARGV[3])

	-- 1. 回收 processing 超时任务到 ready
	local stale = redis.call("ZRANGEBYSCORE", processingKey, "-inf", now, "LIMIT", 0, batchSize)
	for i = 1, #stale do
		redis.call("ZREM", processingKey, stale[i])
		redis.call("ZADD", readyKey, now, stale[i])
	end

	-- 2. 拉取 ready 到期任务到 processing
	local results = redis.call("ZRANGEBYSCORE", readyKey, "-inf", now, "LIMIT", 0, batchSize)
	if #results == 0 then
		return false
	end

	local tasks = {}
	for i = 1, #results do
		local member = results[i]
		local taskData = redis.call("HGET", hashKey, member)
		redis.call("ZREM", readyKey, member)
		redis.call("ZADD", processingKey, visibleAt, member)
		if taskData then
			table.insert(tasks, taskData)
		end
	end

	if #tasks == 0 then
		return false
	end
	return tasks
`

// cancelLua 取消任务脚本：同时从 ready/processing ZSet 删除 + Hash 删除
const cancelLuaScript = `
	local zsetKey = KEYS[1]
	local processingKey = KEYS[2]
	local hashKey = KEYS[3]
	local member = ARGV[1]
	local removed = redis.call("ZREM", zsetKey, member)
	removed = removed + redis.call("ZREM", processingKey, member)
	redis.call("HDEL", hashKey, member)
	return removed
`

// ========== 入队 ==========

// EnqueueAt 在指定时间执行任务
// 如果任务 Key 已存在，会覆盖更新执行时间（重新调度）
func (q *DelayQueue[T]) EnqueueAt(ctx context.Context, queueName string, task *DelayTask[T]) error {
	if q.isClosed() {
		return ErrDelayQueueClosed
	}
	if task == nil || task.Key == "" || task.ExecuteAt.IsZero() {
		return ErrInvalidDelayTask
	}

	// 序列化任务封装（存储到 Hash）
	envelope := delayTaskEnvelope[T]{
		Key:        task.Key,
		Data:       task.Data,
		ExecuteAt:  task.ExecuteAt,
		RetryCount: task.RetryCount,
	}
	taskData, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal delay task failed: %w", err)
	}

	score := float64(task.ExecuteAt.UnixMilli())
	zsetKey := q.zsetKey(queueName)
	hashKey := q.hashKey(queueName)

	if err := q.client.Eval(ctx, enqueueLuaScript, []string{zsetKey, hashKey}, score, task.Key, string(taskData)).Err(); err != nil {
		return fmt.Errorf("enqueue delay task failed: %w", err)
	}

	q.logger.Debugf("delay task enqueued: queue=%s key=%s execute_at=%s retry=%d",
		queueName, task.Key, task.ExecuteAt.Format(time.RFC3339), task.RetryCount)
	return nil
}

// EnqueueWithDelay 延迟指定时间后执行（ExecuteAt 会被覆盖为 now+delay）
func (q *DelayQueue[T]) EnqueueWithDelay(ctx context.Context, queueName string, task *DelayTask[T], delay time.Duration) error {
	if task == nil {
		return ErrInvalidDelayTask
	}
	task.ExecuteAt = time.Now().Add(delay)
	return q.EnqueueAt(ctx, queueName, task)
}

// ========== 取消 ==========

// Cancel 取消未执行的延迟任务
// 返回是否成功移除（false 表示任务不存在或已被消费）
func (q *DelayQueue[T]) Cancel(ctx context.Context, queueName, key string) (bool, error) {
	if q.isClosed() {
		return false, ErrDelayQueueClosed
	}

	result, err := q.client.Eval(ctx, cancelLuaScript,
		[]string{q.zsetKey(queueName), q.processingKey(queueName), q.hashKey(queueName)}, key).Int64()
	if err != nil {
		return false, fmt.Errorf("cancel delay task failed: %w", err)
	}

	if result > 0 {
		q.logger.Debugf("delay task cancelled: queue=%s key=%s", queueName, key)
		return true, nil
	}
	return false, nil
}

// ========== 查询 ==========

// Length 返回 ready 队列中待执行的任务数（兼容方法，等同 ReadyLength）
func (q *DelayQueue[T]) Length(ctx context.Context, queueName string) (int64, error) {
	return q.client.ZCard(ctx, q.zsetKey(queueName)).Result()
}

// ReadyLength 返回 ready 队列中待执行的任务数
func (q *DelayQueue[T]) ReadyLength(ctx context.Context, queueName string) (int64, error) {
	return q.client.ZCard(ctx, q.zsetKey(queueName)).Result()
}

// ProcessingLength 返回 processing 中处理中（未 ACK）的任务数
// 持续增长通常意味着消费者处理过慢或 Pod 频繁崩溃未 ACK
func (q *DelayQueue[T]) ProcessingLength(ctx context.Context, queueName string) (int64, error) {
	return q.client.ZCard(ctx, q.processingKey(queueName)).Result()
}

// DeadLength 返回死信队列长度
func (q *DelayQueue[T]) DeadLength(ctx context.Context, queueName string) (int64, error) {
	return q.client.LLen(ctx, q.deadKey(queueName)).Result()
}

// GetTask 获取任务详情（未执行的）
func (q *DelayQueue[T]) GetTask(ctx context.Context, queueName, key string) (*DelayTask[T], error) {
	data, err := q.client.HGet(ctx, q.hashKey(queueName), key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrDelayTaskNotFound
		}
		return nil, err
	}

	var envelope delayTaskEnvelope[T]
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal delay task failed: %w", err)
	}

	return &DelayTask[T]{
		Key:        envelope.Key,
		Data:       envelope.Data,
		ExecuteAt:  envelope.ExecuteAt,
		RetryCount: envelope.RetryCount,
	}, nil
}

// GetDeadTasks 获取死信队列任务列表（分页）
func (q *DelayQueue[T]) GetDeadTasks(ctx context.Context, queueName string, offset, limit int64) ([]*DelayTask[T], error) {
	if limit <= 0 {
		limit = 50
	}
	result, err := q.client.LRange(ctx, q.deadKey(queueName), offset, offset+limit-1).Result()
	if err != nil {
		return nil, err
	}

	tasks := make([]*DelayTask[T], 0, len(result))
	for _, data := range result {
		var envelope delayTaskEnvelope[T]
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			continue
		}
		tasks = append(tasks, &DelayTask[T]{
			Key:        envelope.Key,
			Data:       envelope.Data,
			ExecuteAt:  envelope.ExecuteAt,
			RetryCount: envelope.RetryCount,
		})
	}
	return tasks, nil
}

// ========== 出队（Lua 原子） ==========

// dequeueExpired 原子获取到期任务
func (q *DelayQueue[T]) dequeueExpired(ctx context.Context, queueName string) ([]*DelayTask[T], error) {
	now := float64(time.Now().UnixMilli())
	// 可见性超时时间戳：任务进入 processing 后，超过此时间未 ACK 视为失败
	visibleAt := float64(time.Now().Add(q.config.VisibilityTimeout).UnixMilli())
	result, err := q.client.Eval(ctx, dequeueLuaScript,
		[]string{q.zsetKey(queueName), q.processingKey(queueName), q.hashKey(queueName)},
		now, q.config.BatchSize, visibleAt).Result()
	if err != nil {
		// Lua 返回 false 时 go-redis 解析为 redis.Nil
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}

	// nil interface（Lua 返回 false/nil）
	if result == nil {
		return nil, nil
	}

	parts, ok := result.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected dequeue result type: %T", result)
	}

	tasks := make([]*DelayTask[T], 0, len(parts))
	for _, p := range parts {
		dataStr, ok := p.(string)
		if !ok {
			continue
		}
		var envelope delayTaskEnvelope[T]
		if err := json.Unmarshal([]byte(dataStr), &envelope); err != nil {
			q.logger.Errorf("unmarshal dequeued delay task failed: %v", err)
			continue
		}
		tasks = append(tasks, &DelayTask[T]{
			Key:        envelope.Key,
			Data:       envelope.Data,
			ExecuteAt:  envelope.ExecuteAt,
			RetryCount: envelope.RetryCount,
		})
	}

	return tasks, nil
}

// ack 确认任务处理完成：从 processing ZSet 删除 + 从 Hash 删除
// 必须在 handler 成功后调用，否则任务会在可见性超时后回归 ready 被重新消费
func (q *DelayQueue[T]) ack(ctx context.Context, queueName, taskKey string) error {
	removed, err := q.client.Eval(ctx, ackLuaScript,
		[]string{q.processingKey(queueName), q.hashKey(queueName)}, taskKey).Int64()
	if err != nil {
		return fmt.Errorf("ack delay task failed: %w", err)
	}
	if removed == 0 {
		// 任务已不在 processing（可能已被 requeueStale 回收并重新消费），视为幂等成功
		q.logger.Debugf("ack no-op, task not in processing: queue=%s key=%s", queueName, taskKey)
	}
	return nil
}

// requeueStale 回收 processing 中超时未 ACK 的任务，移回 ready（score=now，立即可消费）
// 多实例并发执行由 ZREM 原子性保证不重复回收同一任务
// 返回回收的任务数
func (q *DelayQueue[T]) requeueStale(ctx context.Context, queueName string) (int64, error) {
	now := float64(time.Now().UnixMilli())
	count, err := q.client.Eval(ctx, requeueStaleLuaScript,
		[]string{q.processingKey(queueName), q.zsetKey(queueName)},
		now, q.config.BatchSize).Int64()
	if err != nil {
		// Lua 返回 0 时正常，不会触发 redis.Nil（return 0 不是 false）
		return 0, fmt.Errorf("requeue stale tasks failed: %w", err)
	}
	if count > 0 {
		q.logger.Warnf("requeued stale tasks: queue=%s count=%d", queueName, count)
	}
	return count, nil
}

// pollExpired 原子轮询（合并 requeueStale + dequeueExpired 为单次 Lua 调用）：
// 1. 回收 processing 中超时未 ACK 的任务，移回 ready
// 2. 拉取 ready 中到期任务，移到 processing
// 多 Pod 场景下每轮 poll 仅 1 次 Redis 往返（vs 之前 2 次）
func (q *DelayQueue[T]) pollExpired(ctx context.Context, queueName string) ([]*DelayTask[T], error) {
	now := float64(time.Now().UnixMilli())
	visibleAt := float64(time.Now().Add(q.config.VisibilityTimeout).UnixMilli())
	result, err := q.client.Eval(ctx, pollLuaScript,
		[]string{q.zsetKey(queueName), q.processingKey(queueName), q.hashKey(queueName)},
		now, q.config.BatchSize, visibleAt).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("poll expired tasks failed: %w", err)
	}
	if result == nil {
		return nil, nil
	}

	arr, ok := result.([]interface{})
	if !ok {
		return nil, nil
	}
	if len(arr) == 0 {
		return nil, nil
	}

	tasks := make([]*DelayTask[T], 0, len(arr))
	for _, item := range arr {
		dataStr, ok := item.(string)
		if !ok {
			continue
		}
		var envelope delayTaskEnvelope[T]
		if err := json.Unmarshal([]byte(dataStr), &envelope); err != nil {
			q.logger.Errorf("unmarshal polled delay task failed: queue=%s err=%v", queueName, err)
			continue
		}
		tasks = append(tasks, &DelayTask[T]{
			Key:        envelope.Key,
			Data:       envelope.Data,
			ExecuteAt:  envelope.ExecuteAt,
			RetryCount: envelope.RetryCount,
		})
	}
	return tasks, nil
}

// ========== 消费者 ==========

// DelayConsumerOptions 消费者选项（覆盖队列级配置）
type DelayConsumerOptions struct {
	// OverrideMaxRetries 覆盖最大重试次数（>0 生效，0 表示用队列配置）
	OverrideMaxRetries int
	// OverrideRetryDelay 覆盖重试延迟（>0 生效，0 表示用队列配置）
	OverrideRetryDelay time.Duration
}

// StartConsumer 启动消费者 goroutine
// 同一队列名重复调用会返回 ErrDelayQueueRunning
// handler 返回 error 时任务会重新入队（带指数退避延迟），超过最大重试次数后进入死信队列
func (q *DelayQueue[T]) StartConsumer(
	ctx context.Context,
	queueName string,
	handler func(context.Context, *DelayTask[T]) error,
	opts ...DelayConsumerOptions,
) error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return ErrDelayQueueClosed
	}
	if _, exists := q.consumers[queueName]; exists {
		q.mu.Unlock()
		return ErrDelayQueueRunning
	}
	q.mu.Unlock()

	// 合并消费者选项
	opt := DelayConsumerOptions{}
	if len(opts) > 0 {
		opt = opts[0]
	}
	maxRetries := q.config.MaxRetries
	if opt.OverrideMaxRetries > 0 {
		maxRetries = opt.OverrideMaxRetries
	}
	retryDelay := q.config.RetryDelay
	if opt.OverrideRetryDelay > 0 {
		retryDelay = opt.OverrideRetryDelay
	}

	consumerCtx, cancel := context.WithCancel(ctx)

	q.mu.Lock()
	// 再次检查关闭状态（可能在加锁前被 Stop）
	if q.closed {
		q.mu.Unlock()
		cancel()
		return ErrDelayQueueClosed
	}
	q.consumers[queueName] = cancel
	q.mu.Unlock()

	// 本地 WorkerPool 用于并发处理
	worker := syncx.NewWorkerPool(q.config.Concurrency, q.config.Concurrency*10)

	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		defer func() {
			q.mu.Lock()
			delete(q.consumers, queueName)
			q.mu.Unlock()
			_ = worker.Close()
			q.logger.Infof("delay queue consumer stopped: queue=%s", queueName)
		}()

		ticker := time.NewTicker(q.config.PollInterval)
		defer ticker.Stop()

		q.logger.Infof("delay queue consumer started: queue=%s concurrency=%d poll=%v",
			queueName, q.config.Concurrency, q.config.PollInterval)

		for {
			select {
			case <-consumerCtx.Done():
				return
			case <-ticker.C:
				q.pollOnce(consumerCtx, queueName, handler, worker, maxRetries, retryDelay)
			}
		}
	}()

	return nil
}

// pollOnce 执行一次轮询（单次 Lua 原子完成回收+出队）→ 堆积监控 → 分发处理
func (q *DelayQueue[T]) pollOnce(
	ctx context.Context,
	queueName string,
	handler func(context.Context, *DelayTask[T]) error,
	worker *syncx.WorkerPool,
	maxRetries int,
	retryDelay time.Duration,
) {
	// 1. 堆积监控：ready 超过阈值时告警（ZCARD 开销小，仅 MaxReadySize>0 时执行）
	if q.config.MaxReadySize > 0 {
		if readyLen, err := q.ReadyLength(ctx, queueName); err == nil && readyLen > q.config.MaxReadySize {
			q.logger.Warnf("delay queue backlog over threshold: queue=%s ready=%d threshold=%d",
				queueName, readyLen, q.config.MaxReadySize)
		}
	}

	// 2. 原子轮询：回收超时任务 + 拉取到期任务（单次 Lua，多 Pod 安全）
	tasks, err := q.pollExpired(ctx, queueName)
	if err != nil {
		q.logger.Errorf("poll expired tasks failed: queue=%s err=%v", queueName, err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	// 3. 分发处理：Ordered 串行保证顺序，否则并发提交 WorkerPool
	if q.config.Ordered {
		for _, task := range tasks {
			if ctx.Err() != nil {
				return
			}
			q.handleTask(ctx, queueName, task, handler, maxRetries, retryDelay)
		}
		return
	}

	for _, task := range tasks {
		task := task // 捕获循环变量
		if err := worker.SubmitNonBlocking(func() {
			q.handleTask(ctx, queueName, task, handler, maxRetries, retryDelay)
		}); err != nil {
			// WorkerPool 满：任务已在 processing，先 ACK 再重新入队 ready（用独立 context 避免 ctx 取消）
			ackCtx := context.Background()
			_ = q.ack(ackCtx, queueName, task.Key)
			if reErr := q.reEnqueue(ackCtx, queueName, task); reErr != nil {
				q.logger.Errorf("re-enqueue on pool full failed: queue=%s key=%s err=%v", queueName, task.Key, reErr)
			} else {
				q.logger.Warnf("worker pool full, re-enqueue task: queue=%s key=%s", queueName, task.Key)
			}
		}
	}
}

// handleTask 处理单个任务（ACK 模式：成功 ACK 删除，失败 ACK 后重试/死信）
// 任务在 dequeueExpired 时已原子移入 processing，ACK 负责确认完成
// Pod 崩溃/停止时未 ACK 的任务由 requeueStale 在可见性超时后回收，由其它 Pod 接管
func (q *DelayQueue[T]) handleTask(
	ctx context.Context,
	queueName string,
	task *DelayTask[T],
	handler func(context.Context, *DelayTask[T]) error,
	maxRetries int,
	retryDelay time.Duration,
) {
	// 执行业务处理
	if err := handler(ctx, task); err != nil {
		q.logger.Warnf("delay task handler failed: queue=%s key=%s retry=%d/%d err=%v",
			queueName, task.Key, task.RetryCount, maxRetries, err)

		// 处理失败：用独立 context 确保 ACK/重试/死信操作不受 ctx 取消影响
		// 即使这些操作全部失败，requeueStale 也会在可见性超时后兜底回收
		ackCtx := context.Background()
		_ = q.ack(ackCtx, queueName, task.Key)

		if task.RetryCount >= maxRetries {
			// 超过最大重试次数，进入死信队列
			q.moveToDead(ackCtx, queueName, task)
			return
		}

		// 指数退避重新入队：delay * 2^retry
		task.RetryCount++
		delay := retryDelay * time.Duration(1<<uint(task.RetryCount))
		if reErr := q.reEnqueueWithDelay(ackCtx, queueName, task, delay); reErr != nil {
			q.logger.Errorf("re-enqueue on handler failed failed: queue=%s key=%s err=%v", queueName, task.Key, reErr)
		}
		return
	}

	// 处理成功：ACK 确认（用独立 context，Pod 优雅停止时 ctx 可能已取消）
	if err := q.ack(context.Background(), queueName, task.Key); err != nil {
		// ACK 失败：任务会在可见性超时后回归 ready 被重新消费（可能重复执行）
		// 业务 handler 需要幂等；此处仅记录日志，requeueStale 兜底
		q.logger.Errorf("ack failed, task will be requeued after timeout: queue=%s key=%s err=%v", queueName, task.Key, err)
		return
	}
	q.logger.Debugf("delay task processed: queue=%s key=%s", queueName, task.Key)
}

// reEnqueue 重新入队（立即到期，保持原 RetryCount）
func (q *DelayQueue[T]) reEnqueue(ctx context.Context, queueName string, task *DelayTask[T]) error {
	task.ExecuteAt = time.Now()
	return q.EnqueueAt(ctx, queueName, task)
}

// reEnqueueWithDelay 延迟重新入队
func (q *DelayQueue[T]) reEnqueueWithDelay(ctx context.Context, queueName string, task *DelayTask[T], delay time.Duration) error {
	task.ExecuteAt = time.Now().Add(delay)
	return q.EnqueueAt(ctx, queueName, task)
}

// moveToDead 移入死信队列
func (q *DelayQueue[T]) moveToDead(ctx context.Context, queueName string, task *DelayTask[T]) {
	envelope := delayTaskEnvelope[T]{
		Key:        task.Key,
		Data:       task.Data,
		ExecuteAt:  task.ExecuteAt,
		RetryCount: task.RetryCount,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		q.logger.Errorf("marshal dead task failed: queue=%s key=%s err=%v", queueName, task.Key, err)
		return
	}

	if err := q.client.LPush(ctx, q.deadKey(queueName), data).Err(); err != nil {
		q.logger.Errorf("push to dead queue failed: queue=%s key=%s err=%v", queueName, task.Key, err)
		return
	}

	q.logger.Warnf("delay task moved to dead queue: queue=%s key=%s retries=%d",
		queueName, task.Key, task.RetryCount)
}

// ========== 生命周期 ==========

// StopConsumer 停止指定队列的消费者（不等待在途任务）
func (q *DelayQueue[T]) StopConsumer(queueName string) {
	q.mu.Lock()
	cancel, exists := q.consumers[queueName]
	if exists {
		delete(q.consumers, queueName)
	}
	q.mu.Unlock()

	if exists {
		cancel()
	}
}

// Stop 停止所有消费者并等待在途任务处理完成
func (q *DelayQueue[T]) Stop() error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil
	}
	q.closed = true
	for name, cancel := range q.consumers {
		cancel()
		delete(q.consumers, name)
	}
	q.mu.Unlock()

	// 等待所有消费者 goroutine 退出（含 WorkerPool.Close 内部等待）
	q.wg.Wait()
	q.logger.Info("delay queue stopped")
	return nil
}

// isClosed 检查是否已关闭
func (q *DelayQueue[T]) isClosed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed
}
