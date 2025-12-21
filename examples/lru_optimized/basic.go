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

	// 方式1: 直接使用Handler - 简化版API（推荐）
	cache := cachex.NewLRUOptimizedHandler(100)
	defer cache.Close()

	fmt.Printf("✅ 成功创建LRU优化Handler (容量: 100)\n")
	fmt.Printf("支持双API: 简化版 + WithCtx版本\n\n")

	// 1. 基本的Set/Get操作 - 简化版API
	fmt.Println("1. 基本操作演示（简化版API）:")

	key1 := []byte("user:1001")
	value1 := []byte(`{"id":1001,"name":"Alice","email":"alice@example.com"}`)

	err := cache.Set(key1, value1)
	if err != nil {
		log.Printf("设置缓存失败: %v", err)
		return
	}

	result, err := cache.Get(key1)
	if err != nil {
		log.Printf("获取缓存失败: %v", err)
		return
	}

	fmt.Printf("   设置: %s\n", key1)
	fmt.Printf("   获取: %s\n", result)

	// WithCtx版本 - 支持超时控制
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result2, err := cache.GetWithCtx(ctxTimeout, key1)
	if err == nil {
		fmt.Printf("   WithCtx获取: %s\n", result2)
	}
	fmt.Println()

	// 2. 带TTL的操作
	fmt.Println("2. TTL操作演示（简化版API）:")

	key2 := []byte("session:abc123")
	value2 := []byte(`{"user_id":1001,"expires":"2025-11-10"}`)

	err = cache.SetWithTTL(key2, value2, 3*time.Second)
	if err != nil {
		log.Printf("设置带TTL缓存失败: %v", err)
		return
	}

	// 立即获取
	result, err = cache.Get(key2)
	if err != nil {
		log.Printf("获取缓存失败: %v", err)
		return
	}
	fmt.Printf("   立即获取: %s\n", result)

	// 检查TTL
	ttl, err := cache.GetTTL(key2)
	if err != nil {
		log.Printf("获取TTL失败: %v", err)
		return
	}
	fmt.Printf("   剩余TTL: %v\n", ttl)

	// 等待过期
	fmt.Print("   等待过期...")
	time.Sleep(4 * time.Second)

	_, err = cache.Get(key2)
	if err != nil {
		fmt.Printf(" ✓ 缓存已过期\n")
	}
	fmt.Println()

	// 3. GetOrCompute 演示 - 两种API方式
	fmt.Println("3. GetOrCompute演示:")

	key3 := []byte("compute:data")

	// 简化版loader - 不带context
	simpleLoader := func() ([]byte, error) {
		fmt.Print("   执行耗时计算（简化版）...")
		time.Sleep(500 * time.Millisecond)
		return []byte("计算结果数据"), nil
	}

	// 第一次调用，会执行loader
	start := time.Now()
	result, err = cache.GetOrCompute(key3, 10*time.Second, simpleLoader)
	duration1 := time.Since(start)

	if err != nil {
		log.Printf("GetOrCompute失败: %v", err)
		return
	}
	fmt.Printf(" 完成 (耗时: %v)\n", duration1)
	fmt.Printf("   结果: %s\n", result)

	// 第二次调用，直接从缓存获取
	start = time.Now()
	result, err = cache.GetOrCompute(key3, 10*time.Second, simpleLoader)
	duration2 := time.Since(start)

	if err != nil {
		log.Printf("GetOrCompute失败: %v", err)
		return
	}
	fmt.Printf("   从缓存获取 (耗时: %v)\n", duration2)
	fmt.Printf("   结果: %s\n", result)
	fmt.Printf("   性能提升: %.0fx\n", float64(duration1)/float64(duration2))

	// 演示WithCtx版本 - 支持超时控制
	ctxCompute, cancelCompute := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelCompute()
	ctxLoader := func(ctx context.Context) ([]byte, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return []byte("WithCtx结果"), nil
		}
	}
	result3, _ := cache.GetOrComputeWithCtx(ctxCompute, []byte("compute:ctx"), 10*time.Second, ctxLoader)
	if result3 != nil {
		fmt.Printf("   WithCtx版本: %s\n", result3)
	}
	fmt.Println()

	// 4. 批量操作演示
	fmt.Println("4. 批量操作演示（简化版API）:")

	// 批量设置
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("batch:%d", i))
		value := []byte(fmt.Sprintf("批量数据_%d", i))
		err = cache.Set(key, value)
		if err != nil {
			log.Printf("批量设置失败: %v", err)
			return
		}
	}
	fmt.Printf("   设置了10个批量键值对\n")

	// 使用BatchGet - 更高效
	keys := make([][]byte, 10)
	for i := 0; i < 10; i++ {
		keys[i] = []byte(fmt.Sprintf("batch:%d", i))
	}
	start = time.Now()
	results, errs := cache.BatchGet(keys)
	batchDuration := time.Since(start)
	fmt.Printf("   BatchGet获取10个键值对耗时: %v\n", batchDuration)
	successCount := 0
	for i := range results {
		if errs[i] == nil {
			successCount++
		}
	}
	fmt.Printf("   成功获取: %d/%d\n", successCount, len(keys))
	fmt.Println()

	// 5. 删除操作演示
	fmt.Println("5. 删除操作演示:")

	key4 := []byte("to_delete")
	value4 := []byte("将要被删除的数据")

	err = cache.Set(key4, value4)
	if err != nil {
		log.Printf("设置缓存失败: %v", err)
		return
	}

	// 验证存在
	_, err = cache.Get(key4)
	if err != nil {
		log.Printf("意外错误，缓存应该存在: %v", err)
		return
	}
	fmt.Printf("   设置并确认存在: %s\n", key4)

	// 删除
	err = cache.Del(key4)
	if err != nil {
		log.Printf("删除缓存失败: %v", err)
		return
	}

	// 验证删除
	_, err = cache.Get(key4)
	if err != nil {
		fmt.Printf("   ✓ 删除成功，键不存在\n")
	}
	fmt.Println()

	// 6. 统计信息展示
	fmt.Println("6. Handler统计信息:")
	stats := cache.Stats()
	fmt.Printf("   缓存类型: %v\n", stats["cache_type"])
	if entries, ok := stats["entries"]; ok {
		fmt.Printf("   当前条目: %v\n", entries)
	}
	if shardCount, ok := stats["shard_count"]; ok {
		fmt.Printf("   分片数: %v\n", shardCount)
	}
	if hitRate, ok := stats["hit_rate"]; ok {
		fmt.Printf("   命中率: %.2f%%\n", hitRate.(float64)*100)
	}
	fmt.Println()

	fmt.Println("=== 示例演示完成 ===")
}
