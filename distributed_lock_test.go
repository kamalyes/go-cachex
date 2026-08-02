/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-19 20:51:36
 * @FilePath: \go-cachex\distributed_lock_test.go
 * @Description: 分布式锁测试
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

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDistributedLock_TryLock(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       5,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	lock1 := NewDistributedLock(client, "test_lock", config)
	lock2 := NewDistributedLock(client, "test_lock", config)

	// 第一个锁应该能成功获取
	acquired, err := lock1.TryLock(ctx)
	assert.NoError(t, err)
	assert.True(t, acquired, "第一个锁应该能获取成功")

	// 第二个锁应该获取失败（同一个key）
	acquired, err = lock2.TryLock(ctx)
	assert.NoError(t, err)
	assert.False(t, acquired, "第二个锁应该获取失败")

	// 释放第一个锁
	err = lock1.Unlock(ctx)
	assert.NoError(t, err)

	// 现在第二个锁应该能获取成功
	acquired, err = lock2.TryLock(ctx)
	assert.NoError(t, err)
	assert.True(t, acquired, "释放后第二个锁应该能获取成功")

	// 清理
	lock2.Unlock(ctx)
}

func TestDistributedLock_Lock(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Second * 10,
		RetryInterval:    time.Millisecond * 50,
		MaxRetries:       10,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	lock1 := NewDistributedLock(client, "blocking_lock", config)
	lock2 := NewDistributedLock(client, "blocking_lock", config)

	// 启动第一个锁
	err := lock1.Lock(ctx)
	assert.NoError(t, err, "第一个锁应该能获取成功")

	// 在另一个goroutine中测试阻塞锁
	done := make(chan error, 1)
	go func() {
		// 这应该会阻塞直到第一个锁释放，但我们设置了很短的超时
		timeoutCtx, cancel := context.WithTimeout(ctx, time.Millisecond*200)
		defer cancel()

		err := lock2.Lock(timeoutCtx)
		done <- err
	}()

	// 等待goroutine完成或超时
	select {
	case err := <-done:
		// 由于超时，应该返回错误
		assert.Error(t, err, "阻塞锁在超时后应该返回错误")
		if err != nil {
			assert.Contains(t, err.Error(), "context deadline exceeded", "应该是超时错误")
		}
	case <-time.After(time.Second):
		t.Fatal("测试超时")
	}

	// 释放第一个锁
	err = lock1.Unlock(ctx)
	assert.NoError(t, err)

	// 现在lock2应该能获取锁
	err = lock2.Lock(ctx)
	assert.NoError(t, err, "第一个锁释放后，第二个锁应该能获取成功")

	// 清理
	lock2.Unlock(ctx)
}

func TestDistributedLock_LockWithTimeout(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       100,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	lock1 := NewDistributedLock(client, "timeout_lock", config)
	lock2 := NewDistributedLock(client, "timeout_lock", config)

	// 第一个锁获取成功
	err := lock1.Lock(ctx)
	assert.NoError(t, err)

	// 第二个锁使用短超时应该失败
	err = lock2.LockWithTimeout(ctx, time.Millisecond*200)
	assert.Error(t, err, "短超时应该失败")
	assert.Contains(t, err.Error(), "context deadline exceeded", "应该是超时错误")

	// 清理
	lock1.Unlock(ctx)
}

func TestDistributedLock_Extend(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Second * 2, // 短TTL便于测试
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       5,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	lock := NewDistributedLock(client, "extend_lock", config)

	// 获取锁
	err := lock.Lock(ctx)
	assert.NoError(t, err)

	// 获取初始TTL
	initialTTL, err := lock.TTL(ctx)
	assert.NoError(t, err)
	assert.Greater(t, initialTTL, time.Duration(0), "初始TTL应该大于0")

	// 延长TTL
	newTTL := time.Minute * 5
	err = lock.Extend(ctx, newTTL)
	assert.NoError(t, err)

	// 验证TTL已延长
	extendedTTL, err := lock.TTL(ctx)
	assert.NoError(t, err)
	assert.Greater(t, extendedTTL, initialTTL, "延长后的TTL应该更大")

	// 清理
	lock.Unlock(ctx)
}

func TestDistributedLock_IsLocked(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       5,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	lock := NewDistributedLock(client, "status_lock", config)

	// 未获取锁时应该返回false
	locked, err := lock.IsLocked(ctx)
	assert.NoError(t, err)
	assert.False(t, locked, "未获取锁时应该返回false")

	// 获取锁
	err = lock.Lock(ctx)
	assert.NoError(t, err)

	// 现在应该返回true
	locked, err = lock.IsLocked(ctx)
	assert.NoError(t, err)
	assert.True(t, locked, "获取锁后应该返回true")

	// 释放锁
	err = lock.Unlock(ctx)
	assert.NoError(t, err)

	// 释放后应该返回false
	locked, err = lock.IsLocked(ctx)
	assert.NoError(t, err)
	assert.False(t, locked, "释放锁后应该返回false")
}

func TestDistributedLock_TTL(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Second * 10,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       5,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	lock := NewDistributedLock(client, "ttl_lock", config)

	// 获取锁
	err := lock.Lock(ctx)
	assert.NoError(t, err)

	// 获取TTL
	ttl, err := lock.TTL(ctx)
	assert.NoError(t, err)
	assert.Greater(t, ttl, time.Second*5, "TTL应该接近设置的值")
	assert.LessOrEqual(t, ttl, time.Second*10, "TTL不应该超过设置的值")

	// 清理
	lock.Unlock(ctx)

	// 释放后获取TTL应该返回错误
	_, err = lock.TTL(ctx)
	assert.Error(t, err, "释放后获取TTL应该失败")
	assert.Equal(t, ErrLockNotFound, err)
}

func TestDistributedLock_Watchdog(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Millisecond * 500, // 更短的TTL用于测试
		RetryInterval:    time.Millisecond * 50,
		MaxRetries:       3,
		Namespace:        "test",
		EnableWatchdog:   true,
		WatchdogInterval: time.Millisecond * 200, // 200ms续期一次
	}

	lock := NewDistributedLock(client, "watchdog_lock", config)

	// 获取锁
	err := lock.Lock(ctx)
	assert.NoError(t, err)

	// 等待超过原始TTL但少于续期间隔的时间
	time.Sleep(time.Millisecond * 800)

	// 锁应该仍然有效（被看门狗续期）
	locked, err := lock.IsLocked(ctx)
	assert.NoError(t, err)
	assert.True(t, locked, "看门狗应该自动续期锁")

	// 释放锁
	err = lock.Unlock(ctx)
	assert.NoError(t, err)
}

func TestLockManager(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       5,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	manager := NewLockManager(client, config)

	// 获取锁
	lock1 := manager.GetLock("resource1")
	lock2 := manager.GetLock("resource2")

	// 获取同一个资源的锁应该返回相同的实例
	lock1_again := manager.GetLock("resource1")
	assert.Equal(t, lock1, lock1_again, "获取同一资源的锁应该返回相同实例")

	// 获取锁
	err := lock1.Lock(ctx)
	assert.NoError(t, err)
	err = lock2.Lock(ctx)
	assert.NoError(t, err)

	// 获取所有锁的统计信息
	stats, err := manager.GetAllLockStats(ctx)
	assert.NoError(t, err)
	assert.Len(t, stats, 2, "应该有2个锁的统计信息")

	// 释放特定锁
	err = manager.ReleaseLock(ctx, "resource1")
	assert.NoError(t, err)

	// 释放所有锁
	err = manager.ReleaseAllLocks(ctx)
	assert.NoError(t, err)
}

func TestLockWithRetry(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       5,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	counter := 0
	err := LockWithRetry(ctx, client, "test_resource", config, func() error {
		counter++
		time.Sleep(time.Millisecond * 100) // 模拟工作
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, counter, "工作函数应该被执行一次")
}

func TestMutexLock(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()

	// 使用互斥锁保护的计数器
	counter := 0
	var wg sync.WaitGroup

	// 启动多个goroutine并发访问
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			err := MutexLock(ctx, client, "counter_lock", time.Minute, func() error {
				temp := counter
				time.Sleep(time.Millisecond * 10) // 模拟竞态条件
				counter = temp + 1
				return nil
			})
			assert.NoError(t, err, "互斥锁操作应该成功")
		}(i)
	}

	wg.Wait()

	// 如果互斥锁正常工作，计数器应该正好是10
	assert.Equal(t, 10, counter, "互斥锁应该防止竞态条件")
}

func TestDistributedLock_ConcurrentAccess(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Second * 30,
		RetryInterval:    time.Millisecond * 50,
		MaxRetries:       20,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	// 共享资源
	counter := 0
	var wg sync.WaitGroup

	// 启动多个goroutine竞争锁
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			lock := NewDistributedLock(client, "concurrent_lock", config)

			err := lock.Lock(ctx)
			if err != nil {
				t.Logf("goroutine %d 获取锁失败: %v", id, err)
				return
			}
			defer lock.Unlock(ctx)

			// 临界区操作
			temp := counter
			time.Sleep(time.Millisecond * 10) // 模拟工作时间
			counter = temp + 1

			t.Logf("goroutine %d 完成，counter = %d", id, counter)
		}(i)
	}

	wg.Wait()

	// 所有goroutine都应该能获取到锁并完成操作
	assert.Equal(t, 10, counter, "所有goroutine都应该能获取锁")
}

func TestDistributedLock_Stats(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       5,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	lock := NewDistributedLock(client, "stats_lock", config)

	// 获取未获取锁时的统计
	stats, err := lock.GetStats(ctx)
	assert.NoError(t, err)
	require.NotNil(t, stats)
	assert.False(t, stats.Acquired, "未获取锁时Acquired应该为false")
	assert.Contains(t, stats.Key, "stats_lock", "键名应该包含锁名")

	// 获取锁后的统计
	err = lock.Lock(ctx)
	assert.NoError(t, err)

	stats, err = lock.GetStats(ctx)
	assert.NoError(t, err)
	assert.True(t, stats.Acquired, "获取锁后Acquired应该为true")
	assert.Greater(t, stats.TTL, time.Duration(0), "TTL应该大于0")
	assert.NotEmpty(t, stats.Token, "令牌不应该为空")

	// 清理
	lock.Unlock(ctx)
}

// 基准测试
func BenchmarkDistributedLock_TryLock(b *testing.B) {
	client := setupRedisClient(&testing.T{})
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       5,
		Namespace:        "bench",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			lock := NewDistributedLock(client, fmt.Sprintf("bench_lock_%d", i%100), config)
			lock.TryLock(ctx)
			lock.Unlock(ctx)
			i++
		}
	})
}

func BenchmarkDistributedLock_LockUnlock(b *testing.B) {
	client := setupRedisClient(&testing.T{})
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       5,
		Namespace:        "bench",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lock := NewDistributedLock(client, fmt.Sprintf("bench_sequential_%d", i), config)
		lock.Lock(ctx)
		lock.Unlock(ctx)
	}
}

// ============================================================================
// 分布式锁状态管理回归测试
//
// 背景: 原 Unlock() 在所有权丢失（ErrLockNotOwned）时未清理本地 acquired/token/expireTime，
// 导致下次 TryLock() 因 acquired=true 直接返回成功，但实际并未持有 Redis 锁
// 修复后所有所有权丢失路径都通过 resetState() 清理完整本地状态
// ============================================================================

// TestDistributedLock_UnlockNotOwnedClearsState 回归：Unlock 所有权丢失后本地状态被清理
// 原 bug：Unlock 返回 ErrLockNotOwned 但 acquired 仍为 true，下次 TryLock 误判成功
func TestDistributedLock_UnlockNotOwnedClearsState(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       5,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	lockA := NewDistributedLock(client, "ownership_lost", config)
	lockB := NewDistributedLock(client, "ownership_lost", config)

	// lockA 获取锁
	acquired, err := lockA.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// lockB 强制覆盖 Redis 中的锁（模拟 lockA 的锁过期后被他人获取）
	// 直接用 Redis 命令覆盖，模拟 TTL 过期后他人抢锁
	err = client.Set(ctx, lockB.key, "other_token", time.Minute).Err()
	require.NoError(t, err)

	// lockA 尝试 Unlock，应返回 ErrLockNotOwned 并清理本地状态
	err = lockA.Unlock(ctx)
	assert.ErrorIs(t, err, ErrLockNotOwned)

	// 核心断言：本地状态应被清理
	lockA.mu.Lock()
	localAcquired := lockA.acquired
	localToken := lockA.token
	lockA.mu.Unlock()
	assert.False(t, localAcquired, "Unlock 所有权丢失后 acquired 必须被清理")
	assert.Empty(t, localToken, "Unlock 所有权丢失后 token 必须被清理")

	// lockA 再次 TryLock 应该失败（Redis 中锁被 lockB 持有）
	acquired, err = lockA.TryLock(ctx)
	assert.NoError(t, err)
	assert.False(t, acquired, "本地状态清理后 TryLock 不应误判成功")

	// 清理
	client.Del(ctx, lockB.key)
}

// TestDistributedLock_TryLockVerifiesRedisToken 回归：TryLock 校验 Redis token
// 原 bug：TryLock 看到 acquired=true 直接返回成功，不校验 Redis
func TestDistributedLock_TryLockVerifiesRedisToken(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       5,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	lock := NewDistributedLock(client, "token_verify", config)

	// 获取锁
	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 模拟锁被他人覆盖（TTL 过期后他人抢锁）
	err = client.Set(ctx, lock.key, "different_token", time.Minute).Err()
	require.NoError(t, err)

	// 再次 TryLock：本地 acquired=true 但 Redis token 不匹配
	// 修复后应检测到不一致，清理本地状态，并尝试重新获取（SetNX 会失败因 Redis 已有值）
	acquired, err = lock.TryLock(ctx)
	assert.NoError(t, err)
	assert.False(t, acquired, "Redis token 不匹配时 TryLock 不应返回成功")

	// 清理
	client.Del(ctx, lock.key)
}

// TestDistributedLock_TryLockAfterUnlockWorks 确保正常 Unlock 后 TryLock 可重新获取
func TestDistributedLock_TryLockAfterUnlockWorks(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	lock := NewDistributedLock(client, "relock_after_unlock", config)

	// 获取并释放
	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	err = lock.Unlock(ctx)
	require.NoError(t, err)

	// 再次获取应成功
	acquired, err = lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired, "正常 Unlock 后 TryLock 应能重新获取")

	// 清理
	lock.Unlock(ctx)
}

// TestDistributedLock_IsLockedClearsStateOnMismatch 回归：IsLocked 检测到 token 不匹配时清理状态
func TestDistributedLock_IsLockedClearsStateOnMismatch(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	lock := NewDistributedLock(client, "islocked_mismatch", config)

	// 获取锁
	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 模拟锁被他人覆盖
	err = client.Set(ctx, lock.key, "other_token", time.Minute).Err()
	require.NoError(t, err)

	// IsLocked 应返回 false 并清理本地状态
	locked, err := lock.IsLocked(ctx)
	require.NoError(t, err)
	assert.False(t, locked)

	// 验证本地状态被清理
	lock.mu.Lock()
	localAcquired := lock.acquired
	localToken := lock.token
	lock.mu.Unlock()
	assert.False(t, localAcquired, "IsLocked 检测到不匹配后 acquired 必须被清理")
	assert.Empty(t, localToken, "IsLocked 检测到不匹配后 token 必须被清理")

	// 清理
	client.Del(ctx, lock.key)
}

// TestDistributedLock_ExtendClearsStateOnFailure 回归：Extend 失败时清理完整状态
func TestDistributedLock_ExtendClearsStateOnFailure(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()

	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		Namespace:        "test",
		EnableWatchdog:   false,
		WatchdogInterval: time.Second * 30,
	}

	lock := NewDistributedLock(client, "extend_fail", config)

	// 获取锁
	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 模拟锁被他人覆盖
	err = client.Set(ctx, lock.key, "other_token", time.Minute).Err()
	require.NoError(t, err)

	// Extend 应失败并清理完整本地状态
	err = lock.Extend(ctx, time.Minute)
	assert.ErrorIs(t, err, ErrLockNotOwned)

	// 验证本地状态被清理（原 bug 只清理 acquired，未清理 token）
	lock.mu.Lock()
	localAcquired := lock.acquired
	localToken := lock.token
	localExpireTime := lock.expireTime
	lock.mu.Unlock()
	assert.False(t, localAcquired, "Extend 失败后 acquired 必须被清理")
	assert.Empty(t, localToken, "Extend 失败后 token 必须被清理（原 bug 遗漏）")
	assert.True(t, localExpireTime.IsZero(), "Extend 失败后 expireTime 必须被清理")

	// 清理
	client.Del(ctx, lock.key)
}

// TestDistributedLock_WithLogger 覆盖 WithLogger
func TestDistributedLock_WithLogger(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	config := LockConfig{TTL: time.Minute, Namespace: "test"}
	lock := NewDistributedLock(client, "withlogger", config)
	result := lock.WithLogger(NewDefaultCachexLogger())
	assert.Same(t, lock, result)
}

// TestDistributedLock_TryLockRedisConfirms 覆盖 TryLock 中 Redis 确认仍持有锁的路径
func TestDistributedLock_TryLockRedisConfirms(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	config := LockConfig{TTL: time.Minute, Namespace: "test"}
	lock := NewDistributedLock(client, "redis_confirm", config)

	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 再次 TryLock，Redis 确认仍持有锁 → return true, nil，重入计数 +1
	acquired, err = lock.TryLock(ctx)
	assert.NoError(t, err)
	assert.True(t, acquired, "Redis 确认持有时 TryLock 应返回 true")

	// 第一次 Unlock 仅递减重入计数（reentrantCount 2→1），不释放 Redis 锁
	err = lock.Unlock(ctx)
	assert.NoError(t, err)

	// 第二次 Unlock 才真正释放 Redis 锁
	err = lock.Unlock(ctx)
	assert.NoError(t, err)
}

// TestDistributedLock_UnlockNotAcquired 覆盖 Unlock 未获取锁时返回 ErrLockNotFound
func TestDistributedLock_UnlockNotAcquired(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	lock := NewDistributedLock(client, "unlock_not_acquired", LockConfig{TTL: time.Minute, Namespace: "test"})
	err := lock.Unlock(ctx)
	assert.ErrorIs(t, err, ErrLockNotFound)
}

// TestDistributedLock_UnlockEvalError 覆盖 Unlock Eval 错误路径
func TestDistributedLock_UnlockEvalError(t *testing.T) {
	client := setupRedisClient(t)
	ctx := context.Background()
	config := LockConfig{TTL: time.Minute, Namespace: "test", EnableWatchdog: false}
	lock := NewDistributedLock(client, "unlock_eval_err", config)

	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 关闭 client 导致 Eval 错误
	client.Close()
	err = lock.Unlock(ctx)
	assert.Error(t, err)
}

// TestDistributedLock_ExtendNotAcquired 覆盖 Extend 未获取锁
func TestDistributedLock_ExtendNotAcquired(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	lock := NewDistributedLock(client, "extend_not_acquired", LockConfig{TTL: time.Minute, Namespace: "test"})
	err := lock.Extend(ctx, time.Minute)
	assert.ErrorIs(t, err, ErrLockNotFound)
}

// TestDistributedLock_ExtendEmptyToken 覆盖 Extend token 为空
func TestDistributedLock_ExtendEmptyToken(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	lock := NewDistributedLock(client, "extend_empty_token", LockConfig{TTL: time.Minute, Namespace: "test"})

	// 手动设置 acquired=true 但 token 为空
	lock.mu.Lock()
	lock.acquired = true
	lock.token = ""
	lock.mu.Unlock()

	err := lock.Extend(ctx, time.Minute)
	assert.ErrorIs(t, err, ErrLockNotOwned)
}

// TestDistributedLock_ExtendEvalError 覆盖 Extend Eval 错误路径
func TestDistributedLock_ExtendEvalError(t *testing.T) {
	client := setupRedisClient(t)
	ctx := context.Background()
	config := LockConfig{TTL: time.Minute, Namespace: "test", EnableWatchdog: false}
	lock := NewDistributedLock(client, "extend_eval_err", config)

	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 关闭 client 导致 Eval 错误
	client.Close()
	err = lock.Extend(ctx, time.Minute)
	assert.Error(t, err)
}

// TestDistributedLock_IsLockedRedisNil 覆盖 IsLocked 中 redis.Nil（键不存在）
func TestDistributedLock_IsLockedRedisNil(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	config := LockConfig{TTL: time.Minute, Namespace: "test", EnableWatchdog: false}
	lock := NewDistributedLock(client, "islocked_nil", config)

	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 删除 Redis 中的键，模拟 TTL 过期
	client.Del(ctx, lock.key)

	locked, err := lock.IsLocked(ctx)
	assert.NoError(t, err)
	assert.False(t, locked)
}

// TestDistributedLock_IsLockedError 覆盖 IsLocked 中非 redis.Nil 错误
func TestDistributedLock_IsLockedError(t *testing.T) {
	client := setupRedisClient(t)
	ctx := context.Background()
	config := LockConfig{TTL: time.Minute, Namespace: "test", EnableWatchdog: false}
	lock := NewDistributedLock(client, "islocked_err", config)

	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 关闭 client 导致 Get 错误
	client.Close()
	locked, err := lock.IsLocked(ctx)
	assert.Error(t, err)
	assert.False(t, locked)
}

// TestDistributedLock_TTLNotFound 覆盖 TTL 中 ttl == -2（键不存在）
func TestDistributedLock_TTLNotFound(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	lock := NewDistributedLock(client, "ttl_not_found", LockConfig{TTL: time.Minute, Namespace: "test"})

	_, err := lock.TTL(ctx)
	assert.ErrorIs(t, err, ErrLockNotFound)
}

// TestDistributedLock_WatchdogStopChanNil 覆盖 watchdog 中 stopChan == nil 直接返回
func TestDistributedLock_WatchdogStopChanNil(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	lock := NewDistributedLock(client, "wd_nil_stop", LockConfig{TTL: time.Minute, Namespace: "test"})

	// stopChan 默认为 nil，直接调用 watchdog 应立即返回
	lock.watchdog(ctx)
}

// TestDistributedLock_WatchdogLockLost 覆盖 watchdog 检测到锁丢失（!locked）
func TestDistributedLock_WatchdogLockLost(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Millisecond * 500,
		Namespace:        "test",
		EnableWatchdog:   true,
		WatchdogInterval: time.Millisecond * 100,
	}
	lock := NewDistributedLock(client, "wd_lock_lost", config)

	err := lock.Lock(ctx)
	require.NoError(t, err)

	// 删除 Redis 中的键，模拟锁过期
	client.Del(ctx, lock.key)

	// 等待 watchdog 检测到锁丢失并退出
	time.Sleep(time.Millisecond * 300)

	// 锁应已丢失
	lock.mu.Lock()
	isAcq := lock.acquired
	lock.mu.Unlock()
	assert.False(t, isAcq, "watchdog 检测到锁丢失后应清理 acquired")
}

// TestDistributedLock_WatchdogExtendError 覆盖 watchdog 中 Extend 失败
func TestDistributedLock_WatchdogExtendError(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Millisecond * 500,
		Namespace:        "test",
		EnableWatchdog:   true,
		WatchdogInterval: time.Millisecond * 100,
	}
	lock := NewDistributedLock(client, "wd_extend_err", config)

	err := lock.Lock(ctx)
	require.NoError(t, err)

	// 覆盖 Redis 中的 token，使 Extend 失败（token 不匹配）
	client.Set(ctx, lock.key, "different_token", time.Minute)

	// 等待 watchdog 检测并尝试 Extend
	time.Sleep(time.Millisecond * 300)

	// Extend 失败后 acquired 应被清理
	lock.mu.Lock()
	isAcq := lock.acquired
	lock.mu.Unlock()
	assert.False(t, isAcq, "watchdog Extend 失败后应清理 acquired")
}

// TestDistributedLock_WatchdogCtxDone 覆盖 watchdog 中 ctx.Done()
func TestDistributedLock_WatchdogCtxDone(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	config := LockConfig{
		TTL:              time.Minute,
		Namespace:        "test",
		EnableWatchdog:   true,
		WatchdogInterval: time.Millisecond * 100,
	}
	lock := NewDistributedLock(client, "wd_ctx_done", config)

	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 取消 context，watchdog 应通过 ctx.Done() 退出
	cancel()
	time.Sleep(time.Millisecond * 200)
}

// TestDistributedLock_WatchdogIsLockedError 覆盖 watchdog 中 IsLocked 错误
func TestDistributedLock_WatchdogIsLockedError(t *testing.T) {
	client := setupRedisClient(t)
	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Millisecond * 500,
		Namespace:        "test",
		EnableWatchdog:   true,
		WatchdogInterval: time.Millisecond * 100,
	}
	lock := NewDistributedLock(client, "wd_islocked_err", config)

	err := lock.Lock(ctx)
	require.NoError(t, err)

	// 关闭 client 导致 IsLocked 错误
	client.Close()

	// 等待 watchdog 检测到错误
	time.Sleep(time.Millisecond * 300)
}

// TestLockManager_PanicOnNonRedisClient 覆盖 NewLockManager panic
func TestLockManager_PanicOnNonRedisClient(t *testing.T) {
	// 传入 *redis.ClusterClient（非 *redis.Client）应 panic
	clusterClient := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs: []string{"localhost:6379"},
	})
	defer clusterClient.Close()

	assert.Panics(t, func() {
		NewLockManager(clusterClient)
	})
}

// TestLockManager_ReleaseLockNotFound 覆盖 ReleaseLock 中锁不存在
func TestLockManager_ReleaseLockNotFound(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	mgr := NewLockManager(client, LockConfig{TTL: time.Minute, Namespace: "test"})

	err := mgr.ReleaseLock(ctx, "non_existent")
	assert.ErrorIs(t, err, ErrLockNotFound)
}

// TestLockManager_ReleaseAllLocksWithError 覆盖 ReleaseAllLocks 中 Unlock 失败
func TestLockManager_ReleaseAllLocksWithError(t *testing.T) {
	client := setupRedisClient(t)
	ctx := context.Background()
	mgr := NewLockManager(client, LockConfig{
		TTL:            time.Minute,
		Namespace:      "test",
		EnableWatchdog: false,
	})

	lock1 := mgr.GetLock("release_err_1")
	_, err := lock1.TryLock(ctx)
	require.NoError(t, err)

	// 手动删除 Redis 中的键，使 Unlock 时 Lua 脚本返回 0（所有权丢失）
	client.Del(ctx, lock1.key)

	err = mgr.ReleaseAllLocks(ctx)
	assert.Error(t, err, "ReleaseAllLocks 在 Unlock 失败时应返回错误")
}

// TestDistributedLock_GetStats 覆盖 GetStats
func TestDistributedLock_GetStats(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	config := LockConfig{TTL: time.Minute, Namespace: "test", EnableWatchdog: false}
	lock := NewDistributedLock(client, "getstats", config)

	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	stats, err := lock.GetStats(ctx)
	require.NoError(t, err)
	assert.True(t, stats.Acquired)
	assert.NotEmpty(t, stats.Token)
	assert.Equal(t, "test:getstats", stats.Key)

	lock.Unlock(ctx)

	// 未获取锁时的 stats
	stats, err = lock.GetStats(ctx)
	require.NoError(t, err)
	assert.False(t, stats.Acquired)
}

// TestDistributedLock_LockWithRetryFunc 覆盖 LockWithRetry 工具函数
func TestDistributedLock_LockWithRetryFunc(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	config := LockConfig{
		TTL:            time.Minute,
		Namespace:      "test",
		EnableWatchdog: false,
	}

	called := false
	err := LockWithRetry(ctx, client, "retry_test", config, func() error {
		called = true
		return nil
	})
	assert.NoError(t, err)
	assert.True(t, called, "回调函数应被调用")
}

// TestDistributedLock_MutexLockFunc 覆盖 MutexLock 工具函数
func TestDistributedLock_MutexLockFunc(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()

	called := false
	err := MutexLock(ctx, client, "mutex_test", time.Minute, func() error {
		called = true
		return nil
	})
	assert.NoError(t, err)
	assert.True(t, called, "回调函数应被调用")
}

// TestDistributedLock_CleanupExpiredLocks 覆盖 CleanupExpiredLocks
func TestDistributedLock_CleanupExpiredLocks(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	mgr := NewLockManager(client, LockConfig{
		TTL:            time.Millisecond * 100,
		Namespace:      "test",
		EnableWatchdog: false,
	})

	lock1 := mgr.GetLock("expired_1")
	_, err := lock1.TryLock(ctx)
	require.NoError(t, err)

	lock2 := mgr.GetLock("expired_2")
	_, err = lock2.TryLock(ctx)
	require.NoError(t, err)

	// 等待 TTL 过期
	time.Sleep(time.Millisecond * 200)

	err = mgr.CleanupExpiredLocks(ctx)
	assert.NoError(t, err)
}

// TestDistributedLock_WatchdogPanic 覆盖 watchdog 中 OnPanic 回调
// 设置 WatchdogInterval 为负数，导致 time.NewTicker panic，被 OnPanic 捕获
func TestDistributedLock_WatchdogPanic(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		Namespace:        "test",
		EnableWatchdog:   true,
		WatchdogInterval: -1, // 负数导致 time.NewTicker panic
	}
	lock := NewDistributedLock(client, "wd_panic", config)

	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 等待 watchdog 启动并 panic（被 OnPanic 捕获）
	time.Sleep(time.Millisecond * 100)

	// watchdog panic 后 acquired 仍为 true，Unlock 会清理状态
	// 同时 stopWatchdogLocked 发送 stopChan 会走 default 分支（watchdog 已退出）
	err = lock.Unlock(ctx)
	assert.NoError(t, err)
}

// TestDistributedLock_TTLError 覆盖 TTL 方法中 err != nil 分支
// 关闭 client 后调用 TTL，Redis 命令返回错误
func TestDistributedLock_TTLError(t *testing.T) {
	client := setupRedisClient(t)
	ctx := context.Background()
	config := LockConfig{TTL: time.Minute, Namespace: "test", EnableWatchdog: false}
	lock := NewDistributedLock(client, "ttl_error", config)

	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 关闭 client 导致 TTL 命令出错
	client.Close()
	_, err = lock.TTL(ctx)
	assert.Error(t, err)
}

// TestDistributedLock_WatchdogNotAcquired 覆盖 watchdog 中 !acquired 分支
// 获取锁后手动设置 acquired=false（不触发 stopWatchdogLocked），
// watchdog ticker 触发时发现 !acquired，直接返回
func TestDistributedLock_WatchdogNotAcquired(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := LockConfig{
		TTL:              time.Minute,
		Namespace:        "test",
		EnableWatchdog:   true,
		WatchdogInterval: time.Millisecond * 50,
	}
	lock := NewDistributedLock(client, "wd_not_acquired", config)

	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 手动设置 acquired=false，不触发 stopWatchdogLocked
	// watchdog ticker 触发时会发现 !acquired 并返回
	lock.mu.Lock()
	lock.acquired = false
	lock.mu.Unlock()

	// 等待 watchdog ticker 触发（WatchdogInterval=50ms）
	time.Sleep(time.Millisecond * 200)

	// 清理：stopChan 仍非 nil（未调用 stopWatchdogLocked），手动清理
	lock.mu.Lock()
	lock.stopWatchdogLocked()
	lock.mu.Unlock()
}

// TestDistributedLock_WatchdogExtendFail 覆盖 watchdog 中 Extend 失败分支
// 通过 OnBeforeExtend 钩子在 IsLocked 成功后、Extend 调用前修改 Redis token，
// 使 Extend 的 Lua 脚本检测到 token 不匹配，返回0 → ErrLockNotOwned
func TestDistributedLock_WatchdogExtendFail(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := LockConfig{
		TTL:              time.Minute,
		Namespace:        "test",
		EnableWatchdog:   true,
		WatchdogInterval: time.Millisecond * 50,
	}
	lock := NewDistributedLock(client, "wd_extend_fail", config)

	// 设置续期前置钩子：在 IsLocked 成功后、Extend 调用前修改 Redis token
	// 使 Extend 的 Lua 脚本 GET(key) 返回 "wrong_token"，不等于 lock.token，返回0
	lock.OnBeforeExtend(func(_ context.Context) {
		client.Set(ctx, lock.key, "wrong_token", time.Minute)
	})

	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 等待 watchdog ticker 触发：IsLocked 成功 → hook 修改 token → Extend 失败
	time.Sleep(time.Millisecond * 200)

	// Extend 失败后 acquired 应被清理
	lock.mu.Lock()
	isAcq := lock.acquired
	lock.mu.Unlock()
	assert.False(t, isAcq, "watchdog Extend 失败后应清理 acquired")
}

// TestDistributedLock_WatchdogHooks 覆盖 OnBeforeExtend 和 OnAfterExtend 钩子的正常调用路径
func TestDistributedLock_WatchdogHooks(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := LockConfig{
		TTL:              time.Minute,
		Namespace:        "test",
		EnableWatchdog:   true,
		WatchdogInterval: time.Millisecond * 50,
	}
	lock := NewDistributedLock(client, "wd_hooks", config)

	var (
		mu            sync.Mutex
		beforeCalled  int
		afterSuccessN int
		afterErrCount int
	)

	lock.OnBeforeExtend(func(_ context.Context) {
		mu.Lock()
		beforeCalled++
		mu.Unlock()
	})
	lock.OnAfterExtend(func(_ context.Context, err error) {
		mu.Lock()
		if err == nil {
			afterSuccessN++
		} else {
			afterErrCount++
		}
		mu.Unlock()
	})

	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 等待至少一次续期周期
	time.Sleep(time.Millisecond * 200)

	cancel()
	time.Sleep(time.Millisecond * 50)

	mu.Lock()
	assert.GreaterOrEqual(t, beforeCalled, 1, "OnBeforeExtend 应至少被调用一次")
	assert.GreaterOrEqual(t, afterSuccessN, 1, "OnAfterExtend 续期成功应至少被调用一次")
	mu.Unlock()
}

// TestDistributedLock_LockCtxDone 覆盖 Lock 中 select 的 ctx.Done() 分支
// 使用大 RetryInterval + 小 timeout，确保 ctx.Done() 先于 ticker.C 触发
func TestDistributedLock_LockCtxDone(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	config := LockConfig{
		TTL:           time.Minute,
		RetryInterval: time.Second * 10, // 大间隔确保 ticker.C 不会先到
		MaxRetries:    5,
		Namespace:     "test",
	}

	lock1 := NewDistributedLock(client, "ctx_done_lock", config)
	lock2 := NewDistributedLock(client, "ctx_done_lock", config)

	// lock1 先持有锁
	err := lock1.Lock(ctx)
	require.NoError(t, err)
	defer lock1.Unlock(ctx)

	// lock2 用短超时，TryLock 失败后 ctx.Done() 先于 ticker.C
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Millisecond*50)
	defer cancel()
	err = lock2.Lock(timeoutCtx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

// TestLockWithRetry_LockFail 覆盖 LockWithRetry 中 Lock 失败分支
// 用已取消的 ctx 使 Lock 立即失败
func TestLockWithRetry_LockFail(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	config := LockConfig{
		TTL:            time.Minute,
		Namespace:      "test",
		EnableWatchdog: false,
	}

	// 先获取锁，使 LockWithRetry 内部的 Lock 必须重试
	holder := NewDistributedLock(client, "lwr_lock_fail", config)
	_, err := holder.TryLock(ctx)
	require.NoError(t, err)
	defer holder.Unlock(ctx)

	// 用已取消的 ctx 调用 LockWithRetry，Lock 会因 ctx 取消而失败
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err = LockWithRetry(cancelCtx, client, "lwr_lock_fail", config, func() error {
		return nil
	})
	assert.Error(t, err)
}

// TestLockWithRetry_UnlockFail 覆盖 LockWithRetry 中 Unlock 失败分支
// 在回调函数中删除 Redis key，使 defer Unlock 时所有权丢失
func TestLockWithRetry_UnlockFail(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	config := LockConfig{
		TTL:            time.Minute,
		Namespace:      "test",
		EnableWatchdog: false,
	}

	// LockWithRetry 内部获取锁成功后，在 fn 中删除 Redis key
	// defer Unlock 时 Lua 脚本返回 0（所有权丢失），记录警告日志
	err := LockWithRetry(ctx, client, "lwr_unlock_fail", config, func() error {
		// 删除 Redis key，使 Unlock 时所有权丢失
		client.Del(ctx, "test:lwr_unlock_fail")
		return nil
	})
	// LockWithRetry 返回 fn 的结果（nil），Unlock 失败只记录日志
	assert.NoError(t, err)
}

// TestDistributedLock_ReentrantCount 验证重入计数器：
// 多次 TryLock（Redis 确认仍持有）后，前 N-1 次 Unlock 仅递减计数，
// 只有最后一次 Unlock 才真正释放 Redis 锁
func TestDistributedLock_ReentrantCount(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	config := LockConfig{TTL: time.Minute, Namespace: "test", EnableWatchdog: false}
	lock := NewDistributedLock(client, "reentrant_count", config)

	// 第一次获取锁
	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 重入两次
	acquired, err = lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	acquired, err = lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 验证 Redis 锁仍存在
	locked, err := lock.IsLocked(ctx)
	require.NoError(t, err)
	require.True(t, locked, "重入期间锁应仍然持有")

	// 第一次 Unlock：reentrantCount 3→2，锁不应释放
	err = lock.Unlock(ctx)
	assert.NoError(t, err)
	exists, _ := client.Exists(ctx, lock.key).Result()
	assert.Equal(t, int64(1), exists, "重入计数 >0 时不应删除 Redis key")

	// 第二次 Unlock：reentrantCount 2→1，锁不应释放
	err = lock.Unlock(ctx)
	assert.NoError(t, err)
	exists, _ = client.Exists(ctx, lock.key).Result()
	assert.Equal(t, int64(1), exists, "重入计数 >0 时不应删除 Redis key")

	// 第三次 Unlock：reentrantCount 1→0，真正释放 Redis 锁
	err = lock.Unlock(ctx)
	assert.NoError(t, err)
	exists, _ = client.Exists(ctx, lock.key).Result()
	assert.Equal(t, int64(0), exists, "最后一次 Unlock 应删除 Redis key")
}

// TestDistributedLock_ConcurrentReentrantUnlock 验证并发场景下重入计数器：
// 模拟 kronos WithCachexLock 在未强制 overlap-prevent 时多个 goroutine
// 共享同一 DistributedLock 实例，先完成的 goroutine 不会误释放锁
func TestDistributedLock_ConcurrentReentrantUnlock(t *testing.T) {
	client := setupRedisClient(t)
	defer client.Close()
	ctx := context.Background()
	config := LockConfig{
		TTL:              time.Minute,
		Namespace:        "test",
		EnableWatchdog:   true,
		WatchdogInterval: time.Millisecond * 200,
	}
	lock := NewDistributedLock(client, "concurrent_reentrant", config)

	// 第一个 goroutine 获取锁
	acquired, err := lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 第二个 goroutine 重入获取锁（模拟 kronos 新 tick 调用 TryLock）
	acquired, err = lock.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// 第一个 goroutine 完成 Unlock（仅递减计数，不释放锁，看门狗继续运行）
	err = lock.Unlock(ctx)
	assert.NoError(t, err)

	// 锁仍应存在于 Redis
	exists, _ := client.Exists(ctx, lock.key).Result()
	assert.Equal(t, int64(1), exists, "第一个 goroutine Unlock 后锁不应释放")

	// 等待看门狗至少续期一次（验证看门狗未被停止）
	time.Sleep(time.Millisecond * 300)

	// 锁仍应存在且 token 不变
	val, err := client.Get(ctx, lock.key).Result()
	require.NoError(t, err)
	assert.NotEmpty(t, val, "看门狗续期后锁应仍存在")

	// 第二个 goroutine 完成 Unlock（真正释放锁）
	err = lock.Unlock(ctx)
	assert.NoError(t, err)

	// 锁应已从 Redis 删除
	exists, _ = client.Exists(ctx, lock.key).Result()
	assert.Equal(t, int64(0), exists, "最后一个 goroutine Unlock 后锁应释放")
}
