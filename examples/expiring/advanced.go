/*
 * @Description: Expiring Cache 高级使用示例
 */
package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kamalyes/go-cachex"
)

func advancedUsageExample() {
	fmt.Println("=== Expiring Cache 高级使用示例 ==")
	
	// 示例 1: 不同TTL策略
	demonstrateTTLStrategies()
	
	// 示例 2: 并发访问和自动清理
	demonstrateConcurrentAccess()
	
	// 示例 3: 缓存预热和数据分组
	demonstrateWarmupAndGrouping()
	
	// 示例 4: 性能监控
	demonstratePerformanceMonitoring()
}

// 示例 1: 不同TTL策略
func demonstrateTTLStrategies() {
	fmt.Println("1. 不同TTL策略演示")
	fmt.Println("-------------------")
	
	// 创建快速清理的缓存（每50ms清理一次）
	cache := cachex.NewExpiringHandler(50 * time.Millisecond)
	defer cache.Close()
	
	// 短期缓存 - 用于临时数据
	cache.SetWithTTL([]byte("session:user123"), []byte(`{"user_id": 123, "login_time": "2024-01-01"}`), 100*time.Millisecond)
	
	// 中期缓存 - 用于配置数据
	cache.SetWithTTL([]byte("config:feature_flags"), []byte(`{"new_ui": true, "beta_features": false}`), time.Second)
	
	// 永久缓存 - 用于系统配置
	cache.SetWithTTL([]byte("system:version"), []byte("v1.0.0"), -1)
	
	// 立即过期 - 用于测试
	cache.SetWithTTL([]byte("test:immediate"), []byte("will_expire"), 0)
	
	// 检查各种TTL状态
	keys := []string{"session:user123", "config:feature_flags", "system:version", "test:immediate"}
	for _, key := range keys {
		if val, err := cache.Get([]byte(key)); err == nil {
			if ttl, _ := cache.GetTTL([]byte(key)); ttl > 0 {
				fmt.Printf("✓ %s: %s (TTL: %v)\n", key, string(val), ttl)
			} else {
				fmt.Printf("✓ %s: %s (永不过期)\n", key, string(val))
			}
		} else {
			fmt.Printf("✗ %s: 已过期或不存在\n", key)
		}
	}
	
	// 等待部分过期
	time.Sleep(150 * time.Millisecond)
	fmt.Println("\n150ms后状态:")
	for _, key := range keys {
		if val, err := cache.Get([]byte(key)); err == nil {
			fmt.Printf("✓ %s: %s\n", key, string(val))
		} else {
			fmt.Printf("✗ %s: 已过期\n", key)
		}
	}
	fmt.Println()
}

// 示例 2: 并发访问和自动清理
func demonstrateConcurrentAccess() {
	fmt.Println("2. 并发访问和自动清理演示")
	fmt.Println("-------------------------")
	
	cache := cachex.NewExpiringHandler(25 * time.Millisecond)
	defer cache.Close()
	
	var wg sync.WaitGroup
	const workers = 10
	const operations = 20
	
	// 启动多个goroutine进行并发读写
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			for i := 0; i < operations; i++ {
				key := []byte(fmt.Sprintf("worker:%d:item:%d", workerID, i))
				value := []byte(fmt.Sprintf("value_%d_%d_%d", workerID, i, time.Now().Unix()))
				
				// 随机TTL: 10-100ms
				ttl := time.Duration(10+i*5) * time.Millisecond
				
				// 写入数据
				if err := cache.SetWithTTL(key, value, ttl); err != nil {
					log.Printf("Worker %d: 写入失败 %s: %v", workerID, key, err)
					continue
				}
				
				// 立即读取验证
				if _, err := cache.Get(key); err == nil {
					if ttl, _ := cache.GetTTL(key); ttl > 0 {
						fmt.Printf("Worker %d: 成功读取 %s (剩余TTL: %v)\n", workerID, key, ttl)
					}
				} else {
					fmt.Printf("Worker %d: 读取失败 %s: %v\n", workerID, key, err)
				}
				
				// 短暂等待
				time.Sleep(5 * time.Millisecond)
			}
		}(w)
	}
	
	wg.Wait()
	
	// 等待自动清理
	fmt.Println("\n等待自动清理...")
	time.Sleep(200 * time.Millisecond)
	fmt.Println("自动清理完成")
}

// 示例 3: 缓存预热和数据分组
func demonstrateWarmupAndGrouping() {
	fmt.Println("3. 缓存预热和数据分组演示")
	fmt.Println("-----------------------")
	
	cache := cachex.NewExpiringHandler(100 * time.Millisecond)
	defer cache.Close()
	
	// 数据分组预热
	groups := map[string]struct {
		ttl  time.Duration
		data map[string]string
	}{
		"users": {
			ttl: 500 * time.Millisecond,
			data: map[string]string{
				"user:1": `{"id":1,"name":"Alice","role":"admin"}`,
				"user:2": `{"id":2,"name":"Bob","role":"user"}`,
				"user:3": `{"id":3,"name":"Charlie","role":"moderator"}`,
			},
		},
		"products": {
			ttl: 300 * time.Millisecond,
			data: map[string]string{
				"product:1": `{"id":1,"name":"Laptop","price":999}`,
				"product:2": `{"id":2,"name":"Mouse","price":25}`,
				"product:3": `{"id":3,"name":"Keyboard","price":75}`,
			},
		},
		"config": {
			ttl: -1, // 永不过期
			data: map[string]string{
				"config:app":  `{"debug":false,"version":"1.0"}`,
				"config:db":   `{"host":"localhost","port":5432}`,
				"config:redis": `{"host":"localhost","port":6379}`,
			},
		},
	}
	
	// 预热缓存
	fmt.Println("开始缓存预热...")
	for groupName, group := range groups {
		fmt.Printf("预热分组: %s (TTL: %v)\n", groupName, group.ttl)
		for key, value := range group.data {
			if err := cache.SetWithTTL([]byte(key), []byte(value), group.ttl); err != nil {
				log.Printf("预热失败 %s: %v", key, err)
			}
		}
	}
	
	// 验证预热结果
	fmt.Println("\n预热完成，验证数据:")
	allKeys := []string{}
	for _, group := range groups {
		for key := range group.data {
			allKeys = append(allKeys, key)
		}
	}
	
	checkCacheStatus := func(label string) {
		fmt.Printf("\n%s:\n", label)
		activeCount := 0
		for _, key := range allKeys {
			if _, err := cache.Get([]byte(key)); err == nil {
				activeCount++
				ttl, _ := cache.GetTTL([]byte(key))
				if ttl > 0 {
					fmt.Printf("  ✓ %s (TTL: %v)\n", key, ttl)
				} else {
					fmt.Printf("  ✓ %s (永不过期)\n", key)
				}
			} else {
				fmt.Printf("  ✗ %s (已过期)\n", key)
			}
		}
		fmt.Printf("活跃键数量: %d/%d\n", activeCount, len(allKeys))
	}
	
	checkCacheStatus("初始状态")
	
	// 等待部分过期
	time.Sleep(350 * time.Millisecond)
	checkCacheStatus("350ms后")
	
	time.Sleep(200 * time.Millisecond)
	checkCacheStatus("550ms后")
	
	fmt.Println()
}

// 示例 4: 性能监控
func demonstratePerformanceMonitoring() {
	fmt.Println("4. 性能监控演示")
	fmt.Println("----------------")
	
	cache := cachex.NewExpiringHandler(50 * time.Millisecond)
	defer cache.Close()
	
	// 性能测试参数
	const batchSize = 1000
	const valueSize = 100
	
	// 创建测试数据
	value := make([]byte, valueSize)
	for i := range value {
		value[i] = byte('A' + (i % 26))
	}
	
	// 批量写入性能测试
	fmt.Printf("批量写入性能测试 (%d 个键，每个值 %d 字节):\n", batchSize, valueSize)
	start := time.Now()
	
	for i := 0; i < batchSize; i++ {
		key := []byte(fmt.Sprintf("perf_test_%d", i))
		ttl := time.Duration(50+i) * time.Millisecond // 变化的TTL
		if err := cache.SetWithTTL(key, value, ttl); err != nil {
			log.Printf("写入失败 %d: %v", i, err)
		}
	}
	
	writeTime := time.Since(start)
	fmt.Printf("  写入耗时: %v\n", writeTime)
	fmt.Printf("  平均写入速度: %.0f ops/sec\n", float64(batchSize)/writeTime.Seconds())
	
	// 批量读取性能测试
	fmt.Println("\n批量读取性能测试:")
	start = time.Now()
	successCount := 0
	
	for i := 0; i < batchSize; i++ {
		key := []byte(fmt.Sprintf("perf_test_%d", i))
		if _, err := cache.Get(key); err == nil {
			successCount++
		}
	}
	
	readTime := time.Since(start)
	fmt.Printf("  读取耗时: %v\n", readTime)
	fmt.Printf("  成功读取: %d/%d\n", successCount, batchSize)
	fmt.Printf("  平均读取速度: %.0f ops/sec\n", float64(batchSize)/readTime.Seconds())
	
	// 等待自动清理并监控
	fmt.Println("\n监控自动清理过程:")
	for round := 1; round <= 10; round++ {
		time.Sleep(50 * time.Millisecond)
		
		// 统计剩余键数量
		activeCount := 0
		for i := 0; i < batchSize; i++ {
			key := []byte(fmt.Sprintf("perf_test_%d", i))
			if _, err := cache.Get(key); err == nil {
				activeCount++
			}
		}
		
		fmt.Printf("  第%d轮检查 (%dms): 剩余 %d/%d 键\n", 
			round, round*50, activeCount, batchSize)
		
		if activeCount == 0 {
			fmt.Println("  所有键已被清理")
			break
		}
	}
	
	fmt.Println("\n性能监控完成")
}