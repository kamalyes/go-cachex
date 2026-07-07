/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-07-06 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-07-06 23:52:16
 * @FilePath: \go-cachex\object_pool.go
 * @Description: 通用对象预生成池（pbmo 风格），削减高并发下实时构造对象的 CPU 冲击
 *
 * ============================================================================
 * 设计目标
 * ============================================================================
 * 一行注册：业务侧注入 factory 即可，宿主自动完成冷启动预填充、后台周期补充
 * 适用于 CPU 密集型对象的预生成（如 RSA 密钥对、序列化模板、连接预热等）
 *
 * 特性：
 *   1. 注册即启动（冷启动同步预生成少量 + 异步补齐 + 周期任务兜底）
 *   2. 低水位阈值触发补充，防重叠执行避免堆积
 *   3. 池空时调用方可回退实时构造，保证可用性
 *   4. 全局注册表，宿主可统一 StopAllObjectPools 优雅关闭
 *   5. 复用 go-toolbox syncx.PeriodicTaskManager / syncx.Go，不造轮子
 *
 * ============================================================================
 * 使用方式
 * ============================================================================
 * 1. bootstrap 阶段注入全局依赖（与 KVCache/Scheduler 共用）：
 *
 *	cachex.SetLogger(gwglobal.LOGGER)
 *
 * 2. 业务侧定义对象类型并注册（注册即启动）：
 *
 *	type TempKeyPem struct {
 *	    PublicKeyPem  string
 *	    PrivateKeyPem string
 *	}
 *
 *	pool := cachex.RegisterObjectPool[TempKeyPem]("temp_key_pool", func() (TempKeyPem, error) {
 *	    // CPU 密集型构造逻辑
 *	    return TempKeyPem{...}, nil
 *	},
 *	    cachex.WithObjectPoolCapacity(64),
 *	    cachex.WithObjectPoolColdStartCount(8))
 *
 * 3. 业务侧取用（缓存 pool 引用，避免每次查注册表）：
 *
 *	if v, ok := pool.TryGet(); ok {
 *	    // 命中池，0 次 CPU 构造
 *	} else {
 *	    // 回退实时构造
 *	}
 *
 * 4. bootstrap 阶段优雅关闭（可选，进程退出时 goroutine 也会被回收）：
 *
 *	cachex.StopAllObjectPools()
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
	"github.com/kamalyes/go-toolbox/pkg/syncx"
)

// ============================================================
// 公共类型
// ============================================================

// PoolFactory 对象工厂函数：生成一个对象放入池
// 返回 error 时跳过该对象并记录日志，继续生成下一个
type PoolFactory[T any] func() (T, error)

// ObjectPool 对象池接口（非泛型，用于全局注册表存储）
//
// 业务侧的 *ObjectPoolManager[T] 实现此接口，
// StartAllObjectPools / StopAllObjectPools 通过此接口统一管理
type ObjectPool interface {
	// Start 启动池（冷启动预填充 + 周期补充任务），重复调用幂等
	Start(ctx context.Context) error
	// Stop 停止周期补充任务，正在进行的预填充尽快退出
	Stop() error
	// Name 返回池名称（全局唯一）
	Name() string
}

// ObjectPoolConfig 对象池配置
type ObjectPoolConfig struct {
	Capacity        int            // 池容量（缓冲区大小），必填
	MinThreshold    int            // 低水位阈值，低于此值触发补充，默认 Capacity/3
	BatchSize       int            // 每次周期补充的批量上限，默认 Capacity/2
	ColdStartCount  int            // 冷启动同步预生成数量（保证启动即可用），默认 Capacity/8
	RefreshInterval time.Duration  // 补充检查间隔，默认 5s
	Logger          logger.ILogger // 日志器，未设置时回退到 globalLogger
}

// ObjectPoolOption 对象池配置选项
type ObjectPoolOption func(*ObjectPoolConfig)

// WithObjectPoolCapacity 设置池容量
func WithObjectPoolCapacity(n int) ObjectPoolOption {
	return func(c *ObjectPoolConfig) { c.Capacity = n }
}

// WithObjectPoolMinThreshold 设置低水位阈值
func WithObjectPoolMinThreshold(n int) ObjectPoolOption {
	return func(c *ObjectPoolConfig) { c.MinThreshold = n }
}

// WithObjectPoolBatchSize 设置每次补充批量上限
func WithObjectPoolBatchSize(n int) ObjectPoolOption {
	return func(c *ObjectPoolConfig) { c.BatchSize = n }
}

// WithObjectPoolColdStartCount 设置冷启动同步预生成数量（0 表示纯异步）
func WithObjectPoolColdStartCount(n int) ObjectPoolOption {
	return func(c *ObjectPoolConfig) { c.ColdStartCount = n }
}

// WithObjectPoolRefreshInterval 设置补充检查间隔
func WithObjectPoolRefreshInterval(d time.Duration) ObjectPoolOption {
	return func(c *ObjectPoolConfig) { c.RefreshInterval = d }
}

// WithObjectPoolLogger 设置日志器（未设置时回退到 globalLogger）
func WithObjectPoolLogger(l logger.ILogger) ObjectPoolOption {
	return func(c *ObjectPoolConfig) { c.Logger = l }
}

// ============================================================
// ObjectPoolManager
// ============================================================

// ObjectPoolManager 泛型对象池管理器
//
// 包装一个 buffered channel 作为对象缓冲区，通过 factory 预生成对象
// 注册到全局表后由宿主统一启停；注册即启动，业务侧通常无需手动调 Start
type ObjectPoolManager[T any] struct {
	name        string
	config      ObjectPoolConfig
	factory     PoolFactory[T]
	pool        chan T
	periodicMgr *syncx.PeriodicTaskManager

	generated atomic.Int64 // 累计生成计数（观测用）
	served    atomic.Int64 // 累计命中计数（观测用）
	started   atomic.Bool  // 启动标记（幂等保护）
	stopped   atomic.Bool  // 停止标记（通知预填充退出）
	stopOnce  sync.Once
	logger    logger.ILogger
}

// 全局对象池注册表
var (
	objectPoolRegistry   = make(map[string]ObjectPool)
	objectPoolRegistryMu sync.RWMutex
)

// resolveLogger 解析日志器：优先用显式传入的，其次全局，最后默认（确保永不为 nil）
func resolveLogger(l logger.ILogger) logger.ILogger {
	if l != nil {
		return l
	}
	if globalLogger != nil {
		return globalLogger
	}
	return NewDefaultCachexLogger()
}

// RegisterObjectPool 注册对象池并立即启动（pbmo 风格）
//
// 使用者只需在初始化阶段调用一次，后续可通过 GetObjectPool[T](name) 获取实例
// 建议直接保存返回的 *ObjectPoolManager[T] 引用，避免每次查注册表
// name 必须全局唯一，重复注册将 panic
//
//	pool := cachex.RegisterObjectPool[TempKeyPem]("temp_key_pool", factory,
//	    cachex.WithObjectPoolCapacity(64))
func RegisterObjectPool[T any](name string, factory PoolFactory[T], opts ...ObjectPoolOption) *ObjectPoolManager[T] {
	if factory == nil {
		panic(fmt.Sprintf("cachex: object pool %q factory is nil", name))
	}

	objectPoolRegistryMu.Lock()
	defer objectPoolRegistryMu.Unlock()
	if _, exists := objectPoolRegistry[name]; exists {
		panic(fmt.Sprintf("cachex: object pool %q already registered", name))
	}

	// 默认配置
	cfg := ObjectPoolConfig{
		Capacity:        64,
		RefreshInterval: 5 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	// 派生未显式设置的阈值（基于容量）
	if cfg.MinThreshold <= 0 {
		cfg.MinThreshold = cfg.Capacity / 3
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = cfg.Capacity / 2
	}
	if cfg.ColdStartCount < 0 {
		cfg.ColdStartCount = 0
	}
	if cfg.Capacity <= 0 {
		panic(fmt.Sprintf("cachex: object pool %q capacity must be positive", name))
	}
	if cfg.MinThreshold > cfg.Capacity {
		cfg.MinThreshold = cfg.Capacity
	}

	m := &ObjectPoolManager[T]{
		name:        name,
		config:      cfg,
		factory:     factory,
		pool:        make(chan T, cfg.Capacity),
		periodicMgr: syncx.NewPeriodicTaskManager(),
		logger:      resolveLogger(cfg.Logger),
	}
	objectPoolRegistry[name] = m

	// 注册即启动
	_ = m.Start(context.Background())
	return m
}

// GetObjectPool 按名称获取已注册的对象池
func GetObjectPool[T any](name string) (*ObjectPoolManager[T], error) {
	objectPoolRegistryMu.RLock()
	p, ok := objectPoolRegistry[name]
	objectPoolRegistryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("cachex: object pool %q not registered", name)
	}
	typed, ok := p.(*ObjectPoolManager[T])
	if !ok {
		return nil, fmt.Errorf("cachex: object pool %q type mismatch", name)
	}
	return typed, nil
}

// MustGetObjectPool 按名称获取已注册的对象池，未注册时 panic（适合初始化阶段使用）
func MustGetObjectPool[T any](name string) *ObjectPoolManager[T] {
	p, err := GetObjectPool[T](name)
	if err != nil {
		panic(err)
	}
	return p
}

// StartAllObjectPools 启动所有已注册的对象池（通常无需调用，注册时已自动启动）
// 仅供未启用自动启动的场景使用
func StartAllObjectPools(ctx context.Context) error {
	objectPoolRegistryMu.RLock()
	defer objectPoolRegistryMu.RUnlock()
	for _, p := range objectPoolRegistry {
		if err := p.Start(ctx); err != nil {
			return fmt.Errorf("start object pool %q failed: %w", p.Name(), err)
		}
	}
	return nil
}

// StopAllObjectPools 停止所有已注册的对象池（服务关闭时调用）
// 收集所有错误，返回第一个错误（其余错误仅记录日志）
func StopAllObjectPools() error {
	objectPoolRegistryMu.RLock()
	defer objectPoolRegistryMu.RUnlock()
	var firstErr error
	for _, p := range objectPoolRegistry {
		if err := p.Stop(); err != nil {
			if globalLogger != nil {
				globalLogger.Errorf("stop object pool %q failed: %v", p.Name(), err)
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ============================================================
// ObjectPoolManager 方法
// ============================================================

// Name 返回池名称（实现 ObjectPool 接口）
func (m *ObjectPoolManager[T]) Name() string { return m.name }

// Start 启动池：同步预生成冷启动批量，再异步补齐并启动周期补充任务
// 重复调用幂等（已启动则直接返回 nil）
func (m *ObjectPoolManager[T]) Start(ctx context.Context) error {
	if !m.started.CompareAndSwap(false, true) {
		return nil
	}

	// 冷启动同步预生成，保证启动后即有对象可用
	m.fill(m.config.ColdStartCount)

	// 异步补齐到目标容量（不阻塞调用方）
	syncx.Go(ctx).OnPanic(func(r interface{}) {
		m.logger.Errorf("object pool %q async prefill panic: %v", m.name, r)
	}).Exec(func() {
		m.fill(m.config.Capacity)
	})

	// 注册周期补充任务（防重叠执行）
	refillTask := syncx.NewPeriodicTask(
		"object_pool_refill_"+m.name,
		m.config.RefreshInterval,
		m.refillTask,
	).SetPreventOverlap(true).SetOnError(func(name string, err error) {
		m.logger.Errorf("object pool %q refill task error: %v", m.name, err)
	})
	m.periodicMgr.AddTask(refillTask)

	if err := m.periodicMgr.StartWithContext(ctx); err != nil {
		m.logger.Errorf("object pool %q periodic manager start failed: %v", m.name, err)
		return err
	}
	m.logger.Infof("object pool %q started, capacity=%d coldStart=%d minThreshold=%d",
		m.name, m.config.Capacity, m.config.ColdStartCount, m.config.MinThreshold)
	return nil
}

// Stop 停止周期补充任务（实现 ObjectPool 接口）
// 正在进行的预填充会在下一个生成循环检测到停止标记后退出
func (m *ObjectPoolManager[T]) Stop() error {
	m.stopOnce.Do(func() {
		m.stopped.Store(true)
		_ = m.periodicMgr.Stop()
		m.logger.Infof("object pool %q stopped, generated=%d served=%d",
			m.name, m.generated.Load(), m.served.Load())
	})
	return nil
}

// TryGet 非阻塞地从池中取一个对象
// 池空时返回 (零值, false)，由调用方回退实时构造
func (m *ObjectPoolManager[T]) TryGet() (T, bool) {
	var zero T
	select {
	case v := <-m.pool:
		m.served.Add(1)
		return v, true
	default:
		return zero, false
	}
}

// Len 当前池中可用对象数量（观测用，可能随后变化）
func (m *ObjectPoolManager[T]) Len() int {
	return len(m.pool)
}

// PoolStats 池运行统计
type PoolStats struct {
	Name      string // 池名称
	Available int    // 当前可用数量
	Capacity  int    // 池容量
	Generated int64  // 累计生成数量
	Served    int64  // 累计命中数量
}

// Stats 返回池运行统计（观测用）
func (m *ObjectPoolManager[T]) Stats() PoolStats {
	return PoolStats{
		Name:      m.name,
		Available: len(m.pool),
		Capacity:  m.config.Capacity,
		Generated: m.generated.Load(),
		Served:    m.served.Load(),
	}
}

// ============================================================
// 内部方法
// ============================================================

// fill 生成至多 n 个对象放入池
// 池满、已停止或达到容量时立即返回，避免无效 CPU 消耗
func (m *ObjectPoolManager[T]) fill(n int) {
	for i := 0; i < n; i++ {
		// 二次检查：已停止或池已满则退出
		if m.stopped.Load() {
			return
		}
		if len(m.pool) >= m.config.Capacity {
			return
		}
		v, err := m.factory()
		if err != nil {
			m.logger.Errorf("object pool %q factory error: %v", m.name, err)
			continue
		}
		m.generated.Add(1)
		select {
		case m.pool <- v:
		default:
			// 池满（并发竞争兜底），丢弃剩余对象
			return
		}
	}
}

// refillTask 周期补充任务：池中可用对象低于阈值时补齐到目标容量
func (m *ObjectPoolManager[T]) refillTask(ctx context.Context) error {
	if m.stopped.Load() {
		return nil
	}
	current := len(m.pool)
	if current >= m.config.MinThreshold {
		return nil
	}
	need := m.config.Capacity - current
	if need > m.config.BatchSize {
		need = m.config.BatchSize
	}
	m.fill(need)
	return nil
}
