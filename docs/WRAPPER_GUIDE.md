# CacheWrapper 完整使用指南

## 📚 目录

- [快速开始](#快速开始)
- [核心概念](#核心概念)
- [所有可用选项](#所有可用选项)
- [函数式选项构建器](#函数式选项构建器)
- [实际应用场景](#实际应用场景)
- [性能优化](#性能优化)
- [最佳实践](#最佳实践)
- [故障排查](#故障排查)
- [架构设计](#架构设计)

---

## 快速开始

### 基本用法

```go
import (
    "context"
    "time"
    "github.com/redis/go-redis/v9"
    "github.com/kamalyes/go-cachex"
)

// 创建 Redis 客户端
client := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
})

// 定义数据加载函数
loader := func(ctx context.Context) (*User, error) {
    return getUserFromDB(ctx, userID)
}

// 创建缓存包装器
cachedLoader := cachex.CacheWrapper(
    client,
    "user:123",        // 缓存键
    loader,            // 数据加载函数
    time.Hour,         // 缓存过期时间
)

// 使用缓存
user, err := cachedLoader(ctx)
```

### 带选项的用法

```go
cachedLoader := cachex.CacheWrapper(
    client,
    "user:123",
    loader,
    time.Hour,
    cachex.When(isVIP, cachex.WithTTL(time.Hour * 24)),    // 条件选项
    cachex.WithoutCompression(),                            // 跳过压缩
    cachex.WithRetry(3),                                    // 重试 3 次
)
```

---

## 核心概念

### 架构设计

```
                    CacheWrapper[T]
                         ↓
    ┌─────────────────────────────────────────────────┐
    │              缓存流程控制                        │
    │  1. 缓存查询 → 2. 数据加载 → 3. 延迟双删策略      │
    └─────────────────────────────────────────────────┘
              ↓              ↓              ↓
    ┌─────────────┐  ┌─────────────┐  ┌─────────────┐
    │ Redis Get   │  │ DataLoader  │  │ Redis Set   │
    │ 数据解压缩   │  │ 函数执行     │  │ 数据压缩     │
    │ JSON反序列化 │  │ 错误处理     │  │ 延迟删除     │
    └─────────────┘  └─────────────┘  └─────────────┘
```

### 延迟双删策略

```bash
写操作流程：
1. 删除缓存 (第一次删除)
   ↓
2. 设置新缓存
   ↓
3. 延迟100ms
   ↓
4. 再次删除缓存 (第二次删除)
   ↓
5. 重新设置最新缓存

目的：防止并发写入导致的缓存不一致问题
```

### 核心特性

1. **泛型支持** - 支持任意类型的数据缓存
2. **自动压缩** - 使用 Zlib 算法压缩，减少内存占用
3. **延迟双删** - 保证缓存一致性
4. **错误降级** - Redis 故障时自动降级到数据源
5. **并发安全** - 支持高并发访问
6. **灵活选项** - 丰富的选项系统

---

## 所有可用选项

### 选项总览

| 选项 | 函数 | 说明 | 使用场景 |
|------|------|------|----------|
| **强制刷新** | `WithForceRefresh(bool)` | 强制从数据源刷新 | 管理员操作、定时任务 |
| **TTL 覆盖** | `WithTTL(duration)` | 覆盖默认的缓存过期时间 | VIP 用户、动态 TTL |
| **跳过压缩** | `WithoutCompression()` | 不压缩缓存数据 | 小数据、已压缩数据 |
| **异步更新** | `WithAsyncUpdate()` | 异步写入缓存，不阻塞返回 | 高并发、非关键数据 |
| **错误重试** | `WithRetry(times)` | Redis 失败时重试 | 网络不稳定、关键数据 |

### 1. WithForceRefresh - 强制刷新缓存

**签名：** `WithForceRefresh(force bool) CacheOption`

**用途：** 跳过缓存，强制从数据源加载最新数据

**使用示例：**

```go
// 管理员操作
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.When(isAdmin, cachex.WithForceRefresh(true)),
)

// 定时任务
cachex.CacheWrapper(client, key, loader, time.Hour * 24,
    cachex.WithForceRefresh(true), // 总是刷新
)
```

### 2. WithTTL - 覆盖默认 TTL

**签名：** `WithTTL(ttl time.Duration) CacheOption`

**用途：** 根据业务需求动态设置缓存过期时间

**使用示例：**

```go
// VIP 用户更长缓存
cachex.CacheWrapper(client, key, loader, time.Minute,
    cachex.WhenThen(isVIP, 
        cachex.WithTTL(time.Hour * 24),  // VIP: 24小时
        cachex.WithTTL(time.Hour),        // 普通: 1小时
    ),
)

// 根据数据类型
cachex.CacheWrapper(client, key, loader, time.Minute,
    cachex.Match([]cachex.Case{
        cachex.NewCase(dataType == "static", cachex.WithTTL(time.Hour * 24 * 7)),
        cachex.NewCase(dataType == "dynamic", cachex.WithTTL(time.Minute * 5)),
    }, cachex.WithTTL(time.Hour)),
)
```

### 3. WithoutCompression - 跳过压缩

**签名：** `WithoutCompression() CacheOption`

**用途：** 对小数据或已压缩的数据跳过压缩步骤

**何时使用：**

- ✅ 布尔值、小字符串（<100 字节）
- ✅ 已压缩的数据（图片 URL、视频链接）
- ✅ 需要极致读取性能
- ❌ 大对象（>1KB）

**使用示例：**

```go
// 小数据
type OnlineStatus struct {
    IsOnline bool
}
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.WithoutCompression(), // 单个布尔值不需要压缩
)

// 条件压缩
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.When(dataSize < 200, cachex.WithoutCompression()),
)
```

**性能对比：**

| 数据大小 | 压缩后大小 | 压缩耗时 | 解压耗时 | 建议 |
|---------|-----------|---------|---------|------|
| 10B     | 15B       | 50μs    | 30μs    | ❌ 不压缩 |
| 100B    | 80B       | 80μs    | 40μs    | ❌ 不压缩 |
| 1KB     | 400B      | 200μs   | 100μs   | ✅ 压缩 |
| 10KB    | 2KB       | 500μs   | 200μs   | ✅ 压缩 |
| 100KB   | 15KB      | 2ms     | 800μs   | ✅ 压缩 |

### 4. WithAsyncUpdate - 异步更新

**签名：** `WithAsyncUpdate() CacheOption`

**用途：** 后台异步更新缓存，不阻塞业务逻辑返回

**适用场景：**

- ✅ 非关键数据（允许短暂延迟）
- ✅ 高并发读取场景
- ✅ 缓存更新耗时较长
- ❌ 强一致性要求的数据

**使用示例：**

```go
// 统计数据异步更新
cachex.CacheWrapper(client, "stats:daily", loader, time.Hour,
    cachex.WithAsyncUpdate(), // 不阻塞返回
)

// 条件异步
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.When(isHighConcurrency, cachex.WithAsyncUpdate()),
)
```

**性能提升：**

| 场景 | 同步耗时 | 异步耗时 | 响应提升 |
|-----|---------|---------|---------|
| 小数据 | 5ms | 3ms | 40% |
| 中等数据 | 15ms | 3ms | 80% |
| 大数据 | 50ms | 3ms | 94% |

### 5. WithRetry - 错误重试

**签名：** `WithRetry(times int) CacheOption`

**用途：** Redis 操作失败时自动重试

**重试策略：**

- 指数退避：等待时间 = (重试次数)² × 50ms
- 第 1 次重试：50ms 后
- 第 2 次重试：200ms 后
- 第 3 次重试：450ms 后

**使用示例：**

```go
// 关键数据
cachex.CacheWrapper(client, "config:system", loader, time.Hour * 24,
    cachex.WithRetry(3), // 最多重试 3 次
)

// 条件重试
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.WhenThen(isCritical,
        cachex.WithRetry(3),    // 关键数据重试3次
        cachex.WithRetry(1),    // 普通数据重试1次
    ),
)
```

---

## 函数式选项构建器

### When - 条件选项

当条件为 true 时应用选项：

```go
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.When(isVIP, cachex.WithTTL(time.Hour * 24)),
    cachex.When(needRefresh, cachex.WithForceRefresh(true)),
    cachex.When(isSmallData, cachex.WithoutCompression()),
)
```

**替代传统写法：**

```go
// ❌ 传统写法
opts := []cachex.CacheOption{}
if req.ForceRefresh {
    opts = append(opts, cachex.WithForceRefresh(true))
}
if isVIP {
    opts = append(opts, cachex.WithTTL(time.Hour * 24))
}

// ✅ 函数式写法
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.When(req.ForceRefresh, cachex.WithForceRefresh(true)),
    cachex.When(isVIP, cachex.WithTTL(time.Hour * 24)),
)
```

### WhenThen - 二选一

根据条件选择不同的选项：

```go
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.WhenThen(isVIP,
        cachex.WithTTL(time.Hour * 24),  // VIP: 24小时
        cachex.WithTTL(time.Hour),        // 普通: 1小时
    ),
    cachex.WhenThen(isCritical,
        cachex.WithRetry(3),              // 关键: 重试3次
        cachex.WithAsyncUpdate(),         // 非关键: 异步更新
    ),
)
```

### Match - 多条件匹配

类似 switch-case 的选项选择：

```go
cachex.CacheWrapper(client, key, loader, time.Minute,
    cachex.Match([]cachex.Case{
        cachex.NewCase(level == "VIP", cachex.WithTTL(time.Hour * 24)),
        cachex.NewCase(level == "Premium", cachex.WithTTL(time.Hour * 12)),
        cachex.NewCase(level == "Normal", cachex.WithTTL(time.Hour * 6)),
    }, cachex.WithTTL(time.Hour)), // 默认值
)
```

### Combine - 组合选项

将多个选项组合成预设：

```go
// 定义预设
var (
    VIPPreset = cachex.Combine(
        cachex.WithTTL(time.Hour * 24),
        cachex.WithRetry(3),
        cachex.WithAsyncUpdate(),
    )
    
    FastPreset = cachex.Combine(
        cachex.WithoutCompression(),
        cachex.WithTTL(time.Minute * 5),
    )
)

// 使用预设
cachex.CacheWrapper(client, key, loader, time.Hour,
    cachex.When(isVIP, VIPPreset),
    cachex.When(needFast, FastPreset),
)
```

---

## 实际应用场景

### 场景 1：用户数据缓存

```go
func (s *UserService) GetUser(ctx context.Context, req *GetUserRequest) (*User, error) {
    cacheKey := fmt.Sprintf("user:%s", req.UserID)
    
    cachedLoader := cachex.CacheWrapper(
        s.redisClient,
        cacheKey,
        func(ctx context.Context) (*User, error) {
            return s.fetchUserFromDB(ctx, req.UserID)
        },
        time.Hour,
        // 函数式选项
        cachex.When(req.ForceRefresh, cachex.WithForceRefresh(true)),
        cachex.WhenThen(req.User.IsVIP,
            cachex.WithTTL(time.Hour * 24),  // VIP 24小时
            cachex.WithTTL(time.Hour),        // 普通 1小时
        ),
    )
    
    return cachedLoader(ctx)
}
```

### 场景 2：多级缓存策略

```go
func (s *Service) GetData(ctx context.Context, req *Request) (*Data, error) {
    cacheKey := fmt.Sprintf("data:%s", req.ID)
    
    cachedLoader := cachex.CacheWrapper(
        s.redisClient,
        cacheKey,
        func(ctx context.Context) (*Data, error) {
            return s.loadData(ctx, req.ID)
        },
        time.Minute * 5,
        // 根据数据大小选择策略
        cachex.Match([]cachex.Case{
            cachex.NewCase(req.DataSize == "small", cachex.Combine(
                cachex.WithoutCompression(),
                cachex.WithTTL(time.Minute * 5),
            )),
            cachex.NewCase(req.DataSize == "medium", cachex.Combine(
                cachex.WithTTL(time.Hour),
            )),
            cachex.NewCase(req.DataSize == "large", cachex.Combine(
                cachex.WithAsyncUpdate(),
                cachex.WithTTL(time.Hour * 24),
            )),
        }),
        // 根据优先级选择重试策略
        cachex.WhenThen(req.Priority == "high",
            cachex.WithRetry(3),
            cachex.WithRetry(1),
        ),
    )
    
    return cachedLoader(ctx)
}
```

### 场景 3：API 响应缓存

```go
func (s *APIService) GetWeather(ctx context.Context, city string, freshData bool) (*WeatherData, error) {
    cacheKey := fmt.Sprintf("weather:%s", city)
    
    cachedLoader := cachex.CacheWrapper(
        s.redisClient,
        cacheKey,
        func(ctx context.Context) (*WeatherData, error) {
            return s.fetchWeatherFromAPI(ctx, city)
        },
        time.Minute * 15,
        cachex.When(freshData, cachex.WithForceRefresh(true)),
        cachex.WithRetry(2), // API 调用失败重试
    )
    
    return cachedLoader(ctx)
}
```

### 场景 4：定时任务刷新

```go
func (s *Service) RefreshCache(ctx context.Context) {
    cacheKey := "stats:daily"
    
    cachedLoader := cachex.CacheWrapper(
        s.redisClient,
        cacheKey,
        func(ctx context.Context) (*Stats, error) {
            return s.calculateStats(ctx)
        },
        time.Hour * 24,
        cachex.WithForceRefresh(true),  // 定时任务总是刷新
        cachex.WithRetry(3),             // 确保成功
    )
    
    _, err := cachedLoader(ctx)
    if err != nil {
        log.Printf("刷新缓存失败: %v", err)
    }
}
```

---

## 性能优化

### 1. 缓存键设计

```go
// ✅ 好的键设计
fmt.Sprintf("user:%d", userID)
fmt.Sprintf("product:%d:category:%s", productID, category)
fmt.Sprintf("search:%s:page:%d", query, page)

// ❌ 避免的键设计
"user_data_" + string(userID)            // 字符串拼接效率低
fmt.Sprintf("data_%v", complexObject)    // 复杂对象作为键
```

### 2. 过期时间策略

```go
// 根据数据特性设置不同的过期时间
var (
    UserCacheExpiration     = time.Hour * 24     // 用户数据：24小时
    ProductCacheExpiration  = time.Hour * 6      // 商品数据：6小时
    SearchCacheExpiration   = time.Minute * 15   // 搜索结果：15分钟
    ConfigCacheExpiration   = time.Hour * 72     // 配置数据：72小时
)

// 使用
cachex.CacheWrapper(client, key, loader, UserCacheExpiration)
```

### 3. 压缩策略选择

```go
// 小数据跳过压缩
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.When(dataSize < 200, cachex.WithoutCompression()),
)

// 大数据启用压缩（默认）
cachex.CacheWrapper(client, key, largeDataLoader, ttl)
```

### 4. 异步更新策略

```go
// 非关键数据异步更新
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.When(!isCritical, cachex.WithAsyncUpdate()),
)

// 高并发场景
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.When(isHighConcurrency, cachex.WithAsyncUpdate()),
)
```

### 5. 缓存预热

```go
func (s *UserService) WarmupCache(ctx context.Context, userIDs []int) error {
    for _, userID := range userIDs {
        go func(id int) {
            // 异步预热缓存
            _, _ = s.GetUser(ctx, &GetUserRequest{UserID: id})
        }(userID)
    }
    return nil
}
```

---

## 最佳实践

### 1. 使用函数式选项构建器

```go
// ✅ 推荐：函数式风格，简洁清晰
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.When(req.ForceRefresh, cachex.WithForceRefresh(true)),
    cachex.WhenThen(isVIP, 
        cachex.WithTTL(time.Hour * 24),
        cachex.WithTTL(time.Hour),
    ),
)

// ❌ 不推荐：命令式风格，代码冗长
opts := []cachex.CacheOption{}
if req.ForceRefresh {
    opts = append(opts, cachex.WithForceRefresh(true))
}
if isVIP {
    opts = append(opts, cachex.WithTTL(time.Hour * 24))
} else {
    opts = append(opts, cachex.WithTTL(time.Hour))
}
cachedLoader := cachex.CacheWrapper(client, key, loader, ttl, opts...)
```

### 2. 创建可重用的选项预设

```go
// 定义预设
var (
    // VIP 用户预设
    VIPOptions = cachex.Combine(
        cachex.WithTTL(time.Hour * 24),
        cachex.WithRetry(3),
        cachex.WithAsyncUpdate(),
    )
    
    // 快速访问预设
    FastOptions = cachex.Combine(
        cachex.WithoutCompression(),
        cachex.WithTTL(time.Minute * 5),
    )
    
    // 关键数据预设
    CriticalOptions = cachex.Combine(
        cachex.WithRetry(3),
        cachex.WithTTL(time.Hour),
    )
)

// 使用预设
cachex.CacheWrapper(client, key, loader, time.Hour,
    cachex.When(user.IsVIP, VIPOptions),
    cachex.When(isCritical, CriticalOptions),
)
```

### 3. 缓存键命名规范

```go
// 使用统一的缓存键生成函数
func GetUserCacheKey(userID string) string {
    return fmt.Sprintf("user:detail:%s", userID)
}

func GetProductCacheKey(productID int, category string) string {
    return fmt.Sprintf("product:%d:cat:%s", productID, category)
}

// 使用
cacheKey := GetUserCacheKey(req.UserID)
cachedLoader := cachex.CacheWrapper(client, cacheKey, loader, ttl)
```

### 4. 错误处理

```go
result, err := cachedLoader(ctx)
if err != nil {
    // 记录日志
    log.WithError(err).WithField("key", cacheKey).Error("缓存加载失败")
    // 返回业务错误
    return nil, fmt.Errorf("获取数据失败: %w", err)
}
```

### 5. 缓存一致性

```go
// 写操作后清除相关缓存
func (s *UserService) UpdateUser(ctx context.Context, user *User) error {
    // 1. 更新数据库
    if err := s.db.UpdateUser(ctx, user); err != nil {
        return err
    }
    
    // 2. 清除相关缓存
    cacheKeys := []string{
        GetUserCacheKey(user.ID),
        fmt.Sprintf("users:page:*"), // 清除分页缓存
    }
    
    for _, key := range cacheKeys {
        s.redisClient.Del(ctx, key)
    }
    
    return nil
}
```

---

## 故障排查

### 问题 1：缓存未生效

**症状：** 每次都从数据源加载

**可能原因：**

- 每次都设置了 `WithForceRefresh(true)`
- Redis 连接失败

**解决方案：**

```go
// ❌ 错误：总是强制刷新
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.WithForceRefresh(true), // 每次都刷新
)

// ✅ 正确：条件性刷新
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.When(shouldRefresh, cachex.WithForceRefresh(true)),
)
```

### 问题 2：性能下降

**症状：** 缓存更新慢，影响响应时间

**可能原因：**

- 同步更新阻塞
- 压缩大对象耗时

**解决方案：**

```go
// ❌ 错误：大数据同步更新
cachex.CacheWrapper(client, key, largeDataLoader, ttl)

// ✅ 正确：异步更新
cachex.CacheWrapper(client, key, largeDataLoader, ttl,
    cachex.WithAsyncUpdate(),
)
```

### 问题 3：内存占用高

**症状：** Redis 内存使用率高

**可能原因：**

- 缓存了大量大对象
- 压缩未生效

**解决方案：**

```go
// ❌ 错误：禁用压缩
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.WithoutCompression(), // 大数据不压缩
)

// ✅ 正确：启用压缩（默认）
cachex.CacheWrapper(client, key, loader, ttl)

// 或缩短 TTL
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.WithTTL(time.Minute * 10),
)
```

### 问题 4：数据不一致

**症状：** 缓存数据与数据库不一致

**可能原因：**

- 缓存 TTL 设置过长
- 数据更新时未刷新缓存

**解决方案：**

```go
// 缩短 TTL
cachex.CacheWrapper(client, key, loader, ttl,
    cachex.WithTTL(time.Minute * 5),
)

// 或在数据更新时主动刷新
func UpdateData(ctx context.Context, data *Data) error {
    if err := db.Update(data); err != nil {
        return err
    }
    
    // 刷新缓存
    cachedLoader := cachex.CacheWrapper(client, key, loader, ttl,
        cachex.WithForceRefresh(true),
    )
    cachedLoader(ctx)
    
    return nil
}
```

---

## 架构设计

### Redis 连接配置

```go
func NewOptimizedRedisClient() *redis.Client {
    return redis.NewClient(&redis.Options{
        Addr:            "localhost:6379",
        DialTimeout:     10 * time.Second,
        ReadTimeout:     5 * time.Second,
        WriteTimeout:    5 * time.Second,
        PoolSize:        20,              // 连接池大小
        MinIdleConns:    5,               // 最小空闲连接
        MaxRetries:      3,               // 重试次数
        RetryDelay:      100 * time.Millisecond,
    })
}
```

### 缓存配置

```go
type CacheConfig struct {
    DefaultExpiration time.Duration
    MaxKeyLength      int
    CompressionLevel  int
}

var Config = CacheConfig{
    DefaultExpiration: time.Hour,
    MaxKeyLength:      250,
    CompressionLevel:  6, // Zlib压缩级别
}
```

---

## 监控与统计

### 缓存命中率统计

```go
type CacheStats struct {
    Hits   int64
    Misses int64
    Errors int64
}

func (s *CacheStats) HitRate() float64 {
    total := s.Hits + s.Misses
    if total == 0 {
        return 0
    }
    return float64(s.Hits) / float64(total)
}

// 使用
var stats CacheStats
// 在缓存操作时更新统计...
```

### 性能监控

```go
func CacheWrapperWithMetrics[T any](
    client *redis.Client,
    key string,
    loader cachex.CacheFunc[T],
    expiration time.Duration,
    opts ...cachex.CacheOption,
) cachex.CacheFunc[T] {
    return func(ctx context.Context) (T, error) {
        start := time.Now()
        defer func() {
            duration := time.Since(start)
            // 记录性能指标
            log.Printf("Cache operation for key %s took %v", key, duration)
        }()
        
        wrapped := cachex.CacheWrapper(client, key, loader, expiration, opts...)
        return wrapped(ctx)
    }
}
```

---

## 快速参考

### 选项速查表

| 选项 | 用途 | 示例 |
|------|------|------|
| `WithForceRefresh(true)` | 强制从数据源刷新 | `cachex.WithForceRefresh(true)` |
| `WithTTL(duration)` | 自定义过期时间 | `cachex.WithTTL(time.Hour * 2)` |
| `WithoutCompression()` | 跳过数据压缩 | `cachex.WithoutCompression()` |
| `WithAsyncUpdate()` | 异步更新缓存 | `cachex.WithAsyncUpdate()` |
| `WithRetry(times)` | Redis 失败重试 | `cachex.WithRetry(3)` |

### 条件构建器速查表

| 函数 | 用途 | 示例 |
|------|------|------|
| `When(cond, opt)` | 条件选项 | `When(isVIP, WithTTL(time.Hour*24))` |
| `WhenThen(cond, then, else)` | 二选一 | `WhenThen(isVIP, opt1, opt2)` |
| `Match(cases, default)` | 多条件匹配 | `Match([]Case{...}, defaultOpt)` |
| `Combine(opts...)` | 组合选项 | `Combine(opt1, opt2, opt3)` |

---

## 总结

`CacheWrapper` 提供了灵活且强大的缓存功能：

✅ **易用性** - 简单的 API，支持泛型  
✅ **灵活性** - 丰富的选项，函数式构建器  
✅ **性能** - 自动压缩、异步更新、重试机制  
✅ **可靠性** - 延迟双删、错误降级、重试保障  
✅ **可扩展** - 选项模式，易于添加新功能  
✅ **可维护** - 清晰的代码结构，函数式风格  

通过合理使用选项和函数式构建器，可以显著提升代码质量和系统性能！

---

## 相关文档

- [完整示例](./WRAPPER_EXAMPLES.md)
- [测试文件](../wrapper_test.go)
- [选项测试](../wrapper_options_test.go)
- [Redis 配置](../REDIS_CONFIG.md)
