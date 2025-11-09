/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 22:45:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 22:45:00
 * @FilePath: \go-cachex\examples\lru_optimized\basic.go
 * @Description: LRU优化版本客户端基础使用示例
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
)

func basicUsageExample() {
	fmt.Println("=== LRU优化版本缓存客户端示例 ===")
	fmt.Println()

	// 创建上下文
	ctx := context.Background()

	// 创建优化版本的LRU客户端
	client, err := cachex.NewLRUOptimizedClient(ctx, 100)
	if err != nil {
		log.Fatalf("创建LRU优化客户端失败: %v", err)
	}
	defer client.Close()

	fmt.Printf("✅ 成功创建LRU优化客户端 (容量: 100)\n\n")

	// 1. 基本的Set/Get操作
	fmt.Println("1. 基本操作演示:")
	
	key1 := []byte("user:1001")
	value1 := []byte(`{"id":1001,"name":"Alice","email":"alice@example.com"}`)
	
	err = client.Set(ctx, key1, value1)
	if err != nil {
		log.Printf("设置缓存失败: %v", err)
		return
	}
	
	result, err := client.Get(ctx, key1)
	if err != nil {
		log.Printf("获取缓存失败: %v", err)
		return
	}
	
	fmt.Printf("   设置: %s\n", key1)
	fmt.Printf("   获取: %s\n", result)
	fmt.Println()

	// 2. 带TTL的操作
	fmt.Println("2. TTL操作演示:")
	
	key2 := []byte("session:abc123")
	value2 := []byte(`{"user_id":1001,"expires":"2025-11-10"}`)
	
	err = client.SetWithTTL(ctx, key2, value2, 3*time.Second)
	if err != nil {
		log.Printf("设置带TTL缓存失败: %v", err)
		return
	}
	
	// 立即获取
	result, err = client.Get(ctx, key2)
	if err != nil {
		log.Printf("获取缓存失败: %v", err)
		return
	}
	fmt.Printf("   立即获取: %s\n", result)
	
	// 检查TTL
	ttl, err := client.GetTTL(ctx, key2)
	if err != nil {
		log.Printf("获取TTL失败: %v", err)
		return
	}
	fmt.Printf("   剩余TTL: %v\n", ttl)
	
	// 等待过期
	fmt.Print("   等待过期...")
	time.Sleep(4 * time.Second)
	
	_, err = client.Get(ctx, key2)
	if err != nil {
		fmt.Printf(" ✓ 缓存已过期\n")
	}
	fmt.Println()

	// 3. GetOrCompute 演示（支持单飞模式）
	fmt.Println("3. GetOrCompute演示:")
	
	key3 := []byte("compute:data")
	
	// 模拟耗时的计算函数
	loader := func(ctx context.Context) ([]byte, error) {
		fmt.Print("   执行耗时计算...")
		time.Sleep(500 * time.Millisecond)
		return []byte("计算结果数据"), nil
	}
	
	// 第一次调用，会执行loader
	start := time.Now()
	result, err = client.GetOrCompute(ctx, key3, 10*time.Second, loader)
	duration1 := time.Since(start)
	
	if err != nil {
		log.Printf("GetOrCompute失败: %v", err)
		return
	}
	fmt.Printf(" 完成 (耗时: %v)\n", duration1)
	fmt.Printf("   结果: %s\n", result)
	
	// 第二次调用，直接从缓存获取
	start = time.Now()
	result, err = client.GetOrCompute(ctx, key3, 10*time.Second, loader)
	duration2 := time.Since(start)
	
	if err != nil {
		log.Printf("GetOrCompute失败: %v", err)
		return
	}
	fmt.Printf("   从缓存获取 (耗时: %v)\n", duration2)
	fmt.Printf("   结果: %s\n", result)
	fmt.Printf("   性能提升: %.0fx\n", float64(duration1)/float64(duration2))
	fmt.Println()

	// 4. 批量操作演示
	fmt.Println("4. 批量操作演示:")
	
	// 批量设置
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("batch:%d", i))
		value := []byte(fmt.Sprintf("批量数据_%d", i))
		err = client.Set(ctx, key, value)
		if err != nil {
			log.Printf("批量设置失败: %v", err)
			return
		}
	}
	fmt.Printf("   设置了10个批量键值对\n")
	
	// 批量获取（演示性能）
	start = time.Now()
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("batch:%d", i))
		_, err = client.Get(ctx, key)
		if err != nil {
			log.Printf("批量获取失败: %v", err)
			return
		}
	}
	batchDuration := time.Since(start)
	fmt.Printf("   获取10个键值对耗时: %v\n", batchDuration)
	fmt.Println()

	// 5. 删除操作演示
	fmt.Println("5. 删除操作演示:")
	
	key4 := []byte("to_delete")
	value4 := []byte("将要被删除的数据")
	
	err = client.Set(ctx, key4, value4)
	if err != nil {
		log.Printf("设置缓存失败: %v", err)
		return
	}
	
	// 验证存在
	_, err = client.Get(ctx, key4)
	if err != nil {
		log.Printf("意外错误，缓存应该存在: %v", err)
		return
	}
	fmt.Printf("   设置并确认存在: %s\n", key4)
	
	// 删除
	err = client.Del(ctx, key4)
	if err != nil {
		log.Printf("删除缓存失败: %v", err)
		return
	}
	
	// 验证删除
	_, err = client.Get(ctx, key4)
	if err != nil {
		fmt.Printf("   ✓ 删除成功，键不存在\n")
	}
	fmt.Println()

	// 6. 配置信息展示
	fmt.Println("6. 客户端配置:")
	config := client.GetConfig()
	fmt.Printf("   缓存类型: %s\n", config.Type)
	fmt.Printf("   容量: %d\n", config.Capacity)
	fmt.Println()

	fmt.Println("=== 示例演示完成 ===")
}