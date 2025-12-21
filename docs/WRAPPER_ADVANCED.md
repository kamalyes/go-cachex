# CacheWrapper 高级使用指南

本文档介绍 Go-Cachex CacheWrapper 的高级特性和使用技巧

## 📚 目录

- [核心概念](#核心概念)
- [基本用法](#基本用法)
- [高级特性](#高级特性)
- [最佳实践](#最佳实践)
- [性能优化](#性能优化)

## 核心概念

CacheWrapper 是一个泛型缓存包装器，提供以下特性：

- **泛型支持**: 支持任意类型 `CacheWrapper[T]`
- **延迟双删**: 确保缓存一致性
- **自动压缩**: Zlib压缩减少内存占用
- **优雅降级**: 错误时自动回源
- **并发安全**: 支持高并发访问

## 基本用法

### 简单示例

```go
package main

import (
    "context"
    "time"
    
    "github.com/redis/go-redis/v9"
    "github.com/kamalyes/go-cachex"
)

type User struct {
    ID   int    `json:"id"`
    Name string `json:"name"`
    Age  int    `json:"age"`
}

func main() {
    // 创建Redis客户端
    client := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })
    defer client.Close()

    // 创建加载器函数
    loader := cachex.CacheWrapper(client, "user:123",
        func(ctx context.Context) (*User, error) {
            // 从数据库加载
            return loadUserFromDB(ctx, 123)
        },
        time.Hour, // TTL
    )

    ctx := context.Background()
    
    // 第一次调用 - 从数据库加载
    user, err := loader(ctx)
    if err != nil {
        panic(err)
    }
    
    // 第二次调用 - 从缓存获取
    user2, _ := loader(ctx)
}

func loadUserFromDB(ctx context.Context, id int) (*User, error) {
    // 模拟数据库查询
    return &User{ID: id, Name: "Alice", Age: 25}, nil
}
```

## 高级特性

### 1. 延迟双删策略

确保缓存与数据库的一致性：

```go
type Product struct {
    ID    int
    Name  string
    Price float64
}

// 创建带延迟双删的包装器
wrapper := cachex.CacheWrapper(redisClient, "product:100",
    func(ctx context.Context) (*Product, error) {
        return loadProductFromDB(ctx, 100)
    },
    time.Hour,
)

// 更新数据时的完整流程
func updateProduct(ctx context.Context, id int, newPrice float64) error {
    // 1. 第一次删除缓存
    redisClient.Del(ctx, fmt.Sprintf("product:%d", id))
    
    // 2. 更新数据库
    if err := updateProductInDB(ctx, id, newPrice); err != nil {
        return err
    }
    
    // 3. 延迟第二次删除（防止脏数据）
    time.Sleep(500 * time.Millisecond)
    redisClient.Del(ctx, fmt.Sprintf("product:%d", id))
    
    return nil
}
```

### 2. 自动数据压缩

CacheWrapper 自动使用 Zlib 压缩，减少 Redis 内存占用：

```go
type LargeObject struct {
    ID   int
    Data []byte // 大量数据
}

// 大对象自动压缩
largeLoader := cachex.CacheWrapper(redisClient, "large:1",
    func(ctx context.Context) (*LargeObject, error) {
        return &LargeObject{
            ID:   1,
            Data: make([]byte, 1024*1024), // 1MB数据
        }, nil
    },
    time.Hour,
)

// 压缩比例通常可达 60-80%
```

### 3. 错误处理和降级

```go
type Config struct {
    Feature string
    Enabled bool
}

// 带默认值的降级策略
configLoader := cachex.CacheWrapper(redisClient, "config:main",
    func(ctx context.Context) (*Config, error) {
        cfg, err := loadConfigFromDB(ctx)
        if err != nil {
            // 返回默认配置作为降级
            return &Config{
                Feature: "default",
                Enabled: true,
            }, nil
        }
        return cfg, nil
    },
    5*time.Minute,
)
```

### 4. 热数据预加载

```go
// 应用启动时预热缓存
func warmupCache(ctx context.Context) {
    hotUserIDs := []int{1, 2, 3, 100, 200}
    
    for _, id := range hotUserIDs {
        key := fmt.Sprintf("user:%d", id)
        loader := cachex.CacheWrapper(redisClient, key,
            func(ctx context.Context) (*User, error) {
                return loadUserFromDB(ctx, id)
            },
            time.Hour,
        )
        
        // 触发加载
        _, _ = loader(ctx)
    }
}
```

## 最佳实践

### 1. 合理设置 TTL

```go
// 不同数据类型使用不同TTL
var (
    // 热数据 - 短TTL，频繁更新
    hotDataTTL = 5 * time.Minute
    
    // 温数据 - 中等TTL
    warmDataTTL = 1 * time.Hour
    
    // 冷数据 - 长TTL
    coldDataTTL = 24 * time.Hour
    
    // 配置数据 - 较长TTL
    configTTL = 1 * time.Hour
)
```

### 2. 键命名规范

```go
// 使用命名空间防止冲突
const (
    UserCachePrefix    = "user:"
    ProductCachePrefix = "product:"
    OrderCachePrefix   = "order:"
)

func makeUserKey(id int) string {
    return fmt.Sprintf("%s%d", UserCachePrefix, id)
}

func makeProductKey(id int) string {
    return fmt.Sprintf("%s%d", ProductCachePrefix, id)
}
```

### 3. 并发控制

```go
// 高并发场景下的并发控制
type SafeLoader struct {
    loader func(context.Context) (*User, error)
    mu     sync.Mutex
}

func (s *SafeLoader) Load(ctx context.Context) (*User, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.loader(ctx)
}

// 但是 CacheWrapper 本身已经内置了 singleflight 去重
// 通常无需额外加锁
```

### 4. 监控和日志

```go
type MonitoredLoader struct {
    key    string
    loader func(context.Context) (*User, error)
}

func (m *MonitoredLoader) Load(ctx context.Context) (*User, error) {
    start := time.Now()
    user, err := m.loader(ctx)
    duration := time.Since(start)
    
    if err != nil {
        log.Printf("缓存加载失败: key=%s, duration=%v, error=%v", 
            m.key, duration, err)
    } else {
        log.Printf("缓存加载成功: key=%s, duration=%v", 
            m.key, duration)
    }
    
    return user, err
}
```

## 性能优化

### 1. 批量预加载

```go
// 批量预加载相关数据
func batchLoadUsers(ctx context.Context, ids []int) error {
    var wg sync.WaitGroup
    
    for _, id := range ids {
        wg.Add(1)
        go func(userID int) {
            defer wg.Done()
            
            loader := cachex.CacheWrapper(redisClient,
                makeUserKey(userID),
                func(ctx context.Context) (*User, error) {
                    return loadUserFromDB(ctx, userID)
                },
                time.Hour,
            )
            
            _, _ = loader(ctx)
        }(id)
    }
    
    wg.Wait()
    return nil
}
```

### 2. 分层缓存

```go
// 结合本地缓存和Redis缓存
var localCache = make(map[string]*User)
var localMu sync.RWMutex

func getUser(ctx context.Context, id int) (*User, error) {
    key := makeUserKey(id)
    
    // L1: 本地缓存
    localMu.RLock()
    if user, exists := localCache[key]; exists {
        localMu.RUnlock()
        return user, nil
    }
    localMu.RUnlock()
    
    // L2: Redis缓存
    loader := cachex.CacheWrapper(redisClient, key,
        func(ctx context.Context) (*User, error) {
            return loadUserFromDB(ctx, id)
        },
        time.Hour,
    )
    
    user, err := loader(ctx)
    if err != nil {
        return nil, err
    }
    
    // 写入本地缓存
    localMu.Lock()
    localCache[key] = user
    localMu.Unlock()
    
    return user, nil
}
```

### 3. 避免缓存穿透

```go
// 使用空对象模式避免缓存穿透
type NullableUser struct {
    User   *User
    IsNull bool
}

func getUserSafe(ctx context.Context, id int) (*User, error) {
    loader := cachex.CacheWrapper(redisClient,
        makeUserKey(id),
        func(ctx context.Context) (*NullableUser, error) {
            user, err := loadUserFromDB(ctx, id)
            if err == ErrUserNotFound {
                // 缓存空结果，短TTL
                return &NullableUser{IsNull: true}, nil
            }
            if err != nil {
                return nil, err
            }
            return &NullableUser{User: user, IsNull: false}, nil
        },
        5*time.Minute, // 空值短TTL
    )
    
    result, err := loader(ctx)
    if err != nil {
        return nil, err
    }
    
    if result.IsNull {
        return nil, ErrUserNotFound
    }
    
    return result.User, nil
}
```

### 4. 性能监控指标

```go
type CacheMetrics struct {
    Hits        int64
    Misses      int64
    LoadTime    time.Duration
    Errors      int64
}

var metrics = &CacheMetrics{}

func trackMetrics(hit bool, loadTime time.Duration, err error) {
    if hit {
        atomic.AddInt64(&metrics.Hits, 1)
    } else {
        atomic.AddInt64(&metrics.Misses, 1)
    }
    
    if err != nil {
        atomic.AddInt64(&metrics.Errors, 1)
    }
}

// 定期输出指标
func reportMetrics() {
    ticker := time.NewTicker(1 * time.Minute)
    for range ticker.C {
        hits := atomic.LoadInt64(&metrics.Hits)
        misses := atomic.LoadInt64(&metrics.Misses)
        total := hits + misses
        hitRate := float64(0)
        if total > 0 {
            hitRate = float64(hits) / float64(total) * 100
        }
        
        log.Printf("缓存命中率: %.2f%%, 总请求: %d, 错误: %d",
            hitRate, total, atomic.LoadInt64(&metrics.Errors))
    }
}
```

## 常见问题

### Q1: 如何处理缓存雪崩？

```go
// 使用随机TTL避免同时过期
func randomTTL(base time.Duration) time.Duration {
    jitter := time.Duration(rand.Int63n(int64(base / 10)))
    return base + jitter
}

loader := cachex.CacheWrapper(redisClient, key,
    loadFunc,
    randomTTL(time.Hour), // 1小时 ± 6分钟
)
```

### Q2: 如何实现缓存更新通知？

```go
// 使用Redis Pub/Sub通知其他节点更新缓存
func notifyCacheUpdate(key string) {
    redisClient.Publish(context.Background(), "cache:update", key)
}

// 订阅更新通知
pubsub := redisClient.Subscribe(context.Background(), "cache:update")
go func() {
    for msg := range pubsub.Channel() {
        key := msg.Payload
        // 删除本地缓存
        localMu.Lock()
        delete(localCache, key)
        localMu.Unlock()
    }
}()
```

### Q3: 如何处理大对象缓存？

```go
// 大对象分片存储
type LargeData struct {
    Chunks [][]byte
}

func cacheLargeData(ctx context.Context, data *LargeData) error {
    chunkSize := 1024 * 1024 // 1MB per chunk
    
    for i, chunk := range data.Chunks {
        key := fmt.Sprintf("large:data:chunk:%d", i)
        loader := cachex.CacheWrapper(redisClient, key,
            func(ctx context.Context) (*[]byte, error) {
                return &chunk, nil
            },
            time.Hour,
        )
        _, _ = loader(ctx)
    }
    
    return nil
}
```

## 总结

CacheWrapper 提供了强大而灵活的缓存功能：

- ✅ 泛型支持，类型安全
- ✅ 自动压缩，节省内存
- ✅ 延迟双删，保证一致性
- ✅ 优雅降级，提高可用性
- ✅ 并发安全，支持高并发

通过合理使用这些特性，可以构建高性能、高可用的缓存系统。
