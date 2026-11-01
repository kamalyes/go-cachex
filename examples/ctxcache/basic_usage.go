/*
 * @Description: CtxCache 基础使用示例
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
	fmt.Println("=== CtxCache 基础使用示例 ===")

	// 示例 1: 基本的 CRUD 操作
	demonstrateBasicOperations()

	// 示例 2: 上下文支持
	demonstrateContextSupport()

	// 示例 3: GetOrCompute 功能
	demonstrateGetOrCompute()

	// 示例 4: 上下文传递缓存
	demonstrateContextPassing()
}

// 示例 1: 基本的 CRUD 操作
func demonstrateBasicOperations() {
	fmt.Println("1. 基本 CRUD 操作")
	fmt.Println("------------------")

	// 创建底层 Ristretto 缓存
	ristrettoHandler, err := cachex.NewDefaultRistrettoHandler()
	if err != nil {
		log.Fatal("创建 Ristretto 处理器失败:", err)
	}
	defer ristrettoHandler.Close()

	// 创建 CtxCache
	cache := cachex.NewCtxCache(ristrettoHandler)
	defer cache.Close()

	ctx := context.Background()

	// Set 操作
	err = cache.Set(ctx, []byte("user:123"), []byte(`{"name":"Alice","age":30}`))
	if err != nil {
		log.Fatal("设置缓存失败:", err)
	}
	fmt.Println("✓ 设置用户信息")

	// Get 操作
	value, err := cache.Get(ctx, []byte("user:123"))
	if err != nil {
		log.Fatal("获取缓存失败:", err)
	}
	fmt.Printf("✓ 获取用户信息: %s\n", string(value))

	// SetWithTTL 操作
	err = cache.SetWithTTL(ctx, []byte("session:abc"), []byte("session_data"), 2*time.Second)
	if err != nil {
		log.Fatal("设置带TTL缓存失败:", err)
	}
	fmt.Println("✓ 设置会话信息 (TTL: 2秒)")

	// 立即获取
	value, err = cache.Get(ctx, []byte("session:abc"))
	if err != nil {
		fmt.Printf("✗ 获取会话信息失败: %v\n", err)
	} else {
		fmt.Printf("✓ 获取会话信息: %s\n", string(value))
	}

	// 等待TTL过期
	time.Sleep(2500 * time.Millisecond)
	_, err = cache.Get(ctx, []byte("session:abc"))
	if err != nil {
		fmt.Printf("✓ 会话已过期: %v\n", err)
	}

	// Del 操作
	err = cache.Del(ctx, []byte("user:123"))
	if err != nil {
		log.Fatal("删除缓存失败:", err)
	}
	fmt.Println("✓ 删除用户信息")

	_, err = cache.Get(ctx, []byte("user:123"))
	if err != nil {
		fmt.Printf("✓ 用户信息已删除: %v\n", err)
	}

	fmt.Println()
}

// 示例 2: 上下文支持
func demonstrateContextSupport() {
	fmt.Println("2. 上下文支持")
	fmt.Println("--------------")

	// 创建缓存
	lruHandler := cachex.NewLRUHandler(100)
	cache := cachex.NewCtxCache(lruHandler)
	defer cache.Close()

	// 带超时的上下文
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// 正常操作（应该成功）
	err := cache.Set(ctx, []byte("fast_key"), []byte("fast_value"))
	if err != nil {
		fmt.Printf("✗ 快速操作失败: %v\n", err)
	} else {
		fmt.Println("✓ 快速操作成功")
	}

	// 等待超时
	time.Sleep(150 * time.Millisecond)

	// 此时上下文已超时
	err = cache.Set(ctx, []byte("slow_key"), []byte("slow_value"))
	if err != nil {
		fmt.Printf("✓ 超时操作被取消: %v\n", err)
	}

	// 使用新的上下文
	newCtx := context.Background()
	err = cache.Set(newCtx, []byte("new_key"), []byte("new_value"))
	if err != nil {
		fmt.Printf("✗ 新上下文操作失败: %v\n", err)
	} else {
		fmt.Println("✓ 新上下文操作成功")
	}

	fmt.Println()
}

// 示例 3: GetOrCompute 功能
func demonstrateGetOrCompute() {
	fmt.Println("3. GetOrCompute 功能")
	fmt.Println("-------------------")

	// 创建缓存
	expiringHandler := cachex.NewExpiringHandler(100 * time.Millisecond)
	cache := cachex.NewCtxCache(expiringHandler)
	defer cache.Close()

	ctx := context.Background()

	// 模拟数据库查询或 API 调用
	userLoader := func(ctx context.Context) ([]byte, error) {
		fmt.Println("  🔄 执行昂贵的数据库查询...")
		time.Sleep(500 * time.Millisecond) // 模拟慢查询

		// 检查上下文是否被取消
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		return []byte(`{"id":456,"name":"Bob","email":"bob@example.com"}`), nil
	}

	// 第一次调用 - 会执行 loader
	fmt.Println("第一次调用 GetOrCompute:")
	start := time.Now()
	value, err := cache.GetOrCompute(ctx, []byte("user:456"), time.Second, userLoader)
	if err != nil {
		log.Fatal("GetOrCompute 失败:", err)
	}
	fmt.Printf("✓ 获取用户数据: %s (耗时: %v)\n", string(value), time.Since(start))

	// 第二次调用 - 直接从缓存获取
	fmt.Println("\n第二次调用 GetOrCompute:")
	start = time.Now()
	value, err = cache.GetOrCompute(ctx, []byte("user:456"), time.Second, userLoader)
	if err != nil {
		log.Fatal("GetOrCompute 失败:", err)
	}
	fmt.Printf("✓ 从缓存获取: %s (耗时: %v)\n", string(value), time.Since(start))

	// 测试上下文取消
	fmt.Println("\n测试上下文取消:")
	cancelCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = cache.GetOrCompute(cancelCtx, []byte("user:789"), 0, userLoader)
	if err != nil {
		fmt.Printf("✓ 上下文取消成功: %v\n", err)
	}

	fmt.Println()
}

// 示例 4: 上下文传递缓存
func demonstrateContextPassing() {
	fmt.Println("4. 上下文传递缓存")
	fmt.Println("------------------")

	// 创建缓存
	ristrettoHandler, err := cachex.NewDefaultRistrettoHandler()
	if err != nil {
		log.Fatal("创建 Ristretto 处理器失败:", err)
	}
	defer ristrettoHandler.Close()

	cache := cachex.NewCtxCache(ristrettoHandler)
	defer cache.Close()

	// 将缓存放入上下文
	ctx := cachex.WithCache(context.Background(), cache)

	// 模拟业务函数
	processUser := func(ctx context.Context, userID string) error {
		// 从上下文获取缓存
		c := cachex.FromContext(ctx)
		if c == nil {
			return fmt.Errorf("缓存未找到")
		}

		key := []byte("processed:" + userID)

		// 检查是否已处理
		if value, err := c.Get(ctx, key); err == nil {
			fmt.Printf("✓ 用户 %s 已处理: %s\n", userID, string(value))
			return nil
		}

		// 模拟处理逻辑
		fmt.Printf("🔄 正在处理用户 %s...\n", userID)
		time.Sleep(200 * time.Millisecond)

		// 缓存处理结果
		result := fmt.Sprintf("processed_at_%d", time.Now().Unix())
		err := c.SetWithTTL(ctx, key, []byte(result), 5*time.Second)
		if err != nil {
			return fmt.Errorf("缓存处理结果失败: %v", err)
		}

		fmt.Printf("✓ 用户 %s 处理完成: %s\n", userID, result)
		return nil
	}

	// 处理多个用户
	users := []string{"user1", "user2", "user3", "user1", "user2"}

	fmt.Println("处理用户列表:")
	for _, userID := range users {
		if err := processUser(ctx, userID); err != nil {
			fmt.Printf("✗ 处理用户 %s 失败: %v\n", userID, err)
		}
	}

	// 验证上下文中没有缓存的情况
	fmt.Println("\n测试没有缓存的上下文:")
	emptyCtx := context.Background()
	if err := processUser(emptyCtx, "user4"); err != nil {
		fmt.Printf("✓ 预期错误: %v\n", err)
	}

	fmt.Println("\n上下文传递示例完成")
}
