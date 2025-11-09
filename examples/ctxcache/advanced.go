/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 21:12:18
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 21:45:21
 * @FilePath: \go-cachex\examples\ctxcache\advanced.go
 * @Description:
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
/*
 * @Description: CtxCache 高级使用示例
 */
package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamalyes/go-cachex"
)

func advancedUsageExample() {
	fmt.Println("=== CtxCache 高级使用示例 ===")

	// 示例 1: Singleflight 去重演示
	demonstrateSingleflight()

	// 示例 2: 不同后端缓存的性能对比
	demonstrateBackendComparison()

	// 示例 3: 复杂业务场景模拟
	demonstrateBusinessScenario()

	// 示例 4: 错误处理和恢复
	demonstrateErrorHandling()

	// 示例 5: 监控和调试
	demonstrateMonitoring()
}

// 示例 1: Singleflight 去重演示
func demonstrateSingleflight() {
	fmt.Println("1. Singleflight 去重演示")
	fmt.Println("-----------------------")

	// 创建缓存
	ristrettoHandler, err := cachex.NewDefaultRistrettoHandler()
	if err != nil {
		log.Fatal("创建缓存失败:", err)
	}
	defer ristrettoHandler.Close()

	cache := cachex.NewCtxCache(ristrettoHandler)
	defer cache.Close()

	var computeCount int64
	var totalRequests int64

	// 模拟昂贵的计算
	expensiveCompute := func(ctx context.Context) ([]byte, error) {
		count := atomic.AddInt64(&computeCount, 1)
		fmt.Printf("  🔄 执行昂贵计算 #%d\n", count)
		
		// 模拟耗时计算
		time.Sleep(500 * time.Millisecond)
		
		return []byte(fmt.Sprintf("computed_result_%d", count)), nil
	}

	ctx := context.Background()
	const workers = 10
	var wg sync.WaitGroup

	fmt.Printf("启动 %d 个并发请求，都请求相同的键...\n", workers)
	start := time.Now()

	// 启动多个 goroutine 同时请求相同的键
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			atomic.AddInt64(&totalRequests, 1)
			
			value, err := cache.GetOrCompute(ctx, []byte("expensive_key"), time.Minute, expensiveCompute)
			if err != nil {
				fmt.Printf("Worker %d 失败: %v\n", id, err)
				return
			}
			fmt.Printf("Worker %d 获得结果: %s\n", id, string(value))
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	fmt.Printf("\n结果统计:\n")
	fmt.Printf("  总请求数: %d\n", atomic.LoadInt64(&totalRequests))
	fmt.Printf("  实际计算次数: %d\n", atomic.LoadInt64(&computeCount))
	fmt.Printf("  节省计算: %d 次\n", atomic.LoadInt64(&totalRequests)-atomic.LoadInt64(&computeCount))
	fmt.Printf("  总耗时: %v\n", elapsed)
	fmt.Printf("  Singleflight 效果: %.1f%%\n", 
		100.0*(1.0-float64(atomic.LoadInt64(&computeCount))/float64(atomic.LoadInt64(&totalRequests))))

	fmt.Println()
}

// 示例 2: 不同后端缓存的性能对比
func demonstrateBackendComparison() {
	fmt.Println("2. 不同后端缓存的性能对比")
	fmt.Println("-------------------------")

	backends := []struct {
		name    string
		handler cachex.Handler
	}{
		{"LRU", cachex.NewLRUHandler(1000)},
		{"Expiring", cachex.NewExpiringHandler(100 * time.Millisecond)},
	}

	// 添加 Ristretto
	if rh, err := cachex.NewDefaultRistrettoHandler(); err == nil {
		backends = append(backends, struct {
			name    string
			handler cachex.Handler
		}{"Ristretto", rh})
	}

	ctx := context.Background()
	const operations = 1000
	value := make([]byte, 100) // 100字节的值

	for _, backend := range backends {
		fmt.Printf("\n测试后端: %s\n", backend.name)
		cache := cachex.NewCtxCache(backend.handler)

		// 预填充数据
		for i := 0; i < operations/2; i++ {
			key := []byte(fmt.Sprintf("key_%d", i))
			cache.Set(ctx, key, value)
		}

		// 性能测试
		start := time.Now()
		for i := 0; i < operations; i++ {
			key := []byte(fmt.Sprintf("key_%d", i))
			if i%2 == 0 {
				// 50% 读操作
				cache.Get(ctx, key)
			} else {
				// 50% 写操作
				cache.Set(ctx, key, value)
			}
		}
		elapsed := time.Since(start)

		fmt.Printf("  %d 次混合操作耗时: %v\n", operations, elapsed)
		fmt.Printf("  平均每操作: %v\n", elapsed/time.Duration(operations))
		fmt.Printf("  操作速度: %.0f ops/sec\n", float64(operations)/elapsed.Seconds())

		cache.Close()
	}

	fmt.Println()
}

// 示例 3: 复杂业务场景模拟
func demonstrateBusinessScenario() {
	fmt.Println("3. 复杂业务场景模拟")
	fmt.Println("-------------------")

	// 创建多层缓存
	l1Cache := cachex.NewCtxCache(cachex.NewLRUHandler(100))    // L1: 小容量LRU
	l2Cache := cachex.NewCtxCache(cachex.NewExpiringHandler(50 * time.Millisecond)) // L2: 过期缓存

	defer l1Cache.Close()
	defer l2Cache.Close()

	// 模拟用户服务
	type UserService struct {
		l1Cache *cachex.CtxCache
		l2Cache *cachex.CtxCache
	}

	service := &UserService{
		l1Cache: l1Cache,
		l2Cache: l2Cache,
	}

	// 模拟数据库查询
	var dbQueries int64
	dbLoader := func(ctx context.Context) ([]byte, error) {
		queries := atomic.AddInt64(&dbQueries, 1)
		fmt.Printf("  💾 数据库查询 #%d\n", queries)
		time.Sleep(100 * time.Millisecond) // 模拟DB延迟
		return []byte(fmt.Sprintf(`{"user_id":123,"name":"User_%d","query_time":%d}`, 
			queries, time.Now().Unix())), nil
	}

	// 用户查询方法
	getUser := func(ctx context.Context, userID string) ([]byte, error) {
		key := []byte("user:" + userID)
		
		// 先查 L1 缓存
		if data, err := service.l1Cache.Get(ctx, key); err == nil {
			fmt.Printf("  ✅ L1 缓存命中: %s\n", userID)
			return data, nil
		}

		// 再查 L2 缓存，未命中则查数据库
		data, err := service.l2Cache.GetOrCompute(ctx, key, 200*time.Millisecond, dbLoader)
		if err != nil {
			return nil, err
		}

		// 回写到 L1 缓存
		service.l1Cache.Set(ctx, key, data)
		fmt.Printf("  ✅ L2 缓存命中/计算: %s\n", userID)
		return data, nil
	}

	ctx := context.Background()

	fmt.Println("执行多次用户查询:")
	
	// 第一轮查询
	for i := 0; i < 3; i++ {
		userID := fmt.Sprintf("user_%d", i%2) // 只查询2个用户，会有重复
		fmt.Printf("\n查询用户: %s\n", userID)
		data, err := getUser(ctx, userID)
		if err != nil {
			fmt.Printf("  ❌ 查询失败: %v\n", err)
		} else {
			fmt.Printf("  📄 用户数据: %s\n", string(data))
		}
	}

	// 等待 L2 过期
	fmt.Println("\n等待 L2 缓存过期...")
	time.Sleep(250 * time.Millisecond)

	// 第二轮查询
	fmt.Println("L2 过期后再次查询:")
	for i := 0; i < 2; i++ {
		userID := fmt.Sprintf("user_%d", i)
		fmt.Printf("\n查询用户: %s\n", userID)
		data, err := getUser(ctx, userID)
		if err != nil {
			fmt.Printf("  ❌ 查询失败: %v\n", err)
		} else {
			fmt.Printf("  📄 用户数据: %s\n", string(data))
		}
	}

	fmt.Printf("\n总数据库查询次数: %d\n", atomic.LoadInt64(&dbQueries))
	fmt.Println()
}

// 示例 4: 错误处理和恢复
func demonstrateErrorHandling() {
	fmt.Println("4. 错误处理和恢复")
	fmt.Println("-----------------")

	cache := cachex.NewCtxCache(cachex.NewLRUHandler(100))
	defer cache.Close()

	ctx := context.Background()
	var attempts int64

	// 模拟不稳定的服务
	unstableLoader := func(ctx context.Context) ([]byte, error) {
		attempt := atomic.AddInt64(&attempts, 1)
		fmt.Printf("  🔄 尝试 #%d\n", attempt)
		
		// 前两次失败，第三次成功
		if attempt <= 2 {
			return nil, fmt.Errorf("service_unavailable_attempt_%d", attempt)
		}
		
		return []byte(fmt.Sprintf("success_on_attempt_%d", attempt)), nil
	}

	// 带重试的查询函数
	queryWithRetry := func(key []byte, maxRetries int) ([]byte, error) {
		var lastErr error
		
		for retry := 0; retry < maxRetries; retry++ {
			if retry > 0 {
				fmt.Printf("  ⏰ 重试 %d/%d\n", retry, maxRetries-1)
				time.Sleep(time.Duration(retry*100) * time.Millisecond) // 指数退避
			}

			data, err := cache.GetOrCompute(ctx, key, 0, unstableLoader)
			if err == nil {
				return data, nil
			}
			
			lastErr = err
			fmt.Printf("  ❌ 尝试失败: %v\n", err)
		}
		
		return nil, fmt.Errorf("max_retries_exceeded: %v", lastErr)
	}

	// 测试重试机制
	fmt.Println("测试重试机制:")
	data, err := queryWithRetry([]byte("unstable_service"), 5)
	if err != nil {
		fmt.Printf("❌ 最终失败: %v\n", err)
	} else {
		fmt.Printf("✅ 最终成功: %s\n", string(data))
	}

	// 测试上下文超时
	fmt.Println("\n测试上下文超时:")
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	slowLoader := func(ctx context.Context) ([]byte, error) {
		select {
		case <-time.After(200 * time.Millisecond):
			return []byte("slow_result"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	_, err = cache.GetOrCompute(timeoutCtx, []byte("slow_key"), 0, slowLoader)
	if err != nil {
		fmt.Printf("✅ 超时处理正确: %v\n", err)
	}

	fmt.Println()
}

// 示例 5: 监控和调试
func demonstrateMonitoring() {
	fmt.Println("5. 监控和调试")
	fmt.Println("-------------")

	cache := cachex.NewCtxCache(cachex.NewLRUHandler(50))
	defer cache.Close()

	// 统计信息
	var stats struct {
		hits     int64
		misses   int64
		sets     int64
		computes int64
	}

	ctx := context.Background()

	// 监控装饰器
	monitoredGet := func(key []byte) ([]byte, error) {
		data, err := cache.Get(ctx, key)
		if err == nil {
			atomic.AddInt64(&stats.hits, 1)
		} else {
			atomic.AddInt64(&stats.misses, 1)
		}
		return data, err
	}

	monitoredSet := func(key, value []byte) error {
		err := cache.Set(ctx, key, value)
		if err == nil {
			atomic.AddInt64(&stats.sets, 1)
		}
		return err
	}

	monitoredCompute := func(key []byte, loader func(context.Context) ([]byte, error)) ([]byte, error) {
		// 检查是否需要计算
		if _, err := cache.Get(ctx, key); err != nil {
			atomic.AddInt64(&stats.computes, 1)
		}
		return cache.GetOrCompute(ctx, key, time.Second, loader)
	}

	// 模拟工作负载
	fmt.Println("模拟工作负载...")
	
	loader := func(ctx context.Context) ([]byte, error) {
		time.Sleep(10 * time.Millisecond)
		return []byte(fmt.Sprintf("computed_%d", rand.Int())), nil
	}

	// 随机操作
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key_%d", rand.Intn(20))) // 20个可能的键
		
		switch rand.Intn(3) {
		case 0: // Get
			monitoredGet(key)
		case 1: // Set
			value := []byte(fmt.Sprintf("value_%d", i))
			monitoredSet(key, value)
		case 2: // GetOrCompute
			monitoredCompute(key, loader)
		}
	}

	// 输出统计
	total := atomic.LoadInt64(&stats.hits) + atomic.LoadInt64(&stats.misses)
	hitRate := float64(atomic.LoadInt64(&stats.hits)) / float64(total) * 100

	fmt.Printf("\n📊 缓存统计:\n")
	fmt.Printf("  命中次数: %d\n", atomic.LoadInt64(&stats.hits))
	fmt.Printf("  未命中次数: %d\n", atomic.LoadInt64(&stats.misses))
	fmt.Printf("  命中率: %.1f%%\n", hitRate)
	fmt.Printf("  设置次数: %d\n", atomic.LoadInt64(&stats.sets))
	fmt.Printf("  计算次数: %d\n", atomic.LoadInt64(&stats.computes))

	// 性能分析
	fmt.Println("\n📈 性能分析:")
	const testOps = 10000
	
	// 测试 Get 性能
	for i := 0; i < 50; i++ {
		cache.Set(ctx, []byte(fmt.Sprintf("perf_key_%d", i)), []byte("test_value"))
	}

	start := time.Now()
	for i := 0; i < testOps; i++ {
		cache.Get(ctx, []byte(fmt.Sprintf("perf_key_%d", i%50)))
	}
	elapsed := time.Since(start)
	
	fmt.Printf("  %d 次 Get 操作耗时: %v\n", testOps, elapsed)
	fmt.Printf("  平均延迟: %v\n", elapsed/time.Duration(testOps))
	fmt.Printf("  吞吐量: %.0f ops/sec\n", float64(testOps)/elapsed.Seconds())

	fmt.Println("\n监控演示完成")
}