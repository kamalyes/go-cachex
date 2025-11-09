/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 21:20:50
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 21:46:29
 * @FilePath: \go-cachex\examples\sharded\basic_usage.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
/*
 * @Description: Sharded Cache 基础使用示例
 */
package main

import (
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/kamalyes/go-cachex"
)

func basicUsageExample() {
	fmt.Println("=== Sharded Cache 基础使用示例 ==")

	// 示例 1: 基本操作
	demonstrateBasicOperations()

	// 示例 2: 分片分布
	demonstrateShardDistribution()

	// 示例 3: 并发性能对比
	demonstrateConcurrencyComparison()

	// 示例 4: 不同后端缓存
	demonstrateDifferentBackends()
}

// 示例 1: 基本操作
func demonstrateBasicOperations() {
	fmt.Println("1. 基本操作演示")
	fmt.Println("---------------")

	// 创建分片缓存，使用 LRU 作为每个分片的后端
	factory := func() cachex.Handler {
		return cachex.NewLRUHandler(100) // 每个分片容量100
	}
	
	// 创建 8 个分片
	cache := cachex.NewShardedHandler(factory, 8)
	defer cache.Close()

	// 基本的 Set 操作
	err := cache.Set([]byte("user:123"), []byte(`{"name":"Alice","age":30}`))
	if err != nil {
		log.Fatal("设置缓存失败:", err)
	}
	fmt.Println("✓ 设置用户信息")

	// Get 操作
	value, err := cache.Get([]byte("user:123"))
	if err != nil {
		log.Fatal("获取缓存失败:", err)
	}
	fmt.Printf("✓ 获取用户信息: %s\n", string(value))

	// SetWithTTL 操作
	err = cache.SetWithTTL([]byte("session:abc"), []byte("session_data"), 2*time.Second)
	if err != nil {
		log.Fatal("设置TTL缓存失败:", err)
	}
	fmt.Println("✓ 设置会话信息 (TTL: 2秒)")

	// 检查 TTL
	ttl, err := cache.GetTTL([]byte("session:abc"))
	if err != nil {
		fmt.Printf("✗ 获取TTL失败: %v\n", err)
	} else {
		fmt.Printf("✓ 会话TTL: %v\n", ttl)
	}

	// Del 操作
	err = cache.Del([]byte("user:123"))
	if err != nil {
		log.Fatal("删除缓存失败:", err)
	}
	fmt.Println("✓ 删除用户信息")

	_, err = cache.Get([]byte("user:123"))
	if err != nil {
		fmt.Printf("✓ 用户信息已删除: %v\n", err)
	}

	fmt.Println()
}

// 示例 2: 分片分布
func demonstrateShardDistribution() {
	fmt.Println("2. 分片分布演示")
	fmt.Println("----------------")

	// 创建带监控的分片缓存
	shardCounts := make([]int, 4)
	
	factory := func() cachex.Handler {
		return cachex.NewLRUHandler(50)
	}
	cache := cachex.NewShardedHandler(factory, 4)
	defer cache.Close()

	// 存储大量数据来观察分布
	fmt.Println("存储 100 个键值对...")
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key_%d", i))
		value := []byte(fmt.Sprintf("value_%d", i))
		
		err := cache.Set(key, value)
		if err != nil {
			fmt.Printf("设置 key_%d 失败: %v\n", i, err)
			continue
		}

		// 统计分片使用情况（这里简化处理，实际中分片是内部的）
		// 我们通过哈希算法模拟分片选择
		hash := fnv32a(key)
		shardIdx := int(hash % 4)
		shardCounts[shardIdx]++
	}

	// 显示分布统计
	fmt.Println("\n分片使用统计:")
	total := 0
	for i, count := range shardCounts {
		fmt.Printf("  分片 %d: %d 个键 (%.1f%%)\n", i, count, float64(count)*100.0/100.0)
		total += count
	}
	fmt.Printf("  总计: %d 个键\n", total)

	// 验证数据可以正确读取
	fmt.Println("\n验证数据完整性:")
	successCount := 0
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key_%d", i))
		val, err := cache.Get(key)
		if err != nil {
			fmt.Printf("✗ 读取 key_%d 失败: %v\n", i, err)
		} else {
			expected := fmt.Sprintf("value_%d", i)
			if string(val) == expected {
				successCount++
			} else {
				fmt.Printf("✗ key_%d 值不匹配: 期望=%s, 实际=%s\n", i, expected, string(val))
			}
		}
	}
	fmt.Printf("✓ 成功读取: %d/100\n", successCount)

	fmt.Println()
}

// 示例 3: 并发性能对比
func demonstrateConcurrencyComparison() {
	fmt.Println("3. 并发性能对比")
	fmt.Println("----------------")

	// 测试不同配置下的性能
	configs := []struct {
		name   string
		shards int
		capacity int
	}{
		{"单分片", 1, 1000},
		{"4分片", 4, 250},
		{"8分片", 8, 125},
		{"16分片", 16, 62},
	}

	const workers = 10
	const operations = 1000

	for _, config := range configs {
		fmt.Printf("\n测试配置: %s (总容量: %d)\n", config.name, config.shards*config.capacity)
		
		factory := func() cachex.Handler {
			return cachex.NewLRUHandler(config.capacity)
		}
		cache := cachex.NewShardedHandler(factory, config.shards)

		start := time.Now()
		
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for op := 0; op < operations; op++ {
					key := []byte(fmt.Sprintf("worker_%d_key_%d", workerID, op))
					value := []byte(fmt.Sprintf("worker_%d_value_%d", workerID, op))
					
					// 写入
					cache.Set(key, value)
					
					// 读取
					cache.Get(key)
				}
			}(w)
		}
		
		wg.Wait()
		elapsed := time.Since(start)
		totalOps := workers * operations * 2 // 每次包含写入和读取

		fmt.Printf("  并发度: %d 工作线程\n", workers)
		fmt.Printf("  总操作数: %d\n", totalOps)
		fmt.Printf("  耗时: %v\n", elapsed)
		fmt.Printf("  吞吐量: %.0f ops/sec\n", float64(totalOps)/elapsed.Seconds())

		cache.Close()
	}

	fmt.Println()
}

// 示例 4: 不同后端缓存
func demonstrateDifferentBackends() {
	fmt.Println("4. 不同后端缓存演示")
	fmt.Println("--------------------")

	// LRU 后端
	fmt.Println("LRU 后端分片缓存:")
	lruFactory := func() cachex.Handler {
		return cachex.NewLRUHandler(100)
	}
	lruCache := cachex.NewShardedHandler(lruFactory, 4)

	// 添加数据
	for i := 0; i < 50; i++ {
		key := []byte(fmt.Sprintf("lru_key_%d", i))
		value := []byte(fmt.Sprintf("lru_value_%d", i))
		err := lruCache.Set(key, value)
		if err != nil {
			fmt.Printf("  ✗ LRU设置失败: %v\n", err)
		}
	}
	fmt.Println("  ✓ 存储了 50 个键值对")

	// 验证读取
	val, err := lruCache.Get([]byte("lru_key_25"))
	if err != nil {
		fmt.Printf("  ✗ LRU读取失败: %v\n", err)
	} else {
		fmt.Printf("  ✓ 读取成功: %s\n", string(val))
	}
	lruCache.Close()

	// Expiring 后端
	fmt.Println("\nExpiring 后端分片缓存:")
	expiringFactory := func() cachex.Handler {
		return cachex.NewExpiringHandler(50 * time.Millisecond) // 快速清理
	}
	expiringCache := cachex.NewShardedHandler(expiringFactory, 4)

	// 添加带TTL的数据
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("exp_key_%d", i))
		value := []byte(fmt.Sprintf("exp_value_%d", i))
		ttl := time.Duration(i*50+100) * time.Millisecond
		
		err := expiringCache.SetWithTTL(key, value, ttl)
		if err != nil {
			fmt.Printf("  ✗ Expiring设置失败: %v\n", err)
		}
	}
	fmt.Println("  ✓ 存储了 10 个带不同TTL的键值对")

	// 立即读取
	activeCount := 0
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("exp_key_%d", i))
		if _, err := expiringCache.Get(key); err == nil {
			activeCount++
		}
	}
	fmt.Printf("  ✓ 立即读取: %d/10 个键活跃\n", activeCount)

	// 等待部分过期
	time.Sleep(300 * time.Millisecond)
	activeCount = 0
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("exp_key_%d", i))
		if _, err := expiringCache.Get(key); err == nil {
			activeCount++
		}
	}
	fmt.Printf("  ✓ 300ms后: %d/10 个键活跃\n", activeCount)

	expiringCache.Close()

	// 混合后端
	fmt.Println("\n混合后端分片缓存:")
	mixedFactory := func() cachex.Handler {
		// 随机选择后端类型（简化演示）
		return cachex.NewLRUHandler(50)
	}
	mixedCache := cachex.NewShardedHandler(mixedFactory, 8)

	// 使用混合缓存
	for i := 0; i < 20; i++ {
		key := []byte(fmt.Sprintf("mixed_key_%d", i))
		value := []byte(fmt.Sprintf("mixed_value_%d", i))
		
		if i%2 == 0 {
			err := mixedCache.Set(key, value)
			if err != nil {
				fmt.Printf("  ✗ 混合设置失败: %v\n", err)
			}
		} else {
			err := mixedCache.SetWithTTL(key, value, time.Second)
			if err != nil {
				fmt.Printf("  ✗ 混合TTL设置失败: %v\n", err)
			}
		}
	}
	fmt.Println("  ✓ 混合缓存操作完成")

	mixedCache.Close()
	
	fmt.Printf("\nCPU核心数: %d\n", runtime.NumCPU())
	fmt.Println("建议分片数: CPU核心数的2-4倍，以平衡并发性和内存开销")
}

// 简单的 FNV-1a 哈希实现（用于演示）
func fnv32a(data []byte) uint32 {
	hash := uint32(2166136261)
	for _, b := range data {
		hash ^= uint32(b)
		hash *= 16777619
	}
	return hash
}