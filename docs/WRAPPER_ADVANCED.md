# 缓存包装器 (CacheWrapper) 高级使用指南

## 🏗️ 架构设计

### 核心组件架构

```bash
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

## 🚀 快速入门

### 基础用法

```go
package main

import (
    "context"
    "fmt"
    "time"
    "github.com/redis/go-redis/v9"
    "github.com/kamalyes/go-cachex"
)

func main() {
    // 创建Redis客户端
    client := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })

    // 定义数据加载函数
    userLoader := func(ctx context.Context) (*User, error) {
        // 模拟从数据库加载用户数据
        return &User{
            ID:   1,
            Name: "Alice",
            Age:  25,
        }, nil
    }

    // 创建缓存包装器
    cachedUserLoader := cachex.CacheWrapper(
        client,
        "user:1",           // 缓存键
        userLoader,         // 数据加载函数
        time.Hour,         // 缓存过期时间
    )

    // 使用缓存
    ctx := context.Background()
    user, err := cachedUserLoader(ctx)
    if err != nil {
        panic(err)
    }
    
    fmt.Printf("User: %+v\n", user)
}

type User struct {
    ID   int    `json:"id"`
    Name string `json:"name"`
    Age  int    `json:"age"`
}
```

## 📊 功能特性

### 1. 泛型支持

CacheWrapper 支持任意类型的数据缓存：

```go
// 字符串缓存
stringLoader := cachex.CacheWrapper(client, "string_key", 
    func(ctx context.Context) (string, error) {
        return "Hello, World!", nil
    }, time.Minute)

// 整数缓存
intLoader := cachex.CacheWrapper(client, "int_key",
    func(ctx context.Context) (int, error) {
        return 42, nil
    }, time.Minute)

// 切片缓存
sliceLoader := cachex.CacheWrapper(client, "slice_key",
    func(ctx context.Context) ([]string, error) {
        return []string{"a", "b", "c"}, nil
    }, time.Minute)

// 映射缓存
mapLoader := cachex.CacheWrapper(client, "map_key",
    func(ctx context.Context) (map[string]int, error) {
        return map[string]int{"a": 1, "b": 2}, nil
    }, time.Minute)
```

### 2. 数据压缩

自动使用Zlib压缩算法减少Redis内存使用：

```go
// 大数据缓存示例
largeDataLoader := cachex.CacheWrapper(client, "large_data",
    func(ctx context.Context) ([]byte, error) {
        // 返回1MB的数据
        return make([]byte, 1024*1024), nil
    }, time.Hour)

// 数据会自动压缩存储到Redis
data, err := largeDataLoader(ctx)
```

### 3. 错误处理

优雅处理各种错误情况：

```go
// 网络错误处理
errorLoader := cachex.CacheWrapper(client, "error_key",
    func(ctx context.Context) (string, error) {
        // 模拟可能出现的错误
        return "", fmt.Errorf("database connection failed")
    }, time.Minute)

result, err := errorLoader(ctx)
if err != nil {
    // 错误会被正确传递
    log.Printf("Error: %v", err)
}
```

### 4. 并发安全

支持高并发访问：

```go
// 并发测试
loader := cachex.CacheWrapper(client, "concurrent_key",
    func(ctx context.Context) (string, error) {
        time.Sleep(100 * time.Millisecond) // 模拟慢查询
        return "shared_data", nil
    }, time.Minute)

// 多个goroutine同时访问
for i := 0; i < 10; i++ {
    go func() {
        result, _ := loader(ctx)
        fmt.Println(result)
    }()
}
```

## 🎯 高级用法

### 1. 数据库查询缓存

```go
type UserService struct {
    db     *sql.DB
    client *redis.Client
}

func (s *UserService) GetUser(ctx context.Context, userID int) (*User, error) {
    loader := cachex.CacheWrapper(s.client, 
        fmt.Sprintf("user:%d", userID),
        func(ctx context.Context) (*User, error) {
            // 实际的数据库查询
            return s.queryUserFromDB(ctx, userID)
        },
        time.Hour,
    )
    
    return loader(ctx)
}

func (s *UserService) queryUserFromDB(ctx context.Context, userID int) (*User, error) {
    var user User
    err := s.db.QueryRowContext(ctx, 
        "SELECT id, name, age FROM users WHERE id = ?", userID).
        Scan(&user.ID, &user.Name, &user.Age)
    return &user, err
}
```

### 2. API响应缓存

```go
type APIService struct {
    client     *redis.Client
    httpClient *http.Client
}

func (s *APIService) GetWeather(ctx context.Context, city string) (*WeatherData, error) {
    loader := cachex.CacheWrapper(s.client,
        fmt.Sprintf("weather:%s", city),
        func(ctx context.Context) (*WeatherData, error) {
            return s.fetchWeatherFromAPI(ctx, city)
        },
        15*time.Minute, // 天气数据缓存15分钟
    )
    
    return loader(ctx)
}

type WeatherData struct {
    City        string  `json:"city"`
    Temperature float64 `json:"temperature"`
    Humidity    int     `json:"humidity"`
    Description string  `json:"description"`
}
```

### 3. 计算结果缓存

```go
func ExpensiveCalculation(client *redis.Client, input int) func(context.Context) (int, error) {
    return cachex.CacheWrapper(client,
        fmt.Sprintf("calc:%d", input),
        func(ctx context.Context) (int, error) {
            // 模拟复杂计算
            time.Sleep(2 * time.Second)
            result := fibonacci(input)
            return result, nil
        },
        time.Hour,
    )
}

func fibonacci(n int) int {
    if n <= 1 {
        return n
    }
    return fibonacci(n-1) + fibonacci(n-2)
}
```

### 4. 分页数据缓存

```go
type PaginatedData[T any] struct {
    Data       []T   `json:"data"`
    TotalCount int   `json:"total_count"`
    Page       int   `json:"page"`
    PageSize   int   `json:"page_size"`
}

func (s *UserService) GetUsersPaginated(ctx context.Context, page, pageSize int) (*PaginatedData[User], error) {
    loader := cachex.CacheWrapper(s.client,
        fmt.Sprintf("users:page:%d:size:%d", page, pageSize),
        func(ctx context.Context) (*PaginatedData[User], error) {
            return s.queryUsersFromDB(ctx, page, pageSize)
        },
        30*time.Minute,
    )
    
    return loader(ctx)
}
```

## ⚡ 性能优化

### 1. 缓存键设计

```go
// 好的键设计
fmt.Sprintf("user:%d", userID)
fmt.Sprintf("product:%d:category:%s", productID, category)
fmt.Sprintf("search:%s:page:%d", query, page)

// 避免的键设计
"user_data_" + string(userID) // 字符串拼接效率低
fmt.Sprintf("data_%v", complexObject) // 复杂对象作为键
```

### 2. 过期时间策略

```go
// 根据数据特性设置不同的过期时间
var (
    UserCacheExpiration     = time.Hour * 24    // 用户数据：24小时
    ProductCacheExpiration  = time.Hour * 6     // 商品数据：6小时
    SearchCacheExpiration   = time.Minute * 15  // 搜索结果：15分钟
    ConfigCacheExpiration   = time.Hour * 72    // 配置数据：72小时
)
```

### 3. 缓存预热

```go
func (s *UserService) WarmupCache(ctx context.Context, userIDs []int) error {
    for _, userID := range userIDs {
        go func(id int) {
            // 异步预热缓存
            _, _ = s.GetUser(ctx, id)
        }(userID)
    }
    return nil
}
```

## 📈 监控和调试

### 1. 缓存命中率统计

```go
type CacheStats struct {
    Hits   int64
    Misses int64
    Errors int64
}

var stats CacheStats

func CacheWrapperWithStats[T any](client *redis.Client, key string, 
    loader cachex.CacheFunc[T], expiration time.Duration) cachex.CacheFunc[T] {
    
    return func(ctx context.Context) (T, error) {
        // 先尝试从缓存获取
        cachedData, err := client.Get(ctx, key).Result()
        if err == nil {
            atomic.AddInt64(&stats.Hits, 1)
            // 处理缓存数据...
        } else if err == redis.Nil {
            atomic.AddInt64(&stats.Misses, 1)
        } else {
            atomic.AddInt64(&stats.Errors, 1)
        }
        
        // 调用原始包装器
        wrapped := cachex.CacheWrapper(client, key, loader, expiration)
        return wrapped(ctx)
    }
}
```

### 2. 性能监控

```go
func CacheWrapperWithMetrics[T any](client *redis.Client, key string,
    loader cachex.CacheFunc[T], expiration time.Duration) cachex.CacheFunc[T] {
    
    return func(ctx context.Context) (T, error) {
        start := time.Now()
        defer func() {
            duration := time.Since(start)
            // 记录性能指标
            log.Printf("Cache operation for key %s took %v", key, duration)
        }()
        
        wrapped := cachex.CacheWrapper(client, key, loader, expiration)
        return wrapped(ctx)
    }
}
```

## 🔧 配置优化

### 1. Redis连接配置

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

### 2. 环境配置

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

## 🚨 注意事项

### 1. 内存管理

```go
// 避免缓存过大的对象
func (s *Service) GetLargeData(ctx context.Context) ([]byte, error) {
    loader := cachex.CacheWrapper(s.client, "large_data",
        func(ctx context.Context) ([]byte, error) {
            data := make([]byte, 10*1024*1024) // 10MB数据
            return data, nil
        },
        time.Minute*5, // 较短的过期时间
    )
    
    return loader(ctx)
}
```

### 2. 错误处理

```go
// 适当的错误处理
func (s *Service) GetDataWithFallback(ctx context.Context) (string, error) {
    loader := cachex.CacheWrapper(s.client, "fallback_key",
        func(ctx context.Context) (string, error) {
            data, err := s.primaryDataSource(ctx)
            if err != nil {
                // 使用备用数据源
                return s.fallbackDataSource(ctx)
            }
            return data, nil
        },
        time.Minute*10,
    )
    
    return loader(ctx)
}
```

### 3. 缓存一致性

```go
// 写操作后清除相关缓存
func (s *UserService) UpdateUser(ctx context.Context, user *User) error {
    err := s.updateUserInDB(ctx, user)
    if err != nil {
        return err
    }
    
    // 清除相关缓存
    cacheKeys := []string{
        fmt.Sprintf("user:%d", user.ID),
        fmt.Sprintf("users:page:*"), // 清除分页缓存
    }
    
    for _, key := range cacheKeys {
        s.client.Del(ctx, key)
    }
    
    return nil
}
```

## 📋 最佳实践

1. **合理设置过期时间**：根据数据更新频率设置合适的过期时间
2. **键名规范**：使用清晰的命名规范，便于管理和调试
3. **错误处理**：始终处理缓存可能出现的各种错误
4. **监控缓存命中率**：定期监控和优化缓存效果
5. **避免缓存穿透**：对null值也进行适当缓存
6. **内存控制**：避免缓存过大的对象
7. **版本兼容性**：考虑数据结构变更时的兼容性

## 🔍 故障排查

### 常见问题

1. **缓存不生效**：检查Redis连接和键名是否正确
2. **内存占用过高**：检查缓存的对象大小和过期时间设置
3. **序列化错误**：确保缓存的数据类型支持JSON序列化
4. **并发问题**：延迟双删策略可能导致临时的缓存不一致，这是正常现象

### 调试技巧

```go
// 添加调试日志
func CacheWrapperWithDebug[T any](client *redis.Client, key string,
    loader cachex.CacheFunc[T], expiration time.Duration) cachex.CacheFunc[T] {
    
    return func(ctx context.Context) (T, error) {
        log.Printf("Cache operation started for key: %s", key)
        
        wrapped := cachex.CacheWrapper(client, key, loader, expiration)
        result, err := wrapped(ctx)
        
        if err != nil {
            log.Printf("Cache operation failed for key %s: %v", key, err)
        } else {
            log.Printf("Cache operation succeeded for key: %s", key)
        }
        
        return result, err
    }
}
```