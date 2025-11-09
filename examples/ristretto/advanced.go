/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 21:15:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 21:17:57
 * @FilePath: \go-cachex\examples\ristretto\advanced.go
 * @Description: Ristretto Cache Advanced Usage Example
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"github.com/kamalyes/go-cachex"
)

// Product represents a product entity
type Product struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	Category    string  `json:"category"`
	InStock     bool    `json:"in_stock"`
}

// ProductCache wraps Ristretto cache for product data
type ProductCache struct {
	cache cachex.Handler
}

func NewProductCache(config *cachex.RistrettoConfig) *ProductCache {
	cache, err := cachex.NewRistrettoHandler(config)
	if err != nil {
		log.Fatal("Failed to create product cache:", err)
	}
	
	return &ProductCache{cache: cache}
}

func (pc *ProductCache) SetProduct(product *Product, ttl time.Duration) error {
	key := []byte(fmt.Sprintf("product:%d", product.ID))
	data, err := json.Marshal(product)
	if err != nil {
		return err
	}
	
	if ttl > 0 {
		return pc.cache.SetWithTTL(key, data, ttl)
	}
	return pc.cache.Set(key, data)
}

func (pc *ProductCache) GetProduct(id int) (*Product, error) {
	key := []byte(fmt.Sprintf("product:%d", id))
	data, err := pc.cache.Get(key)
	if err != nil {
		return nil, err
	}
	
	var product Product
	if err := json.Unmarshal(data, &product); err != nil {
		return nil, err
	}
	return &product, nil
}

func (pc *ProductCache) SetCategoryIndex(category string, productIDs []int) error {
	key := []byte(fmt.Sprintf("category:%s", category))
	data, err := json.Marshal(productIDs)
	if err != nil {
		return err
	}
	return pc.cache.SetWithTTL(key, data, 10*time.Minute)
}

func (pc *ProductCache) GetCategoryProducts(category string) ([]int, error) {
	key := []byte(fmt.Sprintf("category:%s", category))
	data, err := pc.cache.Get(key)
	if err != nil {
		return nil, err
	}
	
	var productIDs []int
	if err := json.Unmarshal(data, &productIDs); err != nil {
		return nil, err
	}
	return productIDs, nil
}

func (pc *ProductCache) Close() error {
	return pc.cache.Close()
}

// 模拟数据库操作
func generateSampleProduct(id int) *Product {
	categories := []string{"Electronics", "Clothing", "Books", "Home", "Sports"}
	names := []string{"Laptop", "Shirt", "Novel", "Chair", "Basketball"}
	
	return &Product{
		ID:          id,
		Name:        fmt.Sprintf("%s #%d", names[id%len(names)], id),
		Description: fmt.Sprintf("High quality %s with excellent features", names[id%len(names)]),
		Price:       float64(rand.Intn(1000)) + 9.99,
		Category:    categories[id%len(categories)],
		InStock:     rand.Float32() > 0.1, // 90% chance in stock
	}
}

func advancedUsageExample() {
	fmt.Println("=== Ristretto Cache Advanced Usage Example ===")

	// 自定义配置的 Ristretto 缓存
	fmt.Println("\n1. Creating Custom Ristretto Cache:")
	config := &cachex.RistrettoConfig{
		NumCounters: 1e6,     // 1 million counters for frequency estimation
		MaxCost:     1 << 30, // 1GB max memory usage
		BufferItems: 64,      // Write buffer size
	}
	
	productCache := NewProductCache(config)
	defer productCache.Close()
	
	fmt.Printf("Cache created with config: NumCounters=%d, MaxCost=%d bytes, BufferItems=%d\n", 
		config.NumCounters, config.MaxCost, config.BufferItems)

	// 示例 1: 高性能批量操作
	fmt.Println("\n2. High-Performance Bulk Operations:")
	
	const numProducts = 10000
	fmt.Printf("Generating and caching %d products...\n", numProducts)
	
	start := time.Now()
	for i := 1; i <= numProducts; i++ {
		product := generateSampleProduct(i)
		if err := productCache.SetProduct(product, 30*time.Minute); err != nil {
			fmt.Printf("Failed to cache product %d: %v\n", i, err)
		}
	}
	duration := time.Since(start)
	
	fmt.Printf("Cached %d products in %v (%.2f products/second)\n", 
		numProducts, duration, float64(numProducts)/duration.Seconds())

	// 示例 2: 并发读取性能测试
	fmt.Println("\n3. Concurrent Read Performance Test:")
	
	const numGoroutines = 100
	const readsPerGoroutine = 1000
	totalReads := numGoroutines * readsPerGoroutine
	
	var wg sync.WaitGroup
	var successfulReads int64
	var mu sync.Mutex
	
	start = time.Now()
	
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			localSuccessful := 0
			
			for j := 0; j < readsPerGoroutine; j++ {
				productID := rand.Intn(numProducts) + 1
				if _, err := productCache.GetProduct(productID); err == nil {
					localSuccessful++
				}
			}
			
			mu.Lock()
			successfulReads += int64(localSuccessful)
			mu.Unlock()
		}(i)
	}
	
	wg.Wait()
	duration = time.Since(start)
	
	fmt.Printf("Concurrent reads completed:\n")
	fmt.Printf("  - Goroutines: %d\n", numGoroutines)
	fmt.Printf("  - Total reads: %d\n", totalReads)
	fmt.Printf("  - Successful reads: %d\n", successfulReads)
	fmt.Printf("  - Hit rate: %.2f%%\n", float64(successfulReads)/float64(totalReads)*100)
	fmt.Printf("  - Duration: %v\n", duration)
	fmt.Printf("  - Reads/second: %.2f\n", float64(totalReads)/duration.Seconds())

	// 示例 3: 分类索引缓存
	fmt.Println("\n4. Category Index Caching:")
	
	// 构建分类索引
	categories := map[string][]int{
		"Electronics": {},
		"Clothing":    {},
		"Books":       {},
		"Home":        {},
		"Sports":      {},
	}
	
	// 从缓存中读取产品并构建分类索引
	for i := 1; i <= 1000; i++ { // 只处理前1000个产品
		if product, err := productCache.GetProduct(i); err == nil {
			categories[product.Category] = append(categories[product.Category], product.ID)
		}
	}
	
	// 缓存分类索引
	for category, productIDs := range categories {
		if err := productCache.SetCategoryIndex(category, productIDs); err != nil {
			fmt.Printf("Failed to cache category %s: %v\n", category, err)
		} else {
			fmt.Printf("Cached category '%s' with %d products\n", category, len(productIDs))
		}
	}
	
	// 验证分类索引
	fmt.Println("\nVerifying category indexes:")
	for category := range categories {
		if productIDs, err := productCache.GetCategoryProducts(category); err == nil {
			fmt.Printf("  %s: %d products\n", category, len(productIDs))
			
			// 显示该分类的前5个产品
			if len(productIDs) > 0 {
				fmt.Printf("    Sample products: ")
				for i, id := range productIDs {
					if i >= 5 {
						break
					}
					if i > 0 {
						fmt.Print(", ")
					}
					fmt.Printf("#%d", id)
				}
				fmt.Println()
			}
		}
	}

	// 示例 4: 内存使用和性能监控
	fmt.Println("\n5. Memory Usage and Performance Monitoring:")
	
	var m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m1)
	
	// 添加更多大对象到缓存
	fmt.Println("Adding large objects to cache...")
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("large:object:%d", i))
		// 创建 10KB 的大对象
		largeData := make([]byte, 10*1024)
		for j := range largeData {
			largeData[j] = byte(i % 256)
		}
		productCache.cache.Set(key, largeData)
	}
	
	runtime.ReadMemStats(&m2)
	fmt.Printf("Memory usage increased by: %.2f MB\n", 
		float64(m2.Alloc-m1.Alloc)/1024/1024)

	// 示例 5: 缓存热点数据访问模式
	fmt.Println("\n6. Hot Data Access Pattern Simulation:")
	
	// 模拟80/20规则：80%的访问集中在20%的数据上
	hotProducts := make([]int, 2000) // 20% of 10000
	for i := range hotProducts {
		hotProducts[i] = i + 1
	}
	
	const numHotAccess = 50000
	var hotHits int
	
	start = time.Now()
	for i := 0; i < numHotAccess; i++ {
		var productID int
		if rand.Float32() < 0.8 { // 80% access hot data
			productID = hotProducts[rand.Intn(len(hotProducts))]
		} else { // 20% access cold data
			productID = rand.Intn(8000) + 2001 // cold data range
		}
		
		if _, err := productCache.GetProduct(productID); err == nil {
			hotHits++
		}
	}
	duration = time.Since(start)
	
	fmt.Printf("Hot data access simulation:\n")
	fmt.Printf("  - Total accesses: %d\n", numHotAccess)
	fmt.Printf("  - Cache hits: %d\n", hotHits)
	fmt.Printf("  - Hit rate: %.2f%%\n", float64(hotHits)/float64(numHotAccess)*100)
	fmt.Printf("  - Duration: %v\n", duration)
	fmt.Printf("  - Accesses/second: %.2f\n", float64(numHotAccess)/duration.Seconds())

	// 示例 6: 清理和统计
	fmt.Println("\n7. Cleanup and Statistics:")
	
	// 删除一些测试数据
	fmt.Println("Cleaning up test data...")
	for i := 1; i <= 100; i++ {
		key := []byte(fmt.Sprintf("large:object:%d", i))
		productCache.cache.Del(key)
	}
	
	runtime.GC() // 强制垃圾回收
	fmt.Println("Cleanup completed")

	fmt.Println("\n=== Advanced Example completed successfully ===")
}