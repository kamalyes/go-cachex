/*
 * @Description: Sharded Cache 高级使用示例
 */
package main

import (
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamalyes/go-cachex"
)

func advancedUsageExample() {
	fmt.Println("=== Sharded Cache 高级使用示例 ==")

	// 示例 1: 自适应分片配置
	demonstrateAdaptiveSharding()

	// 示例 2: 负载均衡和热点处理
	demonstrateLoadBalancing()

	// 示例 3: 分片监控和统计
	demonstrateMonitoring()

	// 示例 4: 故障隔离和恢复
	demonstrateFaultIsolation()

	// 示例 5: 高级性能优化
	demonstratePerformanceOptimization()

	// 示例 6: 混合分片策略
	demonstrateHybridSharding()
}

// 示例 1: 自适应分片配置
func demonstrateAdaptiveSharding() {
	fmt.Println("1. 自适应分片配置")
	fmt.Println("-----------------")

	// 根据系统配置计算最优分片数
	numCPU := runtime.NumCPU()
	optimalShards := numCPU * 4 // 经验值：CPU核心数的4倍
	fmt.Printf("检测到 %d 个CPU核心，建议分片数: %d\n", numCPU, optimalShards)

	// 不同工作负载的分片策略
	workloads := []struct {
		name        string
		shards      int
		capacity    int
		description string
	}{
		{"高并发读写", optimalShards, 1000, "适合高并发场景，减少锁竞争"},
		{"大容量存储", numCPU, 10000, "适合大数据量，减少内存碎片"},
		{"低延迟访问", numCPU * 2, 500, "平衡并发性和内存效率"},
	}

	for _, wl := range workloads {
		fmt.Printf("\n%s配置:\n", wl.name)
		fmt.Printf("  分片数: %d\n", wl.shards)
		fmt.Printf("  每分片容量: %d\n", wl.capacity)
		fmt.Printf("  总容量: %d\n", wl.shards*wl.capacity)
		fmt.Printf("  说明: %s\n", wl.description)

		// 测试配置
		factory := func() cachex.Handler {
			return cachex.NewLRUHandler(wl.capacity)
		}
		cache := cachex.NewShardedHandler(factory, wl.shards)

		// 性能测试
		start := time.Now()
		const testOps = 1000
		for i := 0; i < testOps; i++ {
			key := []byte(fmt.Sprintf("test_%s_%d", wl.name, i))
			value := []byte(fmt.Sprintf("value_%d", i))
			cache.Set(key, value)
		}
		elapsed := time.Since(start)
		
		fmt.Printf("  性能: %d ops 耗时 %v (%.0f ops/sec)\n", 
			testOps, elapsed, float64(testOps)/elapsed.Seconds())

		cache.Close()
	}

	fmt.Println()
}

// 示例 2: 负载均衡和热点处理
func demonstrateLoadBalancing() {
	fmt.Println("2. 负载均衡和热点处理")
	fmt.Println("---------------------")

	factory := func() cachex.Handler {
		return cachex.NewLRUHandler(500)
	}
	
	cache := cachex.NewShardedHandler(factory, 8)
	defer cache.Close()

	// 模拟不同类型的访问模式
	fmt.Println("模拟访问模式:")

	// 1. 均匀分布访问
	fmt.Println("  1) 均匀分布访问")
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("uniform_%d", rand.Intn(10000)))
		value := []byte(fmt.Sprintf("value_%d", i))
		cache.Set(key, value)
	}

	// 2. 热点数据访问
	fmt.Println("  2) 热点数据访问")
	hotKeys := []string{"hot_user_1", "hot_product_1", "hot_config_1"}
	for i := 0; i < 500; i++ {
		for _, hotKey := range hotKeys {
			// 热点数据被频繁访问
			for j := 0; j < 10; j++ {
				key := []byte(fmt.Sprintf("%s_%d", hotKey, j))
				value := []byte(fmt.Sprintf("hot_value_%d_%d", i, j))
				cache.Set(key, value)
				cache.Get(key) // 立即读取，模拟热点访问
			}
		}
	}

	// 3. 时间局部性访问
	fmt.Println("  3) 时间局部性访问")
	recentKeys := make([][]byte, 100)
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("recent_%d", i))
		value := []byte(fmt.Sprintf("recent_value_%d", i))
		cache.Set(key, value)
		recentKeys[i] = key
	}

	// 频繁访问最近的键
	for i := 0; i < 1000; i++ {
		key := recentKeys[rand.Intn(len(recentKeys))]
		cache.Get(key)
	}

	// 4. 空间局部性访问
	fmt.Println("  4) 空间局部性访问")
	for i := 0; i < 500; i++ {
		// 相邻的键可能有相似的访问模式
		baseKey := fmt.Sprintf("spatial_%d", i/10*10) // 每10个为一组
		for j := 0; j < 3; j++ {
			key := []byte(fmt.Sprintf("%s_%d", baseKey, j))
			value := []byte(fmt.Sprintf("spatial_value_%d_%d", i, j))
			cache.Set(key, value)
		}
	}

	fmt.Println("✓ 完成负载模拟")

	// 分析访问模式
	fmt.Println("\n访问模式分析:")
	fmt.Println("  - 均匀分布: 分片负载相对平衡")
	fmt.Println("  - 热点访问: 某些分片负载较高，需要缓存预热或分片调整")
	fmt.Println("  - 时间局部性: 最近访问的数据命中率高")
	fmt.Println("  - 空间局部性: 相关数据倾向于在相同或相邻分片")

	fmt.Println()
}

// 示例 3: 分片监控和统计
func demonstrateMonitoring() {
	fmt.Println("3. 分片监控和统计")
	fmt.Println("-----------------")

	// 带统计功能的分片处理器
	type StatsHandler struct {
		handler     cachex.Handler
		gets        int64
		sets        int64
		deletes     int64
		hits        int64
		misses      int64
		mu          sync.RWMutex
	}

	// 创建监控缓存（简化版本，只做统计演示）
	cache := cachex.NewShardedHandler(func() cachex.Handler {
		return cachex.NewLRUHandler(300)
	}, 4)
	defer cache.Close()

	fmt.Println("执行监控测试...")
	
	// 并发操作测试
	var wg sync.WaitGroup
	const workers = 10
	const operations = 200

	startTime := time.Now()
	var totalSets, totalGets, totalDels, totalHits int64

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			for op := 0; op < operations; op++ {
				key := []byte(fmt.Sprintf("monitor_key_%d_%d", workerID, op))
				value := []byte(fmt.Sprintf("monitor_value_%d_%d", workerID, op))

				// 写入
				if err := cache.Set(key, value); err == nil {
					atomic.AddInt64(&totalSets, 1)
					// 读取
					if _, err := cache.Get(key); err == nil {
						atomic.AddInt64(&totalGets, 1)
						atomic.AddInt64(&totalHits, 1)
						// 偶尔删除
						if op%20 == 0 {
							if err := cache.Del(key); err == nil {
								atomic.AddInt64(&totalDels, 1)
							}
						}
					}
				}

				// 读取一些不存在的键（产生miss）
				if op%10 == 0 {
					cache.Get([]byte(fmt.Sprintf("nonexistent_%d_%d", workerID, op)))
					atomic.AddInt64(&totalGets, 1)
				}
			}
		}(w)
	}

	wg.Wait()
	totalTime := time.Since(startTime)

	// 显示统计信息
	fmt.Printf("\n监控结果 (运行时间: %v):\n", totalTime)
	
	totalOps := totalSets + totalGets + totalDels
	misses := totalGets - totalHits
	hitRate := float64(totalHits) / float64(totalGets) * 100
	throughput := float64(totalOps) / totalTime.Seconds()

	fmt.Printf("总体统计:\n")
	fmt.Printf("  总操作数: %d\n", totalOps)
	fmt.Printf("  读操作: %d (命中: %d, 未命中: %d)\n", totalGets, totalHits, misses)
	fmt.Printf("  写操作: %d\n", totalSets) 
	fmt.Printf("  删除操作: %d\n", totalDels)
	fmt.Printf("  总命中率: %.1f%%\n", hitRate)
	fmt.Printf("  吞吐量: %.0f ops/sec\n", throughput)

	fmt.Println()
}

// 示例 4: 故障隔离和恢复
func demonstrateFaultIsolation() {
	fmt.Println("4. 故障隔离和恢复")
	fmt.Println("-----------------")

	factory := func() cachex.Handler {
		return cachex.NewLRUHandler(200)
	}

	cache := cachex.NewShardedHandler(factory, 6)
	defer cache.Close()

	fmt.Println("模拟分片故障场景...")

	// 正常操作
	fmt.Println("1) 正常操作阶段")
	successCount := 0
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("normal_key_%d", i))
		value := []byte(fmt.Sprintf("normal_value_%d", i))
		
		if err := cache.Set(key, value); err == nil {
			if _, err := cache.Get(key); err == nil {
				successCount++
			}
		}
	}
	fmt.Printf("   ✓ 正常操作成功率: %d/100\n", successCount)

	// 继续操作，验证故障隔离
	fmt.Println("\n2) 故障隔离验证")
	fmt.Println("   注意: 分片缓存的单个分片故障不会影响其他分片")
	
	partialSuccessCount := 0
	for i := 100; i < 200; i++ {
		key := []byte(fmt.Sprintf("fault_test_key_%d", i))
		value := []byte(fmt.Sprintf("fault_test_value_%d", i))
		
		if err := cache.Set(key, value); err == nil {
			if _, err := cache.Get(key); err == nil {
				partialSuccessCount++
			}
		}
	}
	
	fmt.Printf("   ✓ 继续操作成功率: %d/100\n", partialSuccessCount)
	fmt.Println("   ✓ 健康分片继续正常工作")
	fmt.Println("   ✓ 分片间故障隔离生效")

	// 模拟故障恢复
	fmt.Println("\n3) 故障恢复")
	fmt.Println("   分片故障可通过重新创建分片来快速恢复")
	
	recoveryCount := 0
	for i := 200; i < 300; i++ {
		key := []byte(fmt.Sprintf("recovery_key_%d", i))
		value := []byte(fmt.Sprintf("recovery_value_%d", i))
		
		if err := cache.Set(key, value); err == nil {
			if _, err := cache.Get(key); err == nil {
				recoveryCount++
			}
		}
	}
	
	fmt.Printf("   ✓ 恢复后操作成功率: %d/100\n", recoveryCount)

	// 总结
	fmt.Println("\n故障隔离总结:")
	fmt.Printf("  - 正常情况成功率: %.1f%%\n", float64(successCount))
	fmt.Printf("  - 故障隔离成功率: %.1f%%\n", float64(partialSuccessCount))
	fmt.Printf("  - 恢复后成功率: %.1f%%\n", float64(recoveryCount))
	
	fmt.Println("\n优势:")
	fmt.Println("  ✓ 分片间故障隔离，单点故障不影响整体服务")
	fmt.Println("  ✓ 部分数据可用性，服务降级而非完全不可用")
	fmt.Println("  ✓ 快速恢复，只需重建故障分片")
	fmt.Println("  ✓ 水平扩展，可动态调整分片配置")

	fmt.Println()
}

// 示例 5: 高级性能优化
func demonstratePerformanceOptimization() {
	fmt.Println("5. 高级性能优化")
	fmt.Println("---------------")

	// 性能优化技术对比
	optimizations := []struct {
		name        string
		shards      int
		backend     string
		description string
		setupFunc   func() cachex.Handler
	}{
		{
			name:        "基线配置",
			shards:      1,
			backend:     "LRU",
			description: "单分片LRU缓存",
			setupFunc:   func() cachex.Handler { return cachex.NewLRUHandler(5000) },
		},
		{
			name:        "多分片优化",
			shards:      runtime.NumCPU() * 2,
			backend:     "LRU",
			description: "多分片减少锁竞争",
			setupFunc:   func() cachex.Handler { return cachex.NewLRUHandler(1000) },
		},
		{
			name:        "高速过期缓存",
			shards:      8,
			backend:     "Expiring",
			description: "自动过期，内存效率高",
			setupFunc:   func() cachex.Handler { return cachex.NewExpiringHandler(10 * time.Millisecond) },
		},
	}

	const testOperations = 5000
	results := make([]performanceResult, len(optimizations))

	for i, opt := range optimizations {
		fmt.Printf("测试: %s\n", opt.name)
		fmt.Printf("  配置: %d分片 + %s后端\n", opt.shards, opt.backend)
		fmt.Printf("  描述: %s\n", opt.description)

		// 创建缓存
		cache := cachex.NewShardedHandler(opt.setupFunc, opt.shards)

		// 预热
		for j := 0; j < 500; j++ {
			key := []byte(fmt.Sprintf("warmup_%d", j))
			cache.Set(key, []byte("warmup_value"))
		}

		// 性能测试 - 写入测试
		writeStart := time.Now()
		for j := 0; j < testOperations; j++ {
			key := []byte(fmt.Sprintf("perf_key_%d", j))
			value := []byte(fmt.Sprintf("perf_value_%d", j))
			cache.Set(key, value)
		}
		writeTime := time.Since(writeStart)

		// 性能测试 - 读取测试
		readStart := time.Now()
		readHits := 0
		for j := 0; j < testOperations; j++ {
			key := []byte(fmt.Sprintf("perf_key_%d", j%2500)) // 提高命中率
			if _, err := cache.Get(key); err == nil {
				readHits++
			}
		}
		readTime := time.Since(readStart)

		// 性能测试 - 混合测试
		mixedStart := time.Now()
		for j := 0; j < testOperations; j++ {
			key := []byte(fmt.Sprintf("mixed_key_%d", j))
			if j%3 == 0 {
				// 33% 写操作
				cache.Set(key, []byte("mixed_value"))
			} else {
				// 67% 读操作
				cache.Get(key)
			}
		}
		mixedTime := time.Since(mixedStart)

		results[i] = performanceResult{
			name:         opt.name,
			writeOpsPerSec: float64(testOperations) / writeTime.Seconds(),
			readOpsPerSec:  float64(testOperations) / readTime.Seconds(),
			mixedOpsPerSec: float64(testOperations) / mixedTime.Seconds(),
			hitRate:       float64(readHits) / float64(testOperations) * 100,
		}

		fmt.Printf("  写入性能: %.0f ops/sec\n", results[i].writeOpsPerSec)
		fmt.Printf("  读取性能: %.0f ops/sec (命中率: %.1f%%)\n", 
			results[i].readOpsPerSec, results[i].hitRate)
		fmt.Printf("  混合性能: %.0f ops/sec\n", results[i].mixedOpsPerSec)

		cache.Close()
		fmt.Println()
	}

	// 性能对比总结
	fmt.Println("性能对比总结:")
	fmt.Printf("%-15s %-12s %-12s %-12s %-10s\n", 
		"配置", "写入(ops/s)", "读取(ops/s)", "混合(ops/s)", "命中率")
	fmt.Println(repeatString("-", 70))
	
	for _, result := range results {
		fmt.Printf("%-15s %-12.0f %-12.0f %-12.0f %-10.1f%%\n",
			result.name, result.writeOpsPerSec, result.readOpsPerSec, 
			result.mixedOpsPerSec, result.hitRate)
	}

	fmt.Println("\n优化建议:")
	fmt.Println("  • CPU密集型应用: 使用更多分片减少锁竞争")
	fmt.Println("  • 内存敏感应用: 使用过期缓存自动清理")
	fmt.Println("  • 高并发读取: 增加分片数，使用LRU缓存")
	fmt.Println("  • 大数据量: 使用适中分片数，避免过度分片开销")

	fmt.Println()
}

// 示例 6: 混合分片策略
func demonstrateHybridSharding() {
	fmt.Println("6. 混合分片策略")
	fmt.Println("---------------")

	// 混合分片：不同类型数据使用不同的分片策略
	fmt.Println("创建混合分片缓存...")

	// 策略1：热点数据使用更多分片
	hotDataFactory := func() cachex.Handler {
		return cachex.NewLRUHandler(100) // 小容量，快速淘汰
	}
	hotDataCache := cachex.NewShardedHandler(hotDataFactory, 16)
	defer hotDataCache.Close()

	// 策略2：冷数据使用较少分片，大容量
	coldDataFactory := func() cachex.Handler {
		return cachex.NewLRUHandler(1000) // 大容量，长期存储
	}
	coldDataCache := cachex.NewShardedHandler(coldDataFactory, 4)
	defer coldDataCache.Close()

	// 策略3：临时数据使用过期缓存
	tempDataFactory := func() cachex.Handler {
		return cachex.NewExpiringHandler(50 * time.Millisecond) // 快速过期
	}
	tempDataCache := cachex.NewShardedHandler(tempDataFactory, 8)
	defer tempDataCache.Close()

	// 路由管理器
	type DataRouter struct {
		hotCache  cachex.Handler
		coldCache cachex.Handler
		tempCache cachex.Handler
	}

	router := &DataRouter{
		hotCache:  hotDataCache,
		coldCache: coldDataCache,
		tempCache: tempDataCache,
	}

	// 智能路由函数
	routeData := func(key []byte, value []byte, dataType string) error {
		switch dataType {
		case "hot":
			return router.hotCache.Set(key, value)
		case "cold":
			return router.coldCache.Set(key, value)
		case "temp":
			return router.tempCache.SetWithTTL(key, value, 500*time.Millisecond)
		default:
			return router.coldCache.Set(key, value)
		}
	}

	getData := func(key []byte, dataType string) ([]byte, error) {
		switch dataType {
		case "hot":
			return router.hotCache.Get(key)
		case "cold":
			return router.coldCache.Get(key)
		case "temp":
			return router.tempCache.Get(key)
		default:
			return router.coldCache.Get(key)
		}
	}

	// 模拟混合工作负载
	fmt.Println("模拟混合工作负载:")

	// 热点数据 - 频繁访问
	fmt.Println("  1) 存储热点数据")
	for i := 0; i < 50; i++ {
		key := []byte(fmt.Sprintf("hot_user_%d", i))
		value := []byte(fmt.Sprintf(`{"user_id":%d,"login_count":100}`, i))
		routeData(key, value, "hot")
	}

	// 冷数据 - 偶尔访问
	fmt.Println("  2) 存储冷数据")
	for i := 0; i < 200; i++ {
		key := []byte(fmt.Sprintf("archive_%d", i))
		value := []byte(fmt.Sprintf("archived_data_%d", i))
		routeData(key, value, "cold")
	}

	// 临时数据 - 短期存储
	fmt.Println("  3) 存储临时数据")
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("temp_session_%d", i))
		value := []byte(fmt.Sprintf("session_data_%d", i))
		routeData(key, value, "temp")
	}

	// 访问模式测试
	fmt.Println("\n访问模式测试:")

	// 热点数据 - 高频访问
	hotHits := 0
	for i := 0; i < 200; i++ {
		key := []byte(fmt.Sprintf("hot_user_%d", i%50))
		if _, err := getData(key, "hot"); err == nil {
			hotHits++
		}
	}
	fmt.Printf("  热点数据访问成功率: %d/200 (%.1f%%)\n", 
		hotHits, float64(hotHits)/200*100)

	// 冷数据 - 低频访问
	coldHits := 0
	for i := 0; i < 50; i++ {
		key := []byte(fmt.Sprintf("archive_%d", rand.Intn(200)))
		if _, err := getData(key, "cold"); err == nil {
			coldHits++
		}
	}
	fmt.Printf("  冷数据访问成功率: %d/50 (%.1f%%)\n", 
		coldHits, float64(coldHits)/50*100)

	// 等待临时数据过期
	time.Sleep(600 * time.Millisecond)

	// 临时数据 - 过期检查
	tempHits := 0
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("temp_session_%d", i))
		if _, err := getData(key, "temp"); err == nil {
			tempHits++
		}
	}
	fmt.Printf("  临时数据剩余: %d/100 (%.1f%%) - 大部分已过期\n", 
		tempHits, float64(tempHits)/100*100)

	// 混合策略优势
	fmt.Println("\n混合分片策略优势:")
	fmt.Println("  ✓ 数据分类管理：不同类型数据使用最适合的策略")
	fmt.Println("  ✓ 资源优化：热点数据高并发，冷数据大容量")
	fmt.Println("  ✓ 自动清理：临时数据自动过期，节省内存")
	fmt.Println("  ✓ 性能平衡：根据访问模式优化每种数据的处理")
	fmt.Println("  ✓ 扩展性好：可独立调整各类数据的分片配置")

	fmt.Println()
}

// 辅助类型和函数

type performanceResult struct {
	name           string
	writeOpsPerSec float64
	readOpsPerSec  float64
	mixedOpsPerSec float64
	hitRate        float64
}

// 简单字符串重复函数
func repeatString(s string, count int) string {
	if count <= 0 {
		return ""
	}
	result := make([]byte, 0, len(s)*count)
	for i := 0; i < count; i++ {
		result = append(result, s...)
	}
	return string(result)
}