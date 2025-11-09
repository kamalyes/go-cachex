# Go-Cachex 使用指南

本文档提供 Go-Cachex 的详细使用说明和最佳实践。

## 目录

- [快速开始](#快速开始)
- [客户端接口](#客户端接口)
- [缓存实现](#缓存实现)
- [Context 支持](#context-支持)
- [错误处理](#错误处理)
- [最佳实践](#最佳实践)

## 快速开始

### 安装

```sh
go get -u github.com/kamalyes/go-cachex
```

### 基本示例

```go
package main

import (
    "context"
    "fmt"
    "time"
    
    "github.com/kamalyes/go-cachex"
)

func main() {
    ctx := context.Background()
    
    // 创建 LRU 缓存客户端
    client, err := cachex.NewLRUClient(ctx, 1000)
    if err != nil {
        panic(err)
    }
    defer client.Close()
    
    // 基本操作
    err = client.Set(ctx, []byte("hello"), []byte("world"))
    if err != nil {
        panic(err)
    }
    
    val, err := client.Get(ctx, []byte("hello"))
    if err != nil {
        panic(err)
    }
    
    fmt.Printf("Value: %s\n", string(val)) // Output: Value: world
}
```

## 客户端接口

### 统一客户端配置

Go-Cachex 提供统一的客户端接口，支持所有缓存实现：

```go
// 使用 ClientConfig 配置
client, err := cachex.NewClient(ctx, &cachex.ClientConfig{
    Type:     cachex.CacheLRU,
    Capacity: 1000,
})

// 使用便利函数
lruClient, err := cachex.NewLRUClient(ctx, 1000)
redisClient, err := cachex.NewRedisClient(ctx, &cachex.RedisConfig{
    Addrs: []string{"localhost:6379"},
})
ristrettoClient, err := cachex.NewRistrettoClient(ctx, &cachex.RistrettoConfig{
    NumCounters: 1e7,
    MaxCost:     1 << 30,
    BufferItems: 64,
})
```

### 基本操作

```go
// 设置值
err := client.Set(ctx, []byte("key"), []byte("value"))

// 获取值
val, err := client.Get(ctx, []byte("key"))

// 设置带 TTL 的值
err = client.SetWithTTL(ctx, []byte("key"), []byte("value"), time.Hour)

// 获取 TTL
ttl, err := client.GetTTL(ctx, []byte("key"))

// 删除键
err = client.Del(ctx, []byte("key"))

// 智能加载（去重）
val, err = client.GetOrCompute(ctx, []byte("key"), time.Hour, func(ctx context.Context) ([]byte, error) {
    // 昂贵的计算，并发请求下只会执行一次
    return []byte("computed"), nil
})
```

## 缓存实现

### LRU 缓存

适合本地缓存和测试环境：

```go
client, err := cachex.NewLRUClient(ctx, 1000) // 容量 1000

// 特点：
// - 内存存储
// - LRU 驱逐策略
// - 支持 TTL
// - 线程安全

// 直接使用 Handler
cache := cachex.NewLRUHandler(1000)
defer cache.Close()

err := cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))
err = cache.SetWithTTL([]byte("key-ttl"), []byte("value"), 5*time.Second)
```

### Redis 缓存

适合分布式系统：

```go
// 单节点
client, err := cachex.NewRedisClient(ctx, &cachex.RedisConfig{
    Addrs:    []string{"localhost:6379"},
    Password: "password",
    DB:       0,
})

// 集群模式
client, err := cachex.NewRedisClient(ctx, &cachex.RedisConfig{
    Addrs: []string{
        "localhost:7000",
        "localhost:7001", 
        "localhost:7002",
    },
    IsCluster: true,
})

// 直接使用 Handler
cache, err := cachex.NewRedisHandler(&cachex.RedisConfig{
    Addrs: []string{"localhost:6379"},
})
defer cache.Close()

err = cache.Set([]byte("key"), []byte("value"))
err = cache.SetWithTTL([]byte("key-ttl"), []byte("value"), 24*time.Hour)
```

### Ristretto 缓存

高性能缓存实现：

```go
client, err := cachex.NewRistrettoClient(ctx, &cachex.RistrettoConfig{
    NumCounters: 1e7,     // 预期键数量
    MaxCost:     1 << 30, // 最大内存（字节）
    BufferItems: 64,      // 缓冲区大小
})

// 直接使用 Handler
config := &cachex.RistrettoConfig{
    NumCounters: 1e7,
    MaxCost:     1 << 30,
    BufferItems: 64,
}
cache, err := cachex.NewRistrettoHandler(config)
defer cache.Close()

err = cache.Set([]byte("key"), []byte("value"))
err = cache.SetWithTTL([]byte("key-ttl"), []byte("value"), time.Minute)
```

### 过期缓存

自动清理过期键的内存缓存：

```go
// 创建过期缓存（自动清理过期键）
cache := cachex.NewExpiringHandler()
defer cache.Close()

// 基本操作与 TTL
err := cache.Set([]byte("key"), []byte("value"))
err = cache.SetWithTTL([]byte("temp"), []byte("value"), 30*time.Second)

// 过期键会自动清理
time.Sleep(31 * time.Second)
_, err = cache.Get([]byte("temp")) // 返回 ErrNotFound
```

### 高级缓存模式

#### 上下文感知缓存

```go
// 创建上下文感知缓存包装器
baseCache := cachex.NewRistrettoHandler(nil)
cache := cachex.NewCtxCache(baseCache)

// GetOrCompute - 并发请求去重
loader := func(ctx context.Context) ([]byte, error) {
    // 昂贵的计算或远程调用，并发情况下只执行一次
    return []byte("computed"), nil
}
val, err := cache.GetOrCompute(ctx, []byte("key"), loader)

// WithCache - 在缓存中执行操作
err = cache.WithCache(ctx, []byte("key"), func(val []byte) error {
    // 使用缓存值的操作
    return nil
})
```

#### 分片缓存

```go
// 创建分片缓存
factory := func() cachex.Handler {
    return cachex.NewLRUHandler(1000)
}
cache := cachex.NewShardedHandler(16, factory) // 16 个分片
defer cache.Close()

// 使用方式与普通缓存相同，键自动分配到不同分片
err := cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))
```

#### 两级缓存

```go
// 创建两级缓存系统
l1 := cachex.NewLRUHandler(1000)         // 快速本地缓存
l2 := cachex.NewRedisHandler(redisConfig) // 慢速共享缓存

cache := cachex.NewTwoLevelHandler(l1, l2, &cachex.TwoLevelConfig{
    WriteStrategy: cachex.WriteThrough, // 写透策略
})
defer cache.Close()

// 自动处理两级缓存：优先从 L1 获取，未命中则回退到 L2
err := cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))
err = cache.SetWithTTL([]byte("key"), []byte("value"), time.Hour)
```

## Context 支持

### 超时控制

```go
// 设置超时
ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
defer cancel()

val, err := client.Get(ctx, []byte("key"))
if err == context.DeadlineExceeded {
    // 处理超时
}
```

### 并发去重

```go
// GetOrCompute 自动去重并发请求
val, err := client.GetOrCompute(ctx, []byte("expensive-key"), time.Hour, func(ctx context.Context) ([]byte, error) {
    // 即使有 100 个并发请求，这个函数也只会执行一次
    time.Sleep(time.Second) // 模拟昂贵计算
    return []byte("result"), nil
})
```

### 取消支持

```go
ctx, cancel := context.WithCancel(context.Background())

// 在另一个 goroutine 中取消
go func() {
    time.Sleep(50 * time.Millisecond)
    cancel()
}()

// 操作会被取消
val, err := client.GetOrCompute(ctx, []byte("key"), time.Hour, func(ctx context.Context) ([]byte, error) {
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    case <-time.After(100 * time.Millisecond):
        return []byte("result"), nil
    }
})
```

## 错误处理

### 标准错误类型

```go
val, err := client.Get(ctx, []byte("key"))
switch err {
case nil:
    // 成功
case cachex.ErrNotFound:
    // 键不存在
case cachex.ErrClosed:
    // 缓存已关闭
case context.DeadlineExceeded:
    // 超时
case context.Canceled:
    // 取消
default:
    // 其他错误
}
```

### 错误类型说明

- `ErrNotFound`: 缓存中未找到键
- `ErrInvalidKey`: 无效或空键
- `ErrInvalidValue`: 无效或空值
- `ErrInvalidTTL`: 无效的 TTL 值
- `ErrClosed`: 缓存实例已关闭
- `ErrCapacityExceeded`: 超出缓存容量限制

### 优雅降级

```go
func getValue(ctx context.Context, key []byte) ([]byte, error) {
    // 尝试从缓存获取
    val, err := client.Get(ctx, key)
    if err == nil {
        return val, nil
    }
    
    // 缓存未命中，从数据库加载
    if err == cachex.ErrNotFound {
        return loadFromDB(ctx, key)
    }
    
    // 其他错误
    return nil, err
}
```

### 错误处理最佳实践

```go
// 1. 检查特定错误类型
if err == cachex.ErrNotFound {
    // 处理键不存在的情况
}

// 2. 优雅降级
val, err := client.Get(ctx, []byte("key"))
if err == cachex.ErrNotFound {
    // 从备用源加载数据
    val = loadFromBackup(ctx, []byte("key"))
}

// 3. TTL 验证
if err == cachex.ErrInvalidTTL {
    // 使用默认 TTL
    err = client.SetWithTTL(ctx, key, value, time.Hour)
}

// 4. 优雅关闭
defer func() {
    if err := client.Close(); err != nil {
        log.Printf("关闭缓存时出错: %v", err)
    }
}()
```

## 最佳实践

### 1. 选择合适的缓存类型

```go
// 本地应用或测试
client, _ := cachex.NewLRUClient(ctx, 1000)

// 分布式应用
client, _ := cachex.NewRedisClient(ctx, redisConfig)

// 高性能要求
client, _ := cachex.NewRistrettoClient(ctx, ristrettoConfig)

// 简单过期缓存
cache := cachex.NewExpiringHandler()
```

### 2. 合理设置 TTL

```go
// 短期数据
client.SetWithTTL(ctx, key, value, 5*time.Minute)

// 长期数据
client.SetWithTTL(ctx, key, value, 24*time.Hour)

// 永久数据（直到手动删除或容量驱逐）
client.Set(ctx, key, value)
```

### 3. 使用 GetOrCompute 避免缓存击穿

```go
func getUser(ctx context.Context, userID []byte) (*User, error) {
    data, err := client.GetOrCompute(ctx, userID, time.Hour, func(ctx context.Context) ([]byte, error) {
        // 这里的代码在并发情况下只会执行一次
        user, err := db.GetUser(ctx, string(userID))
        if err != nil {
            return nil, err
        }
        return json.Marshal(user)
    })
    
    if err != nil {
        return nil, err
    }
    
    var user User
    err = json.Unmarshal(data, &user)
    return &user, err
}
```

### 4. 正确处理关闭

```go
func main() {
    client, err := cachex.NewLRUClient(ctx, 1000)
    if err != nil {
        panic(err)
    }
    
    // 确保正确关闭
    defer func() {
        if err := client.Close(); err != nil {
            log.Printf("关闭缓存失败: %v", err)
        }
    }()
    
    // 使用缓存...
}
```

### 5. 监控和指标

```go
// 包装客户端以添加指标
type MetricsClient struct {
    client cachex.ContextHandler
    hits   int64
    misses int64
}

func (m *MetricsClient) Get(ctx context.Context, key []byte) ([]byte, error) {
    val, err := m.client.Get(ctx, key)
    if err == cachex.ErrNotFound {
        atomic.AddInt64(&m.misses, 1)
    } else if err == nil {
        atomic.AddInt64(&m.hits, 1)
    }
    return val, err
}

func (m *MetricsClient) HitRate() float64 {
    hits := atomic.LoadInt64(&m.hits)
    misses := atomic.LoadInt64(&m.misses)
    total := hits + misses
    if total == 0 {
        return 0
    }
    return float64(hits) / float64(total)
}
```

### 6. 分层缓存策略

```go
// 多级缓存提升性能
func createLayeredCache(ctx context.Context) cachex.ContextHandler {
    // L1: 快速内存缓存
    l1, _ := cachex.NewLRUClient(ctx, 1000)
    
    // L2: 分布式 Redis 缓存
    l2, _ := cachex.NewRedisClient(ctx, &cachex.RedisConfig{
        Addrs: []string{"localhost:6379"},
    })
    
    // 组合成两级缓存
    return &LayeredCache{l1: l1, l2: l2}
}

type LayeredCache struct {
    l1, l2 cachex.ContextHandler
}

func (lc *LayeredCache) Get(ctx context.Context, key []byte) ([]byte, error) {
    // 先尝试 L1
    if val, err := lc.l1.Get(ctx, key); err == nil {
        return val, nil
    }
    
    // L1 未命中，尝试 L2
    val, err := lc.l2.Get(ctx, key)
    if err == nil {
        // 回填到 L1
        lc.l1.Set(ctx, key, val)
    }
    return val, err
}
```

### 7. 键设计原则

```go
// 好的键设计
func makeKey(prefix, userID string, version int) []byte {
    return []byte(fmt.Sprintf("%s:user:%s:v%d", prefix, userID, version))
}

// 使用命名空间避免冲突
const (
    UserCachePrefix    = "user"
    SessionCachePrefix = "session"
    MetricsCachePrefix = "metrics"
)

userKey := makeKey(UserCachePrefix, "123", 1)       // "user:user:123:v1"
sessionKey := makeKey(SessionCachePrefix, "abc", 1) // "session:user:abc:v1"
```

### 8. 错误处理和重试

```go
func getWithRetry(ctx context.Context, client cachex.ContextHandler, key []byte, maxRetries int) ([]byte, error) {
    var lastErr error
    
    for i := 0; i < maxRetries; i++ {
        val, err := client.Get(ctx, key)
        if err == nil {
            return val, nil
        }
        
        // 如果是不可重试的错误，直接返回
        if err == cachex.ErrNotFound || err == cachex.ErrInvalidKey {
            return nil, err
        }
        
        lastErr = err
        
        // 指数退避
        backoff := time.Duration(i+1) * 100 * time.Millisecond
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-time.After(backoff):
            continue
        }
    }
    
    return nil, lastErr
}
```