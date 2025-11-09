/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 21:15:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 21:15:00
 * @FilePath: \go-cachex\examples\ristretto\basic_usage.go
 * @Description: Ristretto Cache Basic Usage Example
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
	fmt.Println("=== Ristretto Cache Basic Usage Example ===")

	// 创建默认的 Ristretto 缓存
	fmt.Println("\n1. Creating Default Ristretto Cache:")
	cache, err := cachex.NewDefaultRistrettoHandler()
	if err != nil {
		log.Fatal("Failed to create Ristretto cache:", err)
	}
	defer cache.Close()

	// 基本的 Set/Get 操作
	fmt.Println("\n2. Basic Set/Get Operations:")
	
	// 设置键值对
	if err := cache.Set([]byte("product:1"), []byte(`{"id": 1, "name": "Laptop", "price": 999.99}`)); err != nil {
		log.Fatal(err)
	}
	
	if err := cache.Set([]byte("product:2"), []byte(`{"id": 2, "name": "Mouse", "price": 29.99}`)); err != nil {
		log.Fatal(err)
	}

	// 获取值
	if value, err := cache.Get([]byte("product:1")); err == nil {
		fmt.Printf("product:1 = %s\n", string(value))
	}

	if value, err := cache.Get([]byte("product:2")); err == nil {
		fmt.Printf("product:2 = %s\n", string(value))
	}

	// 带 TTL 的操作
	fmt.Println("\n3. TTL Operations:")
	
	// 设置带 TTL 的键值对
	sessionData := []byte(`{"user_id": 123, "expires": "2025-12-31T23:59:59Z"}`)
	if err := cache.SetWithTTL([]byte("session:abc123"), sessionData, 3*time.Second); err != nil {
		log.Fatal(err)
	}

	// 检查 TTL
	if ttl, err := cache.GetTTL([]byte("session:abc123")); err == nil {
		fmt.Printf("session:abc123 TTL: %v\n", ttl)
	}

	// 获取值（在过期之前）
	if value, err := cache.Get([]byte("session:abc123")); err == nil {
		fmt.Printf("session:abc123 = %s (before expiry)\n", string(value))
	}

	// 等待过期
	fmt.Println("Waiting for session expiry...")
	time.Sleep(4 * time.Second)

	// 尝试获取过期的值
	if _, err := cache.Get([]byte("session:abc123")); err != nil {
		fmt.Printf("session:abc123 has expired: %v\n", err)
	}

	// 更新操作
	fmt.Println("\n4. Update Operations:")
	
	// 更新现有键的值
	updatedProduct := []byte(`{"id": 1, "name": "Gaming Laptop", "price": 1299.99}`)
	if err := cache.Set([]byte("product:1"), updatedProduct); err != nil {
		log.Fatal(err)
	}

	// 验证更新
	if value, err := cache.Get([]byte("product:1")); err == nil {
		fmt.Printf("Updated product:1 = %s\n", string(value))
	}

	// 删除操作
	fmt.Println("\n5. Delete Operations:")
	
	// 删除一个存在的键
	if err := cache.Del([]byte("product:2")); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Deleted product:2")

	// 验证键已删除
	if _, err := cache.Get([]byte("product:2")); err != nil {
		fmt.Printf("product:2 has been deleted: %v\n", err)
	}

	// 大量数据测试
	fmt.Println("\n6. Bulk Operations:")
	
	// 批量设置数据
	fmt.Println("Setting 1000 items...")
	start := time.Now()
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("bulk:item:%d", i))
		value := []byte(fmt.Sprintf(`{"id": %d, "data": "item-%d"}`, i, i))
		if err := cache.Set(key, value); err != nil {
			fmt.Printf("Failed to set item %d: %v\n", i, err)
		}
	}
	fmt.Printf("Bulk set completed in %v\n", time.Since(start))

	// 随机检查一些项目
	fmt.Println("Verifying some random items...")
	checkItems := []int{10, 100, 500, 999}
	for _, i := range checkItems {
		key := []byte(fmt.Sprintf("bulk:item:%d", i))
		if value, err := cache.Get(key); err == nil {
			fmt.Printf("  bulk:item:%d = %s\n", i, string(value))
		} else {
			fmt.Printf("  Failed to get bulk:item:%d: %v\n", i, err)
		}
	}

	fmt.Println("\n=== Example completed successfully ===")
}