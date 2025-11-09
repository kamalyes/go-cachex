/*
 * @Description: TwoLevel Cache 基础使用示例
 */
package main

import (
	"fmt"
	"time"

	"github.com/kamalyes/go-cachex"
)

func basicUsageExample() {
	fmt.Println("=== TwoLevel Cache 基础使用示例 ===")

	// 示例 1: 基本的两级缓存配置
	demonstrateBasicTwoLevel()

	// 示例 2: 写透模式 vs 异步模式
	demonstrateWriteModes()

	// 示例 3: L1 未命中 L2 命中的提升机制
	demonstratePromotion()

	// 示例 4: TTL 处理和传播
	demonstrateTTLHandling()

	// 示例 5: 不同后端组合
	demonstrateBackendCombinations()

	// 示例 6: 性能比较
	demonstratePerformanceComparison()
}

// 示例 1: 基本的两级缓存配置
func demonstrateBasicTwoLevel() {
	fmt.Println("1. 基本两级缓存配置")
	fmt.Println("-------------------")

	// 创建 L1 缓存（快速但容量小）
	l1 := cachex.NewLRUHandler(5) // 只能存储5个条目
	defer l1.Close()

	// 创建 L2 缓存（容量大）
	l2 := cachex.NewLRUHandler(100) // 可以存储100个条目
	defer l2.Close()

	// 创建两级缓存 (写透模式)
	twoLevel := cachex.NewTwoLevelHandler(l1, l2, true)
	defer twoLevel.Close()

	fmt.Printf("L1 缓存容量: 5 个条目 (快速访问)\n")
	fmt.Printf("L2 缓存容量: 100 个条目 (大容量)\n")
	fmt.Printf("写入模式: 写透 (同时写入 L1 和 L2)\n\n")

	// 基本操作演示
	fmt.Println("基本操作演示:")

	// 1. 写入数据
	fmt.Println("  1) 写入数据")
	err := twoLevel.Set([]byte("user:1001"), []byte(`{"name":"张三","age":25}`))
	if err != nil {
		fmt.Printf("    ❌ 写入失败: %v\n", err)
	} else {
		fmt.Printf("    ✓ 成功写入用户数据到两级缓存\n")
	}

	// 2. 读取数据（应该从 L1 命中）
	fmt.Println("  2) 读取数据 (L1 命中)")
	if data, err := twoLevel.Get([]byte("user:1001")); err == nil {
		fmt.Printf("    ✓ 从缓存读取: %s\n", string(data))
		
		// 验证 L1 中有数据
		if _, err := l1.Get([]byte("user:1001")); err == nil {
			fmt.Printf("    ✓ 数据在 L1 缓存中\n")
		}
	} else {
		fmt.Printf("    ❌ 读取失败: %v\n", err)
	}

	// 3. 删除数据
	fmt.Println("  3) 删除数据")
	err = twoLevel.Del([]byte("user:1001"))
	if err != nil {
		fmt.Printf("    ❌ 删除失败: %v\n", err)
	} else {
		fmt.Printf("    ✓ 成功从两级缓存删除数据\n")
	}

	// 4. 尝试读取已删除的数据
	fmt.Println("  4) 尝试读取已删除的数据")
	if _, err := twoLevel.Get([]byte("user:1001")); err != nil {
		fmt.Printf("    ✓ 确认数据已删除: %v\n", err)
	}

	fmt.Println()
}

// 示例 2: 写透模式 vs 异步模式
func demonstrateWriteModes() {
	fmt.Println("2. 写透模式 vs 异步模式")
	fmt.Println("----------------------")

	// 写透模式 (同步)
	fmt.Println("写透模式 (WriteThrough = true):")
	l1_sync := cachex.NewLRUHandler(10)
	l2_sync := cachex.NewLRUHandler(50)
	defer l1_sync.Close()
	defer l2_sync.Close()

	syncCache := cachex.NewTwoLevelHandler(l1_sync, l2_sync, true)
	defer syncCache.Close()

	start := time.Now()
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("sync_key_%d", i))
		value := []byte(fmt.Sprintf("sync_value_%d", i))
		syncCache.Set(key, value)
	}
	syncTime := time.Since(start)

	fmt.Printf("  ✓ 写透模式写入100个条目耗时: %v\n", syncTime)
	fmt.Printf("  ✓ 特点: 数据立即写入两级缓存，一致性强\n")

	// 验证两级都有数据
	testKey := []byte("sync_key_50")
	if _, err1 := l1_sync.Get(testKey); err1 == nil {
		if _, err2 := l2_sync.Get(testKey); err2 == nil {
			fmt.Printf("  ✓ 验证: L1 和 L2 都包含数据\n")
		}
	}

	// 异步模式
	fmt.Println("\n异步模式 (WriteThrough = false):")
	l1_async := cachex.NewLRUHandler(10)
	l2_async := cachex.NewLRUHandler(50)
	defer l1_async.Close()
	defer l2_async.Close()

	asyncCache := cachex.NewTwoLevelHandler(l1_async, l2_async, false)
	defer asyncCache.Close()

	start = time.Now()
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("async_key_%d", i))
		value := []byte(fmt.Sprintf("async_value_%d", i))
		asyncCache.Set(key, value)
	}
	asyncTime := time.Since(start)

	fmt.Printf("  ✓ 异步模式写入100个条目耗时: %v\n", asyncTime)
	fmt.Printf("  ✓ 特点: 立即写入L1，异步写入L2，性能更好\n")

	// 等待异步写入完成
	time.Sleep(10 * time.Millisecond)

	// 验证数据
	testKey2 := []byte("async_key_50")
	if _, err1 := l1_async.Get(testKey2); err1 == nil {
		fmt.Printf("  ✓ L1 立即包含数据\n")
		time.Sleep(10 * time.Millisecond) // 等待异步操作
		if _, err2 := l2_async.Get(testKey2); err2 == nil {
			fmt.Printf("  ✓ L2 最终也包含数据\n")
		}
	}

	// 性能对比
	fmt.Printf("\n性能对比:\n")
	fmt.Printf("  - 写透模式: %v (同步，强一致性)\n", syncTime)
	fmt.Printf("  - 异步模式: %v (异步，高性能)\n", asyncTime)
	if asyncTime < syncTime {
		ratio := float64(syncTime) / float64(asyncTime)
		fmt.Printf("  - 异步模式快 %.1fx\n", ratio)
	}

	fmt.Println()
}

// 示例 3: L1 未命中 L2 命中的提升机制
func demonstratePromotion() {
	fmt.Println("3. L1 未命中 L2 命中的提升机制")
	fmt.Println("-----------------------------")

	// 创建一个很小的 L1 缓存
	l1 := cachex.NewLRUHandler(3) // 非常小的容量
	l2 := cachex.NewLRUHandler(20)
	defer l1.Close()
	defer l2.Close()

	twoLevel := cachex.NewTwoLevelHandler(l1, l2, true)
	defer twoLevel.Close()

	fmt.Printf("L1 容量: 3 个条目\n")
	fmt.Printf("L2 容量: 20 个条目\n\n")

	// 第一步：写入多个条目，超过 L1 容量
	fmt.Println("步骤1: 写入5个条目 (超过L1容量)")
	for i := 1; i <= 5; i++ {
		key := []byte(fmt.Sprintf("item_%d", i))
		value := []byte(fmt.Sprintf("数据_%d", i))
		err := twoLevel.Set(key, value)
		if err == nil {
			fmt.Printf("  ✓ 写入 item_%d\n", i)
		}
	}

	// 第二步：检查 L1 的状态 (应该只有最新的3个)
	fmt.Println("\n步骤2: 检查 L1 状态 (应该只保留最新的3个条目)")
	for i := 1; i <= 5; i++ {
		key := []byte(fmt.Sprintf("item_%d", i))
		if _, err := l1.Get(key); err == nil {
			fmt.Printf("  ✓ L1 包含 item_%d\n", i)
		} else {
			fmt.Printf("  - L1 不包含 item_%d (已被淘汰)\n", i)
		}
	}

	// 第三步：通过 TwoLevel 访问被 L1 淘汰的数据
	fmt.Println("\n步骤3: 访问被L1淘汰的数据 (应该从L2获取并提升到L1)")
	key := []byte("item_1") // 最早的数据，应该被L1淘汰但L2还有
	
	fmt.Printf("访问 item_1:\n")
	if data, err := twoLevel.Get(key); err == nil {
		fmt.Printf("  ✓ 成功获取数据: %s\n", string(data))
		fmt.Printf("  ✓ 数据来源: L2 (L1未命中，L2命中)\n")
		
		// 检查是否已提升到 L1
		if _, err := l1.Get(key); err == nil {
			fmt.Printf("  ✓ 数据已自动提升到 L1\n")
		} else {
			fmt.Printf("  - 数据未能提升到 L1\n")
		}
	} else {
		fmt.Printf("  ❌ 获取失败: %v\n", err)
	}

	// 第四步：再次访问，应该从 L1 命中
	fmt.Println("\n步骤4: 再次访问相同数据 (应该从L1命中)")
	if data, err := twoLevel.Get(key); err == nil {
		fmt.Printf("  ✓ 再次成功获取: %s\n", string(data))
		fmt.Printf("  ✓ 数据来源: L1 (缓存命中)\n")
	}

	fmt.Println("\n提升机制总结:")
	fmt.Println("  1. L1 容量不足时，旧数据被淘汰但L2仍保留")
	fmt.Println("  2. 访问L1中没有的数据时，自动从L2获取")
	fmt.Println("  3. 从L2获取的数据自动提升到L1，加速后续访问")
	fmt.Println("  4. 实现了热点数据的自动缓存层级优化")

	fmt.Println()
}

// 示例 4: TTL 处理和传播
func demonstrateTTLHandling() {
	fmt.Println("4. TTL 处理和传播")
	fmt.Println("----------------")

	// 使用支持TTL的缓存后端
	l1 := cachex.NewExpiringHandler(10 * time.Millisecond) // 10ms 清理间隔
	l2 := cachex.NewExpiringHandler(10 * time.Millisecond)
	defer l1.Close()
	defer l2.Close()

	twoLevel := cachex.NewTwoLevelHandler(l1, l2, true)
	defer twoLevel.Close()

	fmt.Println("使用支持TTL的 Expiring Cache 作为后端")

	// 1. 设置带 TTL 的数据
	fmt.Println("1) 设置带 TTL 的数据")
	err := twoLevel.SetWithTTL([]byte("session:abc123"), []byte("用户会话数据"), 200*time.Millisecond)
	if err == nil {
		fmt.Printf("  ✓ 设置会话数据，TTL: 200ms\n")

		// 检查两级缓存的TTL
		if ttl1, err1 := l1.GetTTL([]byte("session:abc123")); err1 == nil {
			fmt.Printf("  ✓ L1 TTL: %v\n", ttl1)
		}
		if ttl2, err2 := l2.GetTTL([]byte("session:abc123")); err2 == nil {
			fmt.Printf("  ✓ L2 TTL: %v\n", ttl2)
		}
	}

	// 2. 立即访问数据
	fmt.Println("\n2) 立即访问数据")
	if data, err := twoLevel.Get([]byte("session:abc123")); err == nil {
		fmt.Printf("  ✓ 成功获取: %s\n", string(data))
		
		if ttl, err := twoLevel.GetTTL([]byte("session:abc123")); err == nil {
			fmt.Printf("  ✓ 剩余 TTL: %v\n", ttl)
		}
	}

	// 3. 等待部分时间后再访问
	fmt.Println("\n3) 等待100ms后访问")
	time.Sleep(100 * time.Millisecond)
	
	if data, err := twoLevel.Get([]byte("session:abc123")); err == nil {
		fmt.Printf("  ✓ 仍可获取: %s\n", string(data))
		
		if ttl, err := twoLevel.GetTTL([]byte("session:abc123")); err == nil {
			fmt.Printf("  ✓ 剩余 TTL: %v\n", ttl)
		}
	} else {
		fmt.Printf("  - 数据已过期: %v\n", err)
	}

	// 4. 等待完全过期
	fmt.Println("\n4) 等待完全过期 (再等150ms)")
	time.Sleep(150 * time.Millisecond)
	
	if _, err := twoLevel.Get([]byte("session:abc123")); err != nil {
		fmt.Printf("  ✓ 数据已完全过期: %v\n", err)
	}

	// 5. 演示 TTL 提升
	fmt.Println("\n5) TTL 提升演示")
	fmt.Println("   先在L2中设置数据，然后通过TwoLevel访问")

	// 直接在L2中设置数据
	err = l2.SetWithTTL([]byte("promote_test"), []byte("提升测试数据"), 300*time.Millisecond)
	if err == nil {
		fmt.Printf("  ✓ 直接在 L2 设置数据，TTL: 300ms\n")
		
		// 等待一段时间
		time.Sleep(50 * time.Millisecond)
		
		// 通过 TwoLevel 访问，会提升到 L1
		if data, err := twoLevel.Get([]byte("promote_test")); err == nil {
			fmt.Printf("  ✓ 通过 TwoLevel 获取: %s\n", string(data))
			
			// 检查L1是否也有了数据和相应的TTL
			if ttl1, err1 := l1.GetTTL([]byte("promote_test")); err1 == nil {
				fmt.Printf("  ✓ 提升后 L1 TTL: %v\n", ttl1)
			}
		}
	}

	fmt.Println("\nTTL 处理总结:")
	fmt.Println("  1. 设置带TTL的数据会同时传播到L1和L2")
	fmt.Println("  2. TTL在两级缓存中保持同步")
	fmt.Println("  3. 从L2提升数据到L1时，TTL也会一起传播")
	fmt.Println("  4. 过期的数据从两级缓存中自动清除")

	fmt.Println()
}

// 示例 5: 不同后端组合
func demonstrateBackendCombinations() {
	fmt.Println("5. 不同后端组合")
	fmt.Println("--------------")

	combinations := []struct {
		name string
		desc string
		l1Factory func() cachex.Handler
		l2Factory func() cachex.Handler
		useCase string
	}{
		{
			name: "LRU + LRU",
			desc: "两级都使用LRU缓存",
			l1Factory: func() cachex.Handler { return cachex.NewLRUHandler(5) },
			l2Factory: func() cachex.Handler { return cachex.NewLRUHandler(50) },
			useCase: "适用于纯内存缓存场景",
		},
		{
			name: "LRU + Expiring",
			desc: "L1使用LRU，L2使用自动过期缓存",
			l1Factory: func() cachex.Handler { return cachex.NewLRUHandler(5) },
			l2Factory: func() cachex.Handler { return cachex.NewExpiringHandler(10 * time.Millisecond) },
			useCase: "L1保证速度，L2自动清理过期数据",
		},
		{
			name: "Expiring + LRU",
			desc: "L1使用自动过期，L2使用LRU",
			l1Factory: func() cachex.Handler { return cachex.NewExpiringHandler(10 * time.Millisecond) },
			l2Factory: func() cachex.Handler { return cachex.NewLRUHandler(50) },
			useCase: "L1管理短期数据，L2长期存储",
		},
		{
			name: "LRU + Ristretto",
			desc: "L1使用LRU，L2使用高性能Ristretto",
			l1Factory: func() cachex.Handler { return cachex.NewLRUHandler(5) },
			l2Factory: func() cachex.Handler { 
				rist, _ := cachex.NewRistrettoHandler(&cachex.RistrettoConfig{
					NumCounters: 100,
					MaxCost: 10 << 20, // 10MB
					BufferItems: 6,
				})
				return rist
			},
			useCase: "L1提供快速访问，L2提供大容量高性能缓存",
		},
	}

	for i, combo := range combinations {
		fmt.Printf("%d) %s\n", i+1, combo.name)
		fmt.Printf("   描述: %s\n", combo.desc)
		fmt.Printf("   适用场景: %s\n", combo.useCase)

		// 创建缓存实例
		l1 := combo.l1Factory()
		l2 := combo.l2Factory()
		defer l1.Close()
		defer l2.Close()

		twoLevel := cachex.NewTwoLevelHandler(l1, l2, true)
		defer twoLevel.Close()

		// 简单的操作测试
		testKey := []byte(fmt.Sprintf("test_%d", i))
		testValue := []byte(fmt.Sprintf("测试数据_%d", i))

		// 写入测试
		err := twoLevel.Set(testKey, testValue)
		if err == nil {
			fmt.Printf("   ✓ 写入测试通过\n")
			
			// 读取测试
			if data, err := twoLevel.Get(testKey); err == nil && string(data) == string(testValue) {
				fmt.Printf("   ✓ 读取测试通过\n")
			} else {
				fmt.Printf("   ❌ 读取测试失败\n")
			}
		} else {
			fmt.Printf("   ❌ 写入测试失败: %v\n", err)
		}

		fmt.Println()
	}

	fmt.Println("后端组合选择建议:")
	fmt.Println("  • 高性能场景: LRU + Ristretto")
	fmt.Println("  • 内存敏感: Expiring + LRU")
	fmt.Println("  • 通用场景: LRU + LRU")
	fmt.Println("  • 自动清理: LRU + Expiring")

	fmt.Println()
}

// 示例 6: 性能比较
func demonstratePerformanceComparison() {
	fmt.Println("6. 性能比较")
	fmt.Println("----------")

	const testOperations = 5000
	fmt.Printf("测试操作数: %d\n\n", testOperations)

	// 单级缓存基准
	fmt.Println("1) 单级缓存基准测试")
	singleCache := cachex.NewLRUHandler(testOperations)
	defer singleCache.Close()

	start := time.Now()
	for i := 0; i < testOperations; i++ {
		key := []byte(fmt.Sprintf("single_%d", i))
		value := []byte(fmt.Sprintf("value_%d", i))
		singleCache.Set(key, value)
	}
	singleWriteTime := time.Since(start)

	start = time.Now()
	hits := 0
	for i := 0; i < testOperations; i++ {
		key := []byte(fmt.Sprintf("single_%d", i%2000)) // 80%命中率
		if _, err := singleCache.Get(key); err == nil {
			hits++
		}
	}
	singleReadTime := time.Since(start)

	fmt.Printf("   写入性能: %v (%d ops/sec)\n", 
		singleWriteTime, int(float64(testOperations)/singleWriteTime.Seconds()))
	fmt.Printf("   读取性能: %v (%d ops/sec, 命中率: %.1f%%)\n", 
		singleReadTime, int(float64(testOperations)/singleReadTime.Seconds()),
		float64(hits)/testOperations*100)

	// 两级缓存 - 写透模式
	fmt.Println("\n2) 两级缓存 - 写透模式")
	l1_wt := cachex.NewLRUHandler(500)
	l2_wt := cachex.NewLRUHandler(testOperations)
	defer l1_wt.Close()
	defer l2_wt.Close()

	twoLevelWT := cachex.NewTwoLevelHandler(l1_wt, l2_wt, true)
	defer twoLevelWT.Close()

	start = time.Now()
	for i := 0; i < testOperations; i++ {
		key := []byte(fmt.Sprintf("two_wt_%d", i))
		value := []byte(fmt.Sprintf("value_%d", i))
		twoLevelWT.Set(key, value)
	}
	twoWriteThroughTime := time.Since(start)

	start = time.Now()
	l1Hits, l2Hits := 0, 0
	for i := 0; i < testOperations; i++ {
		key := []byte(fmt.Sprintf("two_wt_%d", i%2000))
		if _, err := twoLevelWT.Get(key); err == nil {
			// 简单检查是否为L1命中（最近访问的更可能在L1）
			if i%2000 >= testOperations-500 {
				l1Hits++
			} else {
				l2Hits++
			}
		}
	}
	twoReadThroughTime := time.Since(start)

	fmt.Printf("   写入性能: %v (%d ops/sec)\n", 
		twoWriteThroughTime, int(float64(testOperations)/twoWriteThroughTime.Seconds()))
	fmt.Printf("   读取性能: %v (%d ops/sec, L1命中约:%d, L2命中约:%d)\n", 
		twoReadThroughTime, int(float64(testOperations)/twoReadThroughTime.Seconds()),
		l1Hits, l2Hits)

	// 两级缓存 - 异步模式
	fmt.Println("\n3) 两级缓存 - 异步模式")
	l1_async := cachex.NewLRUHandler(500)
	l2_async := cachex.NewLRUHandler(testOperations)
	defer l1_async.Close()
	defer l2_async.Close()

	twoLevelAsync := cachex.NewTwoLevelHandler(l1_async, l2_async, false)
	defer twoLevelAsync.Close()

	start = time.Now()
	for i := 0; i < testOperations; i++ {
		key := []byte(fmt.Sprintf("two_async_%d", i))
		value := []byte(fmt.Sprintf("value_%d", i))
		twoLevelAsync.Set(key, value)
	}
	twoWriteAsyncTime := time.Since(start)

	// 等待异步写入完成
	time.Sleep(50 * time.Millisecond)

	start = time.Now()
	asyncHits := 0
	for i := 0; i < testOperations; i++ {
		key := []byte(fmt.Sprintf("two_async_%d", i%2000))
		if _, err := twoLevelAsync.Get(key); err == nil {
			asyncHits++
		}
	}
	twoReadAsyncTime := time.Since(start)

	fmt.Printf("   写入性能: %v (%d ops/sec)\n", 
		twoWriteAsyncTime, int(float64(testOperations)/twoWriteAsyncTime.Seconds()))
	fmt.Printf("   读取性能: %v (%d ops/sec, 命中率: %.1f%%)\n", 
		twoReadAsyncTime, int(float64(testOperations)/twoReadAsyncTime.Seconds()),
		float64(asyncHits)/testOperations*100)

	// 性能总结
	fmt.Println("\n性能总结:")
	fmt.Printf("  %-15s %-15s %-15s\n", "模式", "写入性能", "读取性能")
	fmt.Printf("  %-15s %-15s %-15s\n", "----", "--------", "--------")
	fmt.Printf("  %-15s %-15v %-15v\n", "单级缓存", singleWriteTime, singleReadTime)
	fmt.Printf("  %-15s %-15v %-15v\n", "两级-写透", twoWriteThroughTime, twoReadThroughTime)
	fmt.Printf("  %-15s %-15v %-15v\n", "两级-异步", twoWriteAsyncTime, twoReadAsyncTime)

	fmt.Println("\n建议:")
	if twoWriteAsyncTime < singleWriteTime {
		fmt.Println("  ✓ 异步模式写入性能优于单级缓存")
	}
	if twoWriteThroughTime > singleWriteTime {
		ratio := float64(twoWriteThroughTime) / float64(singleWriteTime)
		fmt.Printf("  • 写透模式比单级缓存慢 %.1fx (交换一致性)\n", ratio)
	}
	fmt.Println("  • 对于读密集应用，两级缓存能提供更好的命中率")
	fmt.Println("  • 对于写密集应用，建议使用异步模式")

	fmt.Println()
}

