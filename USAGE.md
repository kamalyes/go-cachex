# Go-Cachex 使用指南

本文档提供 Go-Cachex 各种Handler的实际使用示例和代码演示。

> 💡 **文档导航**: 
> - [README](./README.md) - 项目概览
> - [接口设计文档](./docs/INTERFACE-UNIFICATION-SUMMARY.md) - Handler接口定义和设计说明
> - [性能报告](./docs/PERFORMANCE-REPORT.md) - 性能测试数据

## 目录

- [LRU缓存使用](#lru缓存使用)
- [LRU优化版使用](#lru优化版使用)
- [Redis缓存使用](#redis缓存使用)
- [Ristretto缓存使用](#ristretto缓存使用)
- [过期缓存使用](#过期缓存使用)
- [分片缓存使用](#分片缓存使用)
- [两级缓存使用](#两级缓存使用)
- [Context超时控制](#context超时控制)
- [并发去重](#并发去重)
- [错误处理示例](#错误处理示例)
- [性能优化技巧](#性能优化技巧)
- [生产环境最佳实践](#生产环境最佳实践)

> 📖 **接口说明**: 所有Handler都实现统一的双API接口（简化版 + WithCtx版本），详见 [接口设计文档](./docs/INTERFACE-UNIFICATION-SUMMARY.md)

## LRU缓存使用

适合本地缓存和测试环境：

```go
client, err := cachex.NewLRUClient(ctx, 1000) // 容量 1000

// 特点：
// - 内存存储
// - LRU 驱逐策略
// - 支持 TTL
// - 线程安全

// 直接使用 Handler - 简化版API
cache := cachex.NewLRUHandler(1000)
defer cache.Close()

err := cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))
err = cache.SetWithTTL([]byte("key-ttl"), []byte("value"), 5*time.Second)

// 完整版API - 带context支持
ctx := context.Background()
err = cache.SetWithCtx(ctx, []byte("key2"), []byte("value2"))
val2, err := cache.GetWithCtx(ctx, []byte("key2"))
```

### LRU Optimized 缓存 (推荐)

🚀 超高性能分片架构，适合大型高并发应用：

```go
client, err := cachex.NewLRUOptimizedClient(ctx, 10000) // 容量 10000

// 性能特点：
// - 16分片设计，消除锁竞争 (500%+ 性能提升)
// - 原子操作，零内存分配
// - 缓存行对齐，NUMA友好
// - 批量并行操作
// - 详细性能统计

// 直接使用 Handler
cache := cachex.NewLRUOptimizedHandler(10000)
defer cache.Close()

// 简化版API（极致性能）
err := cache.Set([]byte("key"), []byte("value"))        // 68ns/op, 0 allocs
val, err := cache.Get([]byte("key"))                    // 178ns/op
results, errs := cache.BatchGet([][]byte{               // 并行处理
    []byte("key1"), []byte("key2"), []byte("key3"),
})

// WithCtx版本 - 支持超时控制
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
defer cancel()
val2, err := cache.GetWithCtx(ctx, []byte("key"))

// 实时统计
stats := cache.Stats()
fmt.Printf("分片数: %v, 命中率: %.2f%%, 条目数: %v\n", 
    stats["shard_count"], stats["hit_rate"], stats["entries"])

// 适用场景：
// - 金融交易系统（微秒级延迟要求）
// - 游戏服务器（百万级并发）
// - AI推理服务（模型权重缓存）
// - 搜索引擎（热词索引缓存）
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

// 简化版API
err = cache.Set([]byte("key"), []byte("value"))
err = cache.SetWithTTL([]byte("key-ttl"), []byte("value"), 24*time.Hour)

// WithCtx版本
ctx := context.Background()
err = cache.SetWithCtx(ctx, []byte("key2"), []byte("value2"))
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

// 简化版API
err = cache.Set([]byte("key"), []byte("value"))
err = cache.SetWithTTL([]byte("key-ttl"), []byte("value"), time.Minute)

// WithCtx版本
ctx := context.Background()
val, err := cache.GetWithCtx(ctx, []byte("key"))
```

### 过期缓存

自动清理过期键的内存缓存：

```go
// 创建过期缓存（自动清理过期键）
cache := cachex.NewExpiringHandler()
defer cache.Close()

// 简化版API
err := cache.Set([]byte("key"), []byte("value"))
err = cache.SetWithTTL([]byte("temp"), []byte("value"), 30*time.Second)

// WithCtx版本
ctx := context.Background()
err = cache.SetWithCtx(ctx, []byte("key2"), []byte("value2"))

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

// 简化版API - 键自动分配到不同分片
err := cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))

// WithCtx版本
ctx := context.Background()
val2, err := cache.GetWithCtx(ctx, []byte("key"))
```

#### 两级缓存

```go
// 创建两级缓存系统
l1 := cachex.NewLRUHandler(1000)         // 快速本地缓存
l2, _ := cachex.NewRedisHandler(&cachex.RedisConfig{
    Addrs: []string{"localhost:6379"},
})

cache := cachex.NewTwoLevelHandler(l1, l2, &cachex.TwoLevelConfig{
    WriteStrategy: cachex.WriteThrough, // 写透策略
})
defer cache.Close()

// 简化版API - 自动处理两级缓存
err := cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))
err = cache.SetWithTTL([]byte("key"), []byte("value"), time.Hour)

// WithCtx版本
ctx := context.Background()
val2, err := cache.GetWithCtx(ctx, []byte("key"))
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
// 🚀 超高性能场景 (推荐)
client, _ := cachex.NewLRUOptimizedClient(ctx, 10000)
// 适用: 金融交易、游戏服务器、AI推理、搜索引擎

// 本地应用或中小型系统
client, _ := cachex.NewLRUClient(ctx, 1000)

// 分布式应用
client, _ := cachex.NewRedisClient(ctx, redisConfig)

// 读多写少的大数据场景
client, _ := cachex.NewRistrettoClient(ctx, ristrettoConfig)

// 大容量分层存储
client, _ := cachex.NewTwoLevelClient(ctx, l1Config, l2Config)

// 简单过期缓存
cache := cachex.NewExpiringHandler(time.Hour)
```

### 2. 性能选型指南

| 场景类型 | 推荐方案 | 性能特点 | QPS能力 |
|---------|---------|---------|---------|
| **超大并发系统** | LRU Optimized | 500%+提升，零分配 | 20M+ ops/s |
| **金融交易** | LRU Optimized | 42ns延迟，16分片 | 23M+ ops/s |
| **中小型应用** | LRU Classic | 稳定可靠 | 2M+ ops/s |
| **分布式系统** | Redis | 网络分布式 | 取决于网络 |
| **读密集应用** | Ristretto | 高命中率 | 8M+ ops/s |
| **分层存储** | TwoLevel | 智能提升 | 混合性能 |

### 3. 批量操作优化

```go
// ❌ 避免逐个操作
for _, key := range keys {
    val, _ := client.Get(ctx, key)
    // 处理val...
}

// ✅ 使用批量操作 (推荐)
results, errors := client.BatchGet(ctx, keys)
for i, key := range keys {
    if errors[i] == nil {
        // 处理results[i]...
    }
}
```

### 4. 合理设置 TTL

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

### 5. 监控和统计最佳实践

```go
// 📊 实时监控缓存状态
func monitorCache(client cachex.Handler) {
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()
    
    for range ticker.C {
        stats := client.Stats(context.Background())
        
        // 基础指标
        entries := stats["entries"]
        capacity := stats["client_capacity"]
        
        // 性能指标 (如果支持)
        if hitRate, exists := stats["hit_rate"]; exists {
            fmt.Printf("命中率: %.2f%%, 条目: %v/%v\n", 
                hitRate.(float64)*100, entries, capacity)
        }
        
        // LRU Optimized 分片指标
        if shardCount, exists := stats["shard_count"]; exists {
            fmt.Printf("分片数: %v, 总命中: %v, 总未命中: %v\n",
                shardCount, stats["hits"], stats["misses"])
        }
        
        // 内存压力检测
        if entries.(int) > int(capacity.(int)) * 0.8 {
            log.Printf("⚠️ 缓存使用率超过80%，考虑增加容量")
        }
    }
}

// 🚨 性能告警系统
func setupAlerts(client cachex.Handler) {
    go func() {
        for {
            time.Sleep(30 * time.Second)
            stats := client.Stats(context.Background())
            
            // 命中率告警
            if hitRate, exists := stats["hit_rate"]; exists {
                if hitRate.(float64) < 0.5 {
                    log.Printf("🚨 缓存命中率过低: %.2f%%", hitRate.(float64)*100)
                }
            }
            
            // Ristretto 特殊指标
            if keysEvicted, exists := stats["keys_evicted"]; exists {
                if keysEvicted.(uint64) > 1000 {
                    log.Printf("🚨 缓存驱逐频繁: %v次", keysEvicted)
                }
            }
        }
    }()
}
```

### 6. 包装客户端以添加指标

```go
type MetricsClient struct {
    client cachex.Handler
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
func createLayeredCache(ctx context.Context) cachex.Handler {
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
    l1, l2 cachex.Handler
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
func getWithRetry(ctx context.Context, client cachex.Handler, key []byte, maxRetries int) ([]byte, error) {
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