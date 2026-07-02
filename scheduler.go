/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-07-02 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-07-02 13:34:42
 * @FilePath: \apex-core-service\go-cachex\scheduler.go
 * @Description: go-cachex 调度器管理器
 *
 * 复用全局 Redis 客户端与日志器（与 KVCache 同一套注入机制），
 * 业务侧一行 RegisterScheduler 即可注册调度器，宿主服务统一
 * 调用 StartAllSchedulers / StopAllSchedulers 启停所有调度器
 *
 * 使用示例：
 *
 *	// 1. bootstrap 阶段注入全局依赖（与 RegisterKV 共用）
 *	cachex.SetGlobalRedisClient(gwglobal.REDIS)
 *	cachex.SetLogger(gwglobal.LOGGER)
 *
 *	// 2. 业务侧注册调度器（handler 为任务处理函数）
 *	sched := cachex.RegisterScheduler[MyData]("my_scheduler", handleFunc,
 *	    cachex.WithSchedulerQueueName("core:my_queue"),
 *	    cachex.WithSchedulerNamespace("core:my"))
 *
 *	// 3. 业务侧入队/取消
 *	sched.EnqueueAt(ctx, &cachex.DelayTask[MyData]{Key: "k1", Data: d, ExecuteAt: t})
 *	sched.Cancel(ctx, "k1")
 *
 *	// 4. bootstrap 阶段统一启停
 *	cachex.StartAllSchedulers(ctx)
 *	cachex.StopAllSchedulers()
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SchedulerHandler 任务处理函数类型
// handler 返回 error 时任务会按指数退避重试，超过最大重试次数进入死信队列
type SchedulerHandler[T any] func(ctx context.Context, task *DelayTask[T]) error

// Scheduler 调度器接口（非泛型，用于全局注册表存储）
//
// 业务侧的 *SchedulerManager[T] 实现此接口，
// StartAllSchedulers / StopAllSchedulers 通过此接口统一管理
type Scheduler interface {
	// Start 启动消费者（同一调度器重复调用返回 ErrDelayQueueRunning）
	Start(ctx context.Context) error
	// Stop 停止消费者并等待在途任务处理完成
	Stop() error
	// Name 返回调度器名称（全局唯一）
	Name() string
}

// SchedulerConfig 调度器配置
type SchedulerConfig struct {
	QueueName         string        // 队列名（必填，不同调度器用不同队列名隔离）
	Namespace         string        // Redis 键命名空间前缀，默认 "delayq"
	PollInterval      time.Duration // 消费者轮询间隔，默认 1s
	BatchSize         int64         // 每次轮询最多拉取的到期任务数，默认 10
	Concurrency       int           // 单实例内并发处理任务数，默认 5（Ordered=true 时忽略）
	MaxRetries        int           // 最大重试次数（不含首次执行），默认 3
	RetryDelay        time.Duration // 失败重试基础延迟，默认 5s（实际按指数退避）
	VisibilityTimeout time.Duration // 可见性超时：任务出队后多久未 ACK 视为失败回归队列，默认 5m
	Ordered           bool          // 顺序模式：同一 queue 内任务按到期时间串行处理
	MaxReadySize      int64         // ready 堆积告警阈值，超过时记录 Warn 日志，默认 10000（0 表示不检查）
}

// SchedulerOption 调度器配置选项
type SchedulerOption func(*SchedulerConfig)

// WithSchedulerQueueName 设置队列名（必填）
func WithSchedulerQueueName(name string) SchedulerOption {
	return func(c *SchedulerConfig) { c.QueueName = name }
}

// WithSchedulerNamespace 设置命名空间
func WithSchedulerNamespace(ns string) SchedulerOption {
	return func(c *SchedulerConfig) { c.Namespace = ns }
}

// WithSchedulerPollInterval 设置轮询间隔
func WithSchedulerPollInterval(d time.Duration) SchedulerOption {
	return func(c *SchedulerConfig) { c.PollInterval = d }
}

// WithSchedulerBatchSize 设置批量拉取大小
func WithSchedulerBatchSize(n int64) SchedulerOption {
	return func(c *SchedulerConfig) { c.BatchSize = n }
}

// WithSchedulerConcurrency 设置并发数
func WithSchedulerConcurrency(n int) SchedulerOption {
	return func(c *SchedulerConfig) { c.Concurrency = n }
}

// WithSchedulerMaxRetries 设置最大重试次数
func WithSchedulerMaxRetries(n int) SchedulerOption {
	return func(c *SchedulerConfig) { c.MaxRetries = n }
}

// WithSchedulerRetryDelay 设置重试延迟
func WithSchedulerRetryDelay(d time.Duration) SchedulerOption {
	return func(c *SchedulerConfig) { c.RetryDelay = d }
}

// WithSchedulerVisibilityTimeout 设置可见性超时（任务出队后多久未 ACK 回归队列）
func WithSchedulerVisibilityTimeout(d time.Duration) SchedulerOption {
	return func(c *SchedulerConfig) { c.VisibilityTimeout = d }
}

// WithSchedulerOrdered 启用顺序模式（同一 queue 内任务按到期时间串行处理）
func WithSchedulerOrdered() SchedulerOption {
	return func(c *SchedulerConfig) { c.Ordered = true }
}

// WithSchedulerMaxReadySize 设置 ready 堆积告警阈值（0 表示不检查）
func WithSchedulerMaxReadySize(n int64) SchedulerOption {
	return func(c *SchedulerConfig) { c.MaxReadySize = n }
}

// SchedulerManager 调度器管理器（泛型）
//
// 包装 DelayQueue[T]，固定一个 queueName，提供简化的入队/取消/启停 API
// 通过 RegisterScheduler 注册到全局表，宿主服务统一启停
type SchedulerManager[T any] struct {
	name    string
	config  SchedulerConfig
	queue   *DelayQueue[T]
	handler SchedulerHandler[T]
}

// 全局调度器注册表
var (
	schedulerRegistry   = make(map[string]Scheduler)
	schedulerRegistryMu sync.RWMutex
)

// RegisterScheduler 注册调度器（pbmo 风格）
//
// 使用者只需在初始化阶段调用一次，后续可通过 GetScheduler[T](name) 获取实例
// 必须先调用 SetGlobalRedisClient 注入 Redis 客户端
// name 必须全局唯一，重复注册将 panic
//
//	cachex.RegisterScheduler[MyData]("my_scheduler", handleFunc,
//	    cachex.WithSchedulerQueueName("core:my_queue"))
func RegisterScheduler[T any](name string, handler SchedulerHandler[T], opts ...SchedulerOption) *SchedulerManager[T] {
	if globalRedisClient == nil {
		panic("cachex: global redis client not set, call SetGlobalRedisClient first")
	}
	if handler == nil {
		panic(fmt.Sprintf("cachex: scheduler %q handler is nil", name))
	}

	schedulerRegistryMu.Lock()
	defer schedulerRegistryMu.Unlock()
	if _, exists := schedulerRegistry[name]; exists {
		panic(fmt.Sprintf("cachex: scheduler %q already registered", name))
	}

	// 合并配置
	cfg := SchedulerConfig{
		Namespace:         "delayq",
		PollInterval:      time.Second,
		BatchSize:         10,
		Concurrency:       5,
		MaxRetries:        3,
		RetryDelay:        5 * time.Second,
		VisibilityTimeout: 5 * time.Minute,
		MaxReadySize:      10000,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.QueueName == "" {
		panic(fmt.Sprintf("cachex: scheduler %q queue name is required, use WithSchedulerQueueName", name))
	}

	// 构建 DelayQueue 配置
	dqCfg := DelayQueueConfig{
		Namespace:         cfg.Namespace,
		PollInterval:      cfg.PollInterval,
		BatchSize:         cfg.BatchSize,
		Concurrency:       cfg.Concurrency,
		MaxRetries:        cfg.MaxRetries,
		RetryDelay:        cfg.RetryDelay,
		VisibilityTimeout: cfg.VisibilityTimeout,
		Ordered:           cfg.Ordered,
		MaxReadySize:      cfg.MaxReadySize,
		Logger:            globalLogger,
	}

	sm := &SchedulerManager[T]{
		name:    name,
		config:  cfg,
		queue:   NewDelayQueue[T](globalRedisClient, dqCfg),
		handler: handler,
	}
	schedulerRegistry[name] = sm
	return sm
}

// GetScheduler 按名称获取已注册的调度器
func GetScheduler[T any](name string) (*SchedulerManager[T], error) {
	schedulerRegistryMu.RLock()
	s, ok := schedulerRegistry[name]
	schedulerRegistryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("cachex: scheduler %q not registered", name)
	}
	typed, ok := s.(*SchedulerManager[T])
	if !ok {
		return nil, fmt.Errorf("cachex: scheduler %q type mismatch", name)
	}
	return typed, nil
}

// MustGetScheduler 按名称获取已注册的调度器，未注册时 panic（适合初始化阶段使用）
func MustGetScheduler[T any](name string) *SchedulerManager[T] {
	s, err := GetScheduler[T](name)
	if err != nil {
		panic(err)
	}
	return s
}

// StartAllSchedulers 启动所有已注册的调度器（服务启动时调用）
// 任一启动失败立即返回错误，已启动的不会被回滚（可再次调用 StopAllSchedulers 清理）
func StartAllSchedulers(ctx context.Context) error {
	schedulerRegistryMu.RLock()
	defer schedulerRegistryMu.RUnlock()
	for _, s := range schedulerRegistry {
		if err := s.Start(ctx); err != nil && err != ErrDelayQueueRunning {
			return fmt.Errorf("start scheduler %q failed: %w", s.Name(), err)
		}
	}
	return nil
}

// StopAllSchedulers 停止所有已注册的调度器（服务关闭时调用）
// 收集所有错误，返回第一个错误（其余错误仅记录日志）
func StopAllSchedulers() error {
	schedulerRegistryMu.RLock()
	defer schedulerRegistryMu.RUnlock()
	var firstErr error
	for _, s := range schedulerRegistry {
		if err := s.Stop(); err != nil {
			if globalLogger != nil {
				globalLogger.Errorf("stop scheduler %q failed: %v", s.Name(), err)
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ========== SchedulerManager 方法 ==========

// Name 返回调度器名称
func (m *SchedulerManager[T]) Name() string { return m.name }

// Start 启动消费者（实现 Scheduler 接口）
// 同一调度器重复调用返回 ErrDelayQueueRunning
func (m *SchedulerManager[T]) Start(ctx context.Context) error {
	return m.queue.StartConsumer(ctx, m.config.QueueName, m.handler)
}

// Stop 停止消费者并等待在途任务处理完成（实现 Scheduler 接口）
func (m *SchedulerManager[T]) Stop() error {
	return m.queue.Stop()
}

// EnqueueAt 入队任务（到指定时间执行）
// 同一 Key 重复入队会覆盖 ExecuteAt，实现"重新调度"语义
func (m *SchedulerManager[T]) EnqueueAt(ctx context.Context, task *DelayTask[T]) error {
	return m.queue.EnqueueAt(ctx, m.config.QueueName, task)
}

// EnqueueWithDelay 入队任务（延迟指定时间后执行）
func (m *SchedulerManager[T]) EnqueueWithDelay(ctx context.Context, task *DelayTask[T], delay time.Duration) error {
	return m.queue.EnqueueWithDelay(ctx, m.config.QueueName, task, delay)
}

// Cancel 取消任务（按 Key），返回是否成功取消
func (m *SchedulerManager[T]) Cancel(ctx context.Context, key string) (bool, error) {
	return m.queue.Cancel(ctx, m.config.QueueName, key)
}

// Length 队列中待执行的任务数
func (m *SchedulerManager[T]) Length(ctx context.Context) (int64, error) {
	return m.queue.Length(ctx, m.config.QueueName)
}

// GetTask 查询任务（按 Key）
func (m *SchedulerManager[T]) GetTask(ctx context.Context, key string) (*DelayTask[T], error) {
	return m.queue.GetTask(ctx, m.config.QueueName, key)
}

// DeadLength 死信队列长度
func (m *SchedulerManager[T]) DeadLength(ctx context.Context) (int64, error) {
	return m.queue.DeadLength(ctx, m.config.QueueName)
}

// GetDeadTasks 查询死信队列任务
func (m *SchedulerManager[T]) GetDeadTasks(ctx context.Context, offset, limit int64) ([]*DelayTask[T], error) {
	return m.queue.GetDeadTasks(ctx, m.config.QueueName, offset, limit)
}
