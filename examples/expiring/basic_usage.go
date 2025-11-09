/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 21:25:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 21:25:00
 * @FilePath: \go-cachex\examples\expiring\basic_usage.go
 * @Description: Expiring Cache Basic Usage Example
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
	fmt.Println("=== Expiring Cache Basic Usage Example ===")

	// 创建自动过期缓存，每100ms清理过期项目
	fmt.Println("\n1. Creating Expiring Cache:")
	cache := cachex.NewExpiringHandler(100 * time.Millisecond)
	defer cache.Close()
	fmt.Println("Created expiring cache with 100ms cleanup interval")

	// 基本的 Set/Get 操作
	fmt.Println("\n2. Basic Set/Get Operations:")
	
	// 设置键值对（无过期时间）
	if err := cache.Set([]byte("permanent:user:1"), []byte("Alice")); err != nil {
		log.Fatal(err)
	}
	
	if err := cache.Set([]byte("permanent:user:2"), []byte("Bob")); err != nil {
		log.Fatal(err)
	}

	// 获取值
	if value, err := cache.Get([]byte("permanent:user:1")); err == nil {
		fmt.Printf("permanent:user:1 = %s\n", string(value))
	}

	if value, err := cache.Get([]byte("permanent:user:2")); err == nil {
		fmt.Printf("permanent:user:2 = %s\n", string(value))
	}

	// 带 TTL 的操作
	fmt.Println("\n3. TTL Operations:")
	
	// 设置带 TTL 的键值对
	sessionData := []byte(`{"session_id": "abc123", "user_id": 1001, "expires_at": "2025-12-31T23:59:59Z"}`)
	if err := cache.SetWithTTL([]byte("session:abc123"), sessionData, 2*time.Second); err != nil {
		log.Fatal(err)
	}

	// 设置另一个会话，TTL 更短
	tempSession := []byte(`{"session_id": "xyz789", "user_id": 1002, "temp": true}`)
	if err := cache.SetWithTTL([]byte("session:xyz789"), tempSession, 500*time.Millisecond); err != nil {
		log.Fatal(err)
	}

	// 检查 TTL
	if ttl, err := cache.GetTTL([]byte("session:abc123")); err == nil {
		fmt.Printf("session:abc123 TTL: %v\n", ttl)
	}
	if ttl, err := cache.GetTTL([]byte("session:xyz789")); err == nil {
		fmt.Printf("session:xyz789 TTL: %v\n", ttl)
	}

	// 获取值（在过期之前）
	if value, err := cache.Get([]byte("session:abc123")); err == nil {
		fmt.Printf("session:abc123 = %s (active)\n", string(value))
	}
	if value, err := cache.Get([]byte("session:xyz789")); err == nil {
		fmt.Printf("session:xyz789 = %s (active)\n", string(value))
	}

	// 等待第一个会话过期
	fmt.Println("\nWaiting 1 second...")
	time.Sleep(1 * time.Second)

	// 检查第一个会话状态
	if _, err := cache.Get([]byte("session:xyz789")); err != nil {
		fmt.Printf("session:xyz789 has expired: %v\n", err)
	}
	// 第二个会话应该仍然有效
	if value, err := cache.Get([]byte("session:abc123")); err == nil {
		fmt.Printf("session:abc123 = %s (still active)\n", string(value))
	}

	// 等待第二个会话也过期
	fmt.Println("\nWaiting another 2 seconds...")
	time.Sleep(2 * time.Second)

	// 检查第二个会话状态
	if _, err := cache.Get([]byte("session:abc123")); err != nil {
		fmt.Printf("session:abc123 has expired: %v\n", err)
	}

	// 永久数据应该仍然存在
	if value, err := cache.Get([]byte("permanent:user:1")); err == nil {
		fmt.Printf("permanent:user:1 = %s (permanent, still exists)\n", string(value))
	}

	// 更新操作
	fmt.Println("\n4. Update Operations:")
	
	// 更新现有键的值
	if err := cache.Set([]byte("permanent:user:1"), []byte("Alice Smith")); err != nil {
		log.Fatal(err)
	}

	// 验证更新
	if value, err := cache.Get([]byte("permanent:user:1")); err == nil {
		fmt.Printf("Updated permanent:user:1 = %s\n", string(value))
	}

	// 删除操作
	fmt.Println("\n5. Delete Operations:")
	
	// 删除一个存在的键
	if err := cache.Del([]byte("permanent:user:2")); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Deleted permanent:user:2")

	// 验证键已删除
	if _, err := cache.Get([]byte("permanent:user:2")); err != nil {
		fmt.Printf("permanent:user:2 has been deleted: %v\n", err)
	}

	// 批量 TTL 操作演示
	fmt.Println("\n6. Batch TTL Operations Demo:")
	
	// 批量设置带不同TTL的临时数据
	tempData := map[string]time.Duration{
		"temp:1sec":  1 * time.Second,
		"temp:2sec":  2 * time.Second,
		"temp:3sec":  3 * time.Second,
		"temp:5sec":  5 * time.Second,
	}

	fmt.Println("Setting temporary data with different TTLs...")
	for key, ttl := range tempData {
		value := fmt.Sprintf("data-for-%s", key)
		if err := cache.SetWithTTL([]byte(key), []byte(value), ttl); err != nil {
			log.Printf("Failed to set %s: %v\n", key, err)
		} else {
			fmt.Printf("Set %s with TTL %v\n", key, ttl)
		}
	}

	// 观察过期过程
	for i := 1; i <= 6; i++ {
		fmt.Printf("\nAfter %d second(s):\n", i)
		for key := range tempData {
			if value, err := cache.Get([]byte(key)); err == nil {
				fmt.Printf("  %s = %s\n", key, string(value))
			} else {
				fmt.Printf("  %s = EXPIRED\n", key)
			}
		}
		time.Sleep(1 * time.Second)
	}

	// 自动清理演示
	fmt.Println("\n7. Auto-cleanup Verification:")
	fmt.Println("The cache automatically cleans expired keys every 100ms")
	fmt.Println("This helps maintain memory efficiency without manual intervention")

	fmt.Println("\n=== Example completed successfully ===")
}