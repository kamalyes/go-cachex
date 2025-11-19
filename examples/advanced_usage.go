/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-19 20:53:00
 * @FilePath: \go-cachex\examples\advanced_usage.go
 * @Description: go-cachex 高级功能使用示例
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/kamalyes/go-cachex"
	"github.com/redis/go-redis/v9"
)

// User 用户结构体示例
type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func main() {
	// 创建Redis客户端
	client := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})

	ctx := context.Background()

	// 测试连接
	if err := client.Ping(ctx).Err(); err != nil {
		log.Fatalf("Redis连接失败: %v", err)
	}

	fmt.Println("=== Go-Cachex 高级功能示例 ===")

	// 演示高级缓存功能
	demonstrateAdvancedCache(ctx, client)

	// 演示队列功能
	demonstrateQueue(ctx, client)

	// 演示热key功能
	demonstrateHotKey(ctx, client)

	// 演示分布式锁功能
	demonstrateDistributedLock(ctx, client)

	fmt.Println("\n=== 所有示例执行完成 ===")
}

// 演示高级缓存功能
func demonstrateAdvancedCache(ctx context.Context, client *redis.Client) {
	fmt.Println("1. 高级缓存功能演示")
	fmt.Println("-------------------")

	// 创建高级缓存
	config := cachex.AdvancedCacheConfig{
		Compression:        cachex.CompressionGzip,
		MinSizeForCompress: 100,
		DefaultTTL:         time.Minute * 5,
		Namespace:          "demo",
		EnableMetrics:      true,
	}

	cache := cachex.NewAdvancedCache[User](client, config)

	// 设置用户数据
	user := User{
		ID:    1,
		Name:  "张三",
		Email: "zhangsan@example.com",
	}

	err := cache.Set(ctx, "user:1", user)
	if err != nil {
		log.Printf("设置缓存失败: %v", err)
		return
	}
	fmt.Println("✓ 设置用户缓存成功")

	// 获取用户数据
	if fetchedUser, exists, err := cache.Get(ctx, "user:1"); err != nil {
		log.Printf("获取缓存失败: %v", err)
	} else if exists {
		fmt.Printf("✓ 获取用户缓存成功: %+v\n", fetchedUser)
	} else {
		fmt.Println("✗ 用户缓存不存在")
	}

	// GetOrSet 示例
	user2, err := cache.GetOrSet(ctx, "user:2", func() (User, error) {
		fmt.Println("  → 缓存未命中，从数据库加载用户")
		return User{ID: 2, Name: "李四", Email: "lisi@example.com"}, nil
	})
	if err != nil {
		log.Printf("GetOrSet失败: %v", err)
	} else {
		fmt.Printf("✓ GetOrSet成功: %+v\n", user2)
	}

	// 批量操作示例
	users := map[string]User{
		"user:3": {ID: 3, Name: "王五", Email: "wangwu@example.com"},
		"user:4": {ID: 4, Name: "赵六", Email: "zhaoliu@example.com"},
	}

	if err := cache.BatchSet(ctx, users); err != nil {
		log.Printf("批量设置失败: %v", err)
	} else {
		fmt.Println("✓ 批量设置用户成功")
	}

	// 批量获取
	if batchUsers, err := cache.BatchGet(ctx, []string{"user:3", "user:4", "user:5"}); err != nil {
		log.Printf("批量获取失败: %v", err)
	} else {
		fmt.Printf("✓ 批量获取成功，获得 %d 个用户\n", len(batchUsers))
	}

	// 显示缓存指标
	if metrics := cache.GetMetrics(); metrics != nil {
		fmt.Printf("✓ 缓存指标 - 命中: %d, 未命中: %d, 设置: %d\n",
			metrics.Hits, metrics.Misses, metrics.Sets)
	}

	fmt.Println()
}

// 演示队列功能
func demonstrateQueue(ctx context.Context, client *redis.Client) {
	fmt.Println("2. 队列功能演示")
	fmt.Println("---------------")

	// 创建队列处理器
	config := cachex.QueueConfig{
		MaxRetries:      3,
		RetryDelay:      time.Second * 2,
		BatchSize:       5,
		LockTimeout:     time.Minute,
		CleanupInterval: time.Minute * 5,
	}

	queue := cachex.NewQueueHandler(client, "demo", config)

	// FIFO队列示例
	fmt.Println("FIFO队列:")

	// 入队
	for i := 1; i <= 3; i++ {
		item := &cachex.QueueItem{
			Data: fmt.Sprintf("任务 %d", i),
		}
		if err := queue.Enqueue(ctx, "tasks", cachex.QueueTypeFIFO, item); err != nil {
			log.Printf("入队失败: %v", err)
		} else {
			fmt.Printf("  ✓ 任务 %d 已入队\n", i)
		}
	}

	// 出队
	for i := 0; i < 3; i++ {
		if item, err := queue.Dequeue(ctx, "tasks", cachex.QueueTypeFIFO); err != nil {
			log.Printf("出队失败: %v", err)
		} else if item != nil {
			fmt.Printf("  ✓ 出队: %s\n", item.Data)
		}
	}

	// 优先级队列示例
	fmt.Println("\n优先级队列:")

	// 入队不同优先级的任务
	priorities := []struct {
		data     string
		priority float64
	}{
		{"低优先级任务", 1.0},
		{"高优先级任务", 10.0},
		{"中优先级任务", 5.0},
	}

	for _, p := range priorities {
		item := &cachex.QueueItem{
			Data:     p.data,
			Priority: p.priority,
		}
		if err := queue.Enqueue(ctx, "priority_tasks", cachex.QueueTypePriority, item); err != nil {
			log.Printf("优先级队列入队失败: %v", err)
		} else {
			fmt.Printf("  ✓ %s (优先级: %.1f) 已入队\n", p.data, p.priority)
		}
	}

	// 出队（应该按优先级顺序）
	for i := 0; i < 3; i++ {
		if item, err := queue.Dequeue(ctx, "priority_tasks", cachex.QueueTypePriority); err != nil {
			log.Printf("优先级队列出队失败: %v", err)
		} else if item != nil {
			fmt.Printf("  ✓ 出队: %s (优先级: %.1f)\n", item.Data, item.Priority)
		}
	}

	// 队列统计
	if stats, err := queue.GetStats(ctx, "tasks", cachex.QueueTypeFIFO); err != nil {
		log.Printf("获取队列统计失败: %v", err)
	} else {
		fmt.Printf("✓ 队列统计 - 长度: %d, 失败任务: %d\n", stats.Length, stats.FailedCount)
	}

	fmt.Println()
}

// 演示热key功能
func demonstrateHotKey(ctx context.Context, client *redis.Client) {
	fmt.Println("3. 热Key功能演示")
	fmt.Println("----------------")

	// 创建数据加载器
	loader := &cachex.SQLDataLoader[int, string]{
		QueryFunc: func(ctx context.Context) (map[int]string, error) {
			// 模拟从数据库加载数据
			fmt.Println("  → 从数据库加载用户名映射...")
			return map[int]string{
				1: "张三",
				2: "李四",
				3: "王五",
			}, nil
		},
	}

	// 创建热key缓存
	config := cachex.HotKeyConfig{
		DefaultTTL:        time.Minute * 10,
		RefreshInterval:   time.Minute * 2,
		EnableAutoRefresh: false, // 示例中禁用自动刷新
		Namespace:         "demo",
	}

	hotkey := cachex.NewHotKeyCache[int, string](client, "user_names", loader, config)

	// 获取单个值
	if name, exists, err := hotkey.Get(ctx, 1); err != nil {
		log.Printf("获取热key失败: %v", err)
	} else if exists {
		fmt.Printf("✓ 用户 1 的名字: %s\n", name)
	}

	// 获取所有值
	if allNames, err := hotkey.GetAll(ctx); err != nil {
		log.Printf("获取所有热key失败: %v", err)
	} else {
		fmt.Printf("✓ 所有用户名: %+v\n", allNames)
	}

	// 更新单个值
	if err := hotkey.Set(ctx, 4, "赵六"); err != nil {
		log.Printf("设置热key失败: %v", err)
	} else {
		fmt.Println("✓ 新增用户 4: 赵六")
	}

	// 获取统计信息
	if stats, err := hotkey.GetStats(ctx); err != nil {
		log.Printf("获取热key统计失败: %v", err)
	} else {
		fmt.Printf("✓ 热key统计 - 缓存大小: %d, TTL: %d秒\n",
			stats.LocalCacheSize, stats.TTL)
	}

	fmt.Println()
}

// 演示分布式锁功能
func demonstrateDistributedLock(ctx context.Context, client *redis.Client) {
	fmt.Println("4. 分布式锁功能演示")
	fmt.Println("------------------")

	// 创建分布式锁
	config := cachex.LockConfig{
		TTL:              time.Minute,
		RetryInterval:    time.Millisecond * 100,
		MaxRetries:       5,
		Namespace:        "demo",
		EnableWatchdog:   false, // 示例中禁用看门狗
		WatchdogInterval: time.Second * 30,
	}

	lock := cachex.NewDistributedLock(client, "critical_section", config)

	// 尝试获取锁
	if acquired, err := lock.TryLock(ctx); err != nil {
		log.Printf("尝试获取锁失败: %v", err)
	} else if acquired {
		fmt.Println("✓ 成功获取分布式锁")

		// 模拟临界区操作
		fmt.Println("  → 执行临界区操作...")
		time.Sleep(time.Second)

		// 释放锁
		if err := lock.Unlock(ctx); err != nil {
			log.Printf("释放锁失败: %v", err)
		} else {
			fmt.Println("✓ 成功释放分布式锁")
		}
	} else {
		fmt.Println("✗ 锁已被其他进程持有")
	}

	// 使用锁管理器
	lockMgr := cachex.NewLockManager(client, config)
	
	// 使用工具函数执行互斥操作
	err := cachex.MutexLock(ctx, client, "resource_lock", time.Minute, func() error {
		fmt.Println("✓ 在分布式锁保护下执行操作")
		time.Sleep(time.Millisecond * 500)
		return nil
	})

	if err != nil {
		log.Printf("互斥锁操作失败: %v", err)
	} else {
		fmt.Println("✓ 互斥锁操作完成")
	}

	// 获取锁统计
	testLock := lockMgr.GetLock("test_lock")
	if stats, err := testLock.GetStats(ctx); err != nil {
		log.Printf("获取锁统计失败: %v", err)
	} else {
		fmt.Printf("✓ 锁统计 - 键: %s, 已获取: %t\n",
			stats.Key, stats.Acquired)
	}

	fmt.Println()
}