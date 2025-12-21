package main

import (
	"context"
	"fmt"
	"time"

	"github.com/kamalyes/go-cachex"
)

func main() {
	fmt.Println("=== Handler 接口统一性测试 ===")

	// 测试所有Handler实现都有BatchGet和Stats方法
	handlers := []struct {
		name    string
		handler cachex.Handler
		err     error
	}{}

	// LRU Handler
	handlers = append(handlers, struct {
		name    string
		handler cachex.Handler
		err     error
	}{"LRU", cachex.NewLRUHandler(100), nil})

	// LRU Optimized Handler
	handlers = append(handlers, struct {
		name    string
		handler cachex.Handler
		err     error
	}{"LRU_Optimized", cachex.NewLRUOptimizedHandler(100), nil})

	// Ristretto Handler
	ristrettoHandler, ristrettoErr := cachex.NewRistrettoHandler(&cachex.RistrettoConfig{
		NumCounters: 1000,
		MaxCost:     1000,
		BufferItems: 64,
	})
	handlers = append(handlers, struct {
		name    string
		handler cachex.Handler
		err     error
	}{"Ristretto", ristrettoHandler, ristrettoErr})

	// TwoLevel Handler
	handlers = append(handlers, struct {
		name    string
		handler cachex.Handler
		err     error
	}{"TwoLevel", cachex.NewTwoLevelHandler(
		cachex.NewLRUHandler(50),
		cachex.NewLRUHandler(100),
		false, // writeThrough
	), nil})

	// Sharded Handler
	handlers = append(handlers, struct {
		name    string
		handler cachex.Handler
		err     error
	}{"Sharded", cachex.NewShardedHandler(func() cachex.Handler {
		return cachex.NewLRUHandler(25)
	}, 4), nil})

	// Expiring Handler
	handlers = append(handlers, struct {
		name    string
		handler cachex.Handler
		err     error
	}{"Expiring", cachex.NewExpiringHandler(time.Minute), nil})

	for _, h := range handlers {
		fmt.Printf("\n--- %s Handler 测试 ---\n", h.name)

		// 检查创建是否成功
		if h.err != nil {
			fmt.Printf("❌ %s Handler creation failed: %v\n", h.name, h.err)
			continue
		}
		if h.handler == nil {
			fmt.Printf("❌ %s Handler is nil\n", h.name)
			continue
		}

		// 测试基本操作
		testData := map[string]string{
			"key1": "value1",
			"key2": "value2",
			"key3": "value3",
		}

		// 设置测试数据 - 使用简化版API
		for k, v := range testData {
			err := h.handler.Set([]byte(k), []byte(v))
			if err != nil {
				fmt.Printf("❌ Set failed: %v\n", err)
				continue
			}
		}

		// 测试WithCtx版本
		ctx := context.Background()
		err := h.handler.SetWithCtx(ctx, []byte("ctx_key"), []byte("ctx_value"))
		if err == nil {
			fmt.Printf("WithCtx API: ✅\n")
		}

		// 测试BatchGet
		keys := [][]byte{[]byte("key1"), []byte("key2"), []byte("key3"), []byte("nonexistent")}
		results, errors := h.handler.BatchGet(keys)

		fmt.Printf("BatchGet 结果:\n")
		for i, key := range keys {
			if errors[i] == nil {
				fmt.Printf("  %s: %s ✅\n", string(key), string(results[i]))
			} else {
				fmt.Printf("  %s: %v ❌\n", string(key), errors[i])
			}
		}

		// 测试Stats
		stats := h.handler.Stats()
		fmt.Printf("Stats 结果:\n")
		for k, v := range stats {
			fmt.Printf("  %s: %v\n", k, v)
		}

		// 清理
		h.handler.Close()
	}

	fmt.Println("\n=== ContextHandler 接口测试 ===")

	// 测试Client (实现ContextHandler)
	ctx := context.Background()
	client, err := cachex.NewClient(ctx, &cachex.ClientConfig{
		Type:     cachex.CacheLRU,
		Capacity: 100,
	})
	if err != nil {
		fmt.Printf("❌ Client creation failed: %v\n", err)
		return
	}
	defer client.Close()

	// 设置测试数据
	testData := map[string]string{
		"client_key1": "client_value1",
		"client_key2": "client_value2",
		"client_key3": "client_value3",
	}

	for k, v := range testData {
		err := client.Set([]byte(k), []byte(v))
		if err != nil {
			fmt.Printf("❌ Client Set failed: %v\n", err)
		}
	}

	// 测试Client BatchGet
	keys := [][]byte{[]byte("client_key1"), []byte("client_key2"), []byte("client_key3")}
	results, errors := client.BatchGet(keys)

	fmt.Printf("Client BatchGet 结果:\n")
	for i, key := range keys {
		if errors[i] == nil {
			fmt.Printf("  %s: %s ✅\n", string(key), string(results[i]))
		} else {
			fmt.Printf("  %s: %v ❌\n", string(key), errors[i])
		}
	}

	// 测试Client Stats
	stats := client.Stats()
	fmt.Printf("Client Stats 结果:\n")
	for k, v := range stats {
		fmt.Printf("  %s: %v\n", k, v)
	}

	fmt.Println("\n✅ 所有接口统一性测试完成！")
}
