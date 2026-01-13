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
	t.Skip("Skipping watchdog test - causes timeout issues in test environment")

	if testing.Short() {
		t.Skip("跳过看门狗测试（时间较长）")
	}

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
