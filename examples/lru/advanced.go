/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-09 21:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 21:00:00
 * @FilePath: \go-cachex\examples\lru\advanced_usage.go
 * @Description: LRU Cache Advanced Usage Example
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kamalyes/go-cachex"
)

// User represents a user entity
type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// UserCache wraps LRU cache for user data
type UserCache struct {
	cache cachex.Handler
}

func NewUserCache(capacity int) *UserCache {
	return &UserCache{
		cache: cachex.NewLRUHandler(capacity),
	}
}

func (uc *UserCache) SetUser(user *User, ttl time.Duration) error {
	key := []byte(fmt.Sprintf("user:%d", user.ID))
	data, err := json.Marshal(user)
	if err != nil {
		return err
	}
	
	if ttl > 0 {
		return uc.cache.SetWithTTL(key, data, ttl)
	}
	return uc.cache.Set(key, data)
}

func (uc *UserCache) GetUser(id int) (*User, error) {
	key := []byte(fmt.Sprintf("user:%d", id))
	data, err := uc.cache.Get(key)
	if err != nil {
		return nil, err
	}
	
	var user User
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (uc *UserCache) DeleteUser(id int) error {
	key := []byte(fmt.Sprintf("user:%d", id))
	return uc.cache.Del(key)
}

func (uc *UserCache) Close() error {
	return uc.cache.Close()
}

// 模拟数据库操作
func loadUserFromDB(id int) (*User, error) {
	// 模拟数据库延迟
	time.Sleep(100 * time.Millisecond)
	
	users := map[int]*User{
		1: {ID: 1, Name: "Alice", Email: "alice@example.com"},
		2: {ID: 2, Name: "Bob", Email: "bob@example.com"},
		3: {ID: 3, Name: "Charlie", Email: "charlie@example.com"},
		4: {ID: 4, Name: "David", Email: "david@example.com"},
		5: {ID: 5, Name: "Eve", Email: "eve@example.com"},
	}
	
	if user, exists := users[id]; exists {
		return user, nil
	}
	return nil, fmt.Errorf("user %d not found", id)
}

func advancedUsageExample() {
	fmt.Println("=== LRU Cache Advanced Usage Example ===")

	// 创建用户缓存
	userCache := NewUserCache(3)
	defer userCache.Close()

	// 示例 1: 基本缓存模式
	fmt.Println("\n1. Basic Caching Pattern:")
	
	// 第一次访问 - 从数据库加载
	start := time.Now()
	user, err := getUserWithCache(userCache, 1)
	if err != nil {
		log.Fatal(err)
	}
	duration1 := time.Since(start)
	fmt.Printf("First access: %+v (took: %v)\n", user, duration1)
	
	// 第二次访问 - 从缓存获取
	start = time.Now()
	user, err = getUserWithCache(userCache, 1)
	if err != nil {
		log.Fatal(err)
	}
	duration2 := time.Since(start)
	fmt.Printf("Second access: %+v (took: %v)\n", user, duration2)
	
	// 防止除零错误
	if duration2 > 0 {
		fmt.Printf("Cache speedup: %v\n", duration1/duration2)
	} else {
		fmt.Printf("Cache speedup: instant (too fast to measure)\n")
	}

	// 示例 2: TTL 和缓存失效
	fmt.Println("\n2. TTL and Cache Expiration:")
	
	// 设置带 TTL 的用户数据
	user2 := &User{ID: 2, Name: "Bob Updated", Email: "bob.new@example.com"}
	if err := userCache.SetUser(user2, 2*time.Second); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Set user with 2s TTL")
	
	// 立即获取
	if user, err := userCache.GetUser(2); err == nil {
		fmt.Printf("Immediate get: %+v\n", user)
	}
	
	// 等待过期
	time.Sleep(3 * time.Second)
	if _, err := userCache.GetUser(2); err != nil {
		fmt.Printf("After expiry: %v\n", err)
	}

	// 示例 3: LRU 驱逐行为
	fmt.Println("\n3. LRU Eviction Behavior:")
	
	// 填充缓存到容量
	for i := 1; i <= 3; i++ {
		user, _ := loadUserFromDB(i)
		userCache.SetUser(user, 0) // 无 TTL
		fmt.Printf("Added user %d to cache\n", i)
	}
	
	// 访问用户 1，使其成为最近使用的
	userCache.GetUser(1)
	fmt.Println("Accessed user 1 (making it most recently used)")
	
	// 添加新用户，应该驱逐用户 2
	user4, _ := loadUserFromDB(4)
	userCache.SetUser(user4, 0)
	fmt.Println("Added user 4 (should evict user 2)")
	
	// 验证用户 2 被驱逐
	if _, err := userCache.GetUser(2); err != nil {
		fmt.Printf("User 2 evicted: %v\n", err)
	}
	
	// 验证其他用户仍在缓存中
	for _, id := range []int{1, 3, 4} {
		if user, err := userCache.GetUser(id); err == nil {
			fmt.Printf("User %d still in cache: %s\n", id, user.Name)
		}
	}

	// 示例 4: 并发访问性能
	fmt.Println("\n4. Concurrent Access Performance:")
	
	concurrentCacheTest(userCache)

	// 示例 5: 预热缓存
	fmt.Println("\n5. Cache Warming:")
	
	newCache := NewUserCache(10)
	defer newCache.Close()
	
	// 预热策略：预加载常用数据
	fmt.Println("Warming up cache with frequently accessed users...")
	frequentUsers := []int{1, 2, 3}
	for _, id := range frequentUsers {
		user, err := loadUserFromDB(id)
		if err == nil {
			newCache.SetUser(user, 10*time.Minute) // 10分钟 TTL
			fmt.Printf("Warmed up user %d\n", id)
		}
	}

	fmt.Println("\n=== Advanced Example completed successfully ===")
}

// getUserWithCache implements cache-aside pattern
func getUserWithCache(cache *UserCache, id int) (*User, error) {
	// 尝试从缓存获取
	user, err := cache.GetUser(id)
	if err == nil {
		return user, nil
	}
	
	// 缓存未命中，从数据库加载
	if err == cachex.ErrNotFound {
		user, err := loadUserFromDB(id)
		if err != nil {
			return nil, err
		}
		
		// 存储到缓存
		cache.SetUser(user, 5*time.Minute)
		return user, nil
	}
	
	return nil, err
}

// 并发访问测试
func concurrentCacheTest(cache *UserCache) {
	const numGoroutines = 10
	const numRequests = 100
	
	var wg sync.WaitGroup
	start := time.Now()
	
	// 启动多个 goroutine 进行并发访问
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			
			for j := 0; j < numRequests; j++ {
				userID := (j % 5) + 1 // 访问用户 1-5
				getUserWithCache(cache, userID)
			}
		}(i)
	}
	
	wg.Wait()
	duration := time.Since(start)
	totalRequests := numGoroutines * numRequests
	
	fmt.Printf("Concurrent test completed:\n")
	fmt.Printf("  - Goroutines: %d\n", numGoroutines)
	fmt.Printf("  - Total requests: %d\n", totalRequests)
	fmt.Printf("  - Total time: %v\n", duration)
	
	// 防止除零错误
	if duration.Seconds() > 0 {
		fmt.Printf("  - Requests/second: %.2f\n", float64(totalRequests)/duration.Seconds())
	} else {
		fmt.Printf("  - Requests/second: instant (too fast to measure)\n")
	}
}