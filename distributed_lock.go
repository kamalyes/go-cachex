/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-19 00:00:00
 * @FilePath: \go-cachex\distributed_lock.go
 * @Description: 分布式锁实现，提供可靠的分布式锁机制
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/mathx"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
	"github.com/redis/go-redis/v9"
)

var (
	ErrLockNotFound    = errors.New("lock not found")
	ErrLockNotObtained = errors.New("lock not obtained")
	ErrLockExpired     = errors.New("lock has expired")
	ErrLockNotOwned    = errors.New("lock not owned by this instance")
)

// LockConfig 锁配置
type LockConfig struct {
	TTL              time.Duration  // 锁的TTL
	RetryInterval    time.Duration  // 重试间隔
	MaxRetries       int            // 最大重试次数
	Namespace        string         // 命名空间
	EnableWatchdog   bool           // 是否启用看门狗自动续期
	WatchdogInterval time.Duration  // 看门狗检查间隔
	Logger           logger.ILogger // 日志记录器
}

// DistributedLock 分布式锁
type DistributedLock struct {
	client     *redis.Client
	config     LockConfig
	key        string         // 锁的键名
	token      string         // 锁的令牌（用于确保只有持锁者能释放锁）
	acquired   bool           // 是否已获取锁
	expireTime time.Time      // 锁的过期时间
	stopChan   chan struct{}  // 停止看门狗的通道
	mu         sync.Mutex     // 保护锁状态的互斥锁
	logger     logger.ILogger // 日志记录器
}

// WithLogger 设置日志记录器
func (c *DistributedLock) WithLogger(logger logger.ILogger) *DistributedLock {
	c.logger = logger
	return c
}

// NewDistributedLock 创建分布式锁
func NewDistributedLock(client *redis.Client, key string, config LockConfig) *DistributedLock {
	config.TTL = mathx.IfNotZero(config.TTL, time.Minute*5)
	config.RetryInterval = mathx.IfNotZero(config.RetryInterval, time.Millisecond*100)
	config.MaxRetries = mathx.IfNotZero(config.MaxRetries, 10)
	config.Namespace = mathx.IfNotEmpty(config.Namespace, "lock")
	config.WatchdogInterval = mathx.IfNotZero(config.WatchdogInterval, config.TTL/3)
	config.Logger = mathx.IfEmpty(config.Logger, NewDefaultCachexLogger())

	lockKey := fmt.Sprintf("%s:%s", config.Namespace, key)

	return &DistributedLock{
		client: client,
		config: config,
		key:    lockKey,
		logger: config.Logger,
		// token将在TryLock时生成
		stopChan: nil, // 将在获取锁时创建
	}
}

// generateToken 生成唯一令牌
func generateToken() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// TryLock 尝试获取锁（非阻塞）
func (l *DistributedLock) TryLock(ctx context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.acquired {
		return true, nil // 已经持有锁
	}

	// 重新生成token确保每次获取锁都有唯一标识
	l.token = generateToken()

	// 使用SET命令的NX和EX选项原子性地设置锁
	result, err := l.client.SetNX(ctx, l.key, l.token, l.config.TTL).Result()
	if err != nil {
		l.logger.Errorf("failed to acquire lock for key %s: %v", l.key, err)
		return false, fmt.Errorf("failed to acquire lock: %w", err)
	}

	if result {
		l.acquired = true
		l.expireTime = time.Now().Add(l.config.TTL)
		l.logger.Debugf("lock acquired for key %s, token: %s, ttl: %v", l.key, l.token, l.config.TTL)

		// 启动看门狗
		if l.config.EnableWatchdog {
			// 重新创建停止通道确保看门狗能正常工作
			l.stopChan = make(chan struct{})
			syncx.Go(ctx).
				OnPanic(func(r interface{}) {
					if l.logger != nil {
						l.logger.Errorf("Panic in watchdog: %v", r)
					}
				}).
				Exec(func() { l.watchdog(ctx) })
		}

		return true, nil
	}

	return false, nil
}

// Lock 获取锁（阻塞）
func (l *DistributedLock) Lock(ctx context.Context) error {
	ticker := time.NewTicker(l.config.RetryInterval)
	defer ticker.Stop()

	for {
		acquired, err := l.TryLock(ctx)
		if err != nil {
			return err
		}

		if acquired {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// 继续重试，只在context超时或取消时退出
		}
	}
}

// LockWithTimeout 带超时的获取锁
func (l *DistributedLock) LockWithTimeout(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return l.Lock(ctx)
}

// Unlock 释放锁
func (l *DistributedLock) Unlock(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.acquired {
		return ErrLockNotFound
	}

	// 停止看门狗
	if l.config.EnableWatchdog && l.stopChan != nil {
		select {
		case l.stopChan <- struct{}{}:
		default:
		}
		// 关闭通道避免goroutine泄漏
		close(l.stopChan)
		l.stopChan = nil
	}

	// 使用Lua脚本确保只有持锁者能释放锁
	script := `
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("DEL", KEYS[1])
		else
			return 0
		end
	`

	result, err := l.client.Eval(ctx, script, []string{l.key}, l.token).Result()
	if err != nil {
		return fmt.Errorf("failed to release lock: %w", err)
	}

	// 检查是否成功释放
	if result.(int64) == 1 {
		l.acquired = false
		l.logger.Debugf("lock released for key %s", l.key)
		return nil
	}

	l.logger.Warnf("failed to release lock for key %s: not owned by this instance", l.key)
	return ErrLockNotOwned
}

// Extend 延长锁的TTL
func (l *DistributedLock) Extend(ctx context.Context, ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.acquired {
		return ErrLockNotFound
	}

	// 检查token是否为空
	if l.token == "" {
		return ErrLockNotOwned
	}

	// 使用Lua脚本确保只有持锁者能延长TTL
	script := `
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("EXPIRE", KEYS[1], ARGV[2])
		else
			return 0
		end
	`

	result, err := l.client.Eval(ctx, script, []string{l.key}, l.token, int64(ttl.Seconds())).Result()
	if err != nil {
		return fmt.Errorf("failed to extend lock: %w", err)
	}

	if result.(int64) == 1 {
		l.expireTime = time.Now().Add(ttl)
		l.logger.Debugf("lock extended for key %s, new ttl: %v", l.key, ttl)
		return nil
	}

	// 延长失败，可能锁已经丢失
	l.acquired = false
	l.logger.Warnf("failed to extend lock for key %s: lock may have been lost", l.key)
	return ErrLockNotOwned
}

// IsLocked 检查锁是否仍然有效
func (l *DistributedLock) IsLocked(ctx context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.acquired {
		return false, nil
	}

	// 检查Redis中的锁
	value, err := l.client.Get(ctx, l.key).Result()
	if err != nil {
		if err == redis.Nil {
			l.acquired = false
			return false, nil
		}
		return false, err
	}

	// 检查令牌是否匹配
	if value != l.token {
		l.acquired = false
		return false, nil
	}

	return true, nil
}

// TTL 获取锁的剩余TTL
func (l *DistributedLock) TTL(ctx context.Context) (time.Duration, error) {
	ttl, err := l.client.TTL(ctx, l.key).Result()
	if err != nil {
		return 0, err
	}

	if ttl == -2 {
		return 0, ErrLockNotFound
	}

	return ttl, nil
}

// watchdog 看门狗，自动续期锁
func (l *DistributedLock) watchdog(ctx context.Context) {
	ticker := time.NewTicker(l.config.WatchdogInterval)
	defer ticker.Stop()

	// 创建专用的context避免被外部取消
	watchdogCtx := context.Background()

	for {
		select {
		case <-ticker.C:
			// 检查锁是否仍然有效（使用锁保护）
			l.mu.Lock()
			if !l.acquired {
				l.mu.Unlock()
				return // 锁已释放，退出看门狗
			}
			l.mu.Unlock()

			// 检查Redis中的锁状态
			locked, err := l.IsLocked(watchdogCtx)
			if err != nil {
				// 发生错误时继续尝试，不立即退出
				l.logger.Warnf("watchdog failed to check lock status for key %s: %v", l.key, err)
				continue
			}
			if !locked {
				// 锁已丢失，退出看门狗
				l.logger.Warnf("watchdog detected lock lost for key %s, stopping", l.key)
				return
			}

			// 续期锁
			if err := l.Extend(watchdogCtx, l.config.TTL); err != nil {
				// 续期失败，继续尝试而不是立即退出
				l.logger.Warnf("watchdog failed to extend lock for key %s: %v", l.key, err)
				continue
			}
		case <-l.stopChan:
			return
		case <-ctx.Done():
			return
		}
	}
}

// LockManager 锁管理器
type LockManager struct {
	client *redis.Client
	config LockConfig
	locks  map[string]*DistributedLock
	mu     sync.RWMutex
}

// NewLockManager 创建锁管理器
func NewLockManager(redisClient redis.UniversalClient, config ...LockConfig) *LockManager {
	// 类型断言为*redis.Client
	client, ok := redisClient.(*redis.Client)
	if !ok {
		panic("LockManager requires *redis.Client, cluster mode not supported yet")
	}

	cfg := LockConfig{
		TTL:              time.Minute * 5,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       10,
		Namespace:        "lock",
		EnableWatchdog:   true,
		WatchdogInterval: time.Minute,
	}
	if len(config) > 0 {
		cfg = config[0]
	}

	return &LockManager{
		client: client,
		config: cfg,
		locks:  make(map[string]*DistributedLock),
	}
}

// GetLock 获取或创建锁
func (m *LockManager) GetLock(key string) *DistributedLock {
	m.mu.Lock()
	defer m.mu.Unlock()

	if lock, exists := m.locks[key]; exists {
		return lock
	}

	lock := NewDistributedLock(m.client, key, m.config)
	m.locks[key] = lock
	return lock
}

// ReleaseLock 释放锁并从管理器中移除
func (m *LockManager) ReleaseLock(ctx context.Context, key string) error {
	m.mu.Lock()
	lock, exists := m.locks[key]
	if !exists {
		m.mu.Unlock()
		m.config.Logger.Warnf("attempted to release non-existent lock: %s", key)
		return ErrLockNotFound
	}
	delete(m.locks, key)
	m.mu.Unlock()

	err := lock.Unlock(ctx)
	if err == nil {
		m.config.Logger.Debugf("lock manager released lock: %s", key)
	}
	return err
}

// ReleaseAllLocks 释放所有锁
func (m *LockManager) ReleaseAllLocks(ctx context.Context) error {
	m.mu.Lock()
	locks := make(map[string]*DistributedLock, len(m.locks))
	for k, v := range m.locks {
		locks[k] = v
	}
	// 清空锁映射
	m.locks = make(map[string]*DistributedLock)
	m.mu.Unlock()

	var lastErr error
	releasedCount := 0
	for key, lock := range locks {
		if err := lock.Unlock(ctx); err != nil {
			lastErr = fmt.Errorf("failed to release lock %s: %w", key, err)
			m.config.Logger.Errorf("failed to release lock %s: %v", key, err)
		} else {
			releasedCount++
		}
	}

	m.config.Logger.Infof("released %d/%d locks", releasedCount, len(locks))

	return lastErr
}

// GetLockStats 获取锁统计信息
type LockStats struct {
	Key            string        `json:"key"`
	Token          string        `json:"token"`
	Acquired       bool          `json:"acquired"`
	ExpireTime     time.Time     `json:"expire_time"`
	TTL            time.Duration `json:"ttl"`
	WatchdogActive bool          `json:"watchdog_active"`
}

func (l *DistributedLock) GetStats(ctx context.Context) (*LockStats, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	stats := &LockStats{
		Key:            l.key,
		Token:          l.token,
		Acquired:       l.acquired,
		ExpireTime:     l.expireTime,
		WatchdogActive: l.config.EnableWatchdog,
	}

	if l.acquired {
		ttl, err := l.TTL(ctx)
		if err == nil {
			stats.TTL = ttl
		}
	}

	return stats, nil
}

// GetAllLockStats 获取管理器中所有锁的统计信息
func (m *LockManager) GetAllLockStats(ctx context.Context) (map[string]*LockStats, error) {
	m.mu.RLock()
	locks := make(map[string]*DistributedLock, len(m.locks))
	for k, v := range m.locks {
		locks[k] = v
	}
	m.mu.RUnlock()

	stats := make(map[string]*LockStats)
	for key, lock := range locks {
		stat, err := lock.GetStats(ctx)
		if err == nil {
			stats[key] = stat
		}
	}

	return stats, nil
}

// CleanupExpiredLocks 清理过期的锁
func (m *LockManager) CleanupExpiredLocks(ctx context.Context) error {
	m.mu.Lock()
	keysToDelete := make([]string, 0)

	for key, lock := range m.locks {
		locked, err := lock.IsLocked(ctx)
		if err != nil || !locked {
			keysToDelete = append(keysToDelete, key)
		}
	}

	for _, key := range keysToDelete {
		delete(m.locks, key)
	}
	m.mu.Unlock()

	return nil
}

// LockWithRetry 带重试的锁获取工具函数
func LockWithRetry(ctx context.Context, client *redis.Client, key string, config LockConfig, fn func() error) error {
	lock := NewDistributedLock(client, key, config)

	if err := lock.Lock(ctx); err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}

	defer func() {
		if unlockErr := lock.Unlock(ctx); unlockErr != nil {
			// 记录解锁错误，但不覆盖原始错误
			lock.logger.Warnf("failed to unlock: %v", unlockErr)
		}
	}()

	return fn()
}

// MutexLock 互斥锁工具函数，确保同时只有一个操作在执行
func MutexLock(ctx context.Context, client *redis.Client, key string, ttl time.Duration, fn func() error) error {
	config := LockConfig{
		TTL:           ttl,
		MaxRetries:    30,
		RetryInterval: time.Millisecond * 100,
		Namespace:     "mutex",
	}

	return LockWithRetry(ctx, client, key, config, fn)
}
