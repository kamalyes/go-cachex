/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 21:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 21:00:00
 * @FilePath: \go-cachex\examples\lru\basic_usage.go
 * @Description: LRU Cache Basic Usage Example
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */

package main

import (
	"fmt"
	"log"
	"time"

	"github.com/kamalyes/go-cachex"
)

func basicUsageExample() {
	fmt.Println("=== LRU Cache Basic Usage Example ===")

	// 创建容量为 3 的 LRU 缓存
	cache := cachex.NewLRUHandler(3)
	defer cache.Close()

	// 基本的 Set/Get 操作
	fmt.Println("\n1. Basic Set/Get Operations:")
	
	// 设置键值对
	if err := cache.Set([]byte("user:1"), []byte("Alice")); err != nil {
		log.Fatal(err)
	}
	
	if err := cache.Set([]byte("user:2"), []byte("Bob")); err != nil {
		log.Fatal(err)
	}

	// 获取值
	if value, err := cache.Get([]byte("user:1")); err == nil {
		fmt.Printf("user:1 = %s\n", string(value))
	}

	if value, err := cache.Get([]byte("user:2")); err == nil {
		fmt.Printf("user:2 = %s\n", string(value))
	}

	// 测试 LRU 驱逐机制
	fmt.Println("\n2. LRU Eviction Example:")
	
	// 添加第三个项目
	cache.Set([]byte("user:3"), []byte("Charlie"))
	fmt.Println("Added user:3 = Charlie")

	// 访问 user:1，使其变为最近使用
	cache.Get([]byte("user:1"))
	fmt.Println("Accessed user:1 (making it most recently used)")

	// 添加第四个项目，这应该驱逐 user:2（最少最近使用的）
	cache.Set([]byte("user:4"), []byte("David"))
	fmt.Println("Added user:4 = David (should evict user:2)")

	// 验证 user:2 被驱逐
	if _, err := cache.Get([]byte("user:2")); err != nil {
		fmt.Printf("user:2 has been evicted: %v\n", err)
	}

	// 验证其他用户仍然存在
	for _, userID := range []string{"user:1", "user:3", "user:4"} {
		if value, err := cache.Get([]byte(userID)); err == nil {
			fmt.Printf("%s = %s (still in cache)\n", userID, string(value))
		}
	}

	// 带 TTL 的操作
	fmt.Println("\n3. TTL Operations:")
	
	// 设置带 TTL 的键值对
	if err := cache.SetWithTTL([]byte("temp:session"), []byte("temp_data"), 2*time.Second); err != nil {
		log.Fatal(err)
	}

	// 检查 TTL
	if ttl, err := cache.GetTTL([]byte("temp:session")); err == nil {
		fmt.Printf("temp:session TTL: %v\n", ttl)
	}

	// 获取值（在过期之前）
	if value, err := cache.Get([]byte("temp:session")); err == nil {
		fmt.Printf("temp:session = %s (before expiry)\n", string(value))
	}

	// 等待过期
	fmt.Println("Waiting for expiry...")
	time.Sleep(3 * time.Second)

	// 尝试获取过期的值
	if _, err := cache.Get([]byte("temp:session")); err != nil {
		fmt.Printf("temp:session has expired: %v\n", err)
	}

	// 删除操作
	fmt.Println("\n4. Delete Operations:")
	
	// 删除一个存在的键
	if err := cache.Del([]byte("user:1")); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Deleted user:1")

	// 验证键已删除
	if _, err := cache.Get([]byte("user:1")); err != nil {
		fmt.Printf("user:1 has been deleted: %v\n", err)
	}

	// 删除不存在的键（应该成功）
	if err := cache.Del([]byte("non-existent")); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Deleted non-existent key (no error)")

	fmt.Println("\n=== Example completed successfully ===")
}