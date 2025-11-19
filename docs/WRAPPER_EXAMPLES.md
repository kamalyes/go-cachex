# CacheWrapper 使用示例

## 快速开始

### 基础字符串缓存

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
    defer client.Close()

    // 创建字符串数据加载器
    stringLoader := cachex.CacheWrapper(client, "hello_key", 
        func(ctx context.Context) (string, error) {
            fmt.Println("Loading data from source...")
            return "Hello, World!", nil
        }, 
        time.Minute,
    )

    ctx := context.Background()
    
    // 第一次调用 - 从数据源加载
    result1, _ := stringLoader(ctx)
    fmt.Println("First call:", result1)
    
    // 第二次调用 - 从缓存获取
    result2, _ := stringLoader(ctx)
    fmt.Println("Second call:", result2)
}
```

### 结构体缓存

```go
type User struct {
    ID   int    `json:"id"`
    Name string `json:"name"`
    Age  int    `json:"age"`
}

func ExampleStructCache() {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer client.Close()

    userLoader := cachex.CacheWrapper(client, "user:123",
        func(ctx context.Context) (*User, error) {
            // 模拟数据库查询
            fmt.Println("Querying database...")
            return &User{
                ID:   123,
                Name: "Alice",
                Age:  25,
            }, nil
        },
        time.Hour,
    )

    ctx := context.Background()
    user, err := userLoader(ctx)
    if err != nil {
        panic(err)
    }
    
    fmt.Printf("User: %+v\n", user)
}
```

### 切片数据缓存

```go
func ExampleSliceCache() {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer client.Close()

    listLoader := cachex.CacheWrapper(client, "items_list",
        func(ctx context.Context) ([]string, error) {
            fmt.Println("Loading items...")
            return []string{"item1", "item2", "item3"}, nil
        },
        30*time.Minute,
    )

    ctx := context.Background()
    items, _ := listLoader(ctx)
    fmt.Println("Items:", items)
}
```

### 映射数据缓存

```go
func ExampleMapCache() {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer client.Close()

    configLoader := cachex.CacheWrapper(client, "app_config",
        func(ctx context.Context) (map[string]string, error) {
            fmt.Println("Loading configuration...")
            return map[string]string{
                "theme": "dark",
                "lang":  "en",
                "debug": "false",
            }, nil
        },
        time.Hour*24,
    )

    ctx := context.Background()
    config, _ := configLoader(ctx)
    fmt.Println("Config:", config)
}
```

## 数据库集成示例

### 用户服务示例

```go
package service

import (
    "context"
    "database/sql"
    "fmt"
    "time"
    "github.com/redis/go-redis/v9"
    "github.com/kamalyes/go-cachex"
)

type UserService struct {
    db     *sql.DB
    client *redis.Client
}

type User struct {
    ID       int       `json:"id"`
    Name     string    `json:"name"`
    Email    string    `json:"email"`
    CreateAt time.Time `json:"create_at"`
}

func NewUserService(db *sql.DB, client *redis.Client) *UserService {
    return &UserService{db: db, client: client}
}

// GetUser 获取用户信息（带缓存）
func (s *UserService) GetUser(ctx context.Context, userID int) (*User, error) {
    cacheKey := fmt.Sprintf("user:%d", userID)
    
    loader := cachex.CacheWrapper(s.client, cacheKey,
        func(ctx context.Context) (*User, error) {
            return s.getUserFromDB(ctx, userID)
        },
        time.Hour, // 缓存1小时
    )
    
    return loader(ctx)
}

// getUserFromDB 从数据库查询用户信息
func (s *UserService) getUserFromDB(ctx context.Context, userID int) (*User, error) {
    var user User
    query := `SELECT id, name, email, created_at FROM users WHERE id = ?`
    
    err := s.db.QueryRowContext(ctx, query, userID).Scan(
        &user.ID, &user.Name, &user.Email, &user.CreateAt,
    )
    
    if err != nil {
        return nil, fmt.Errorf("failed to query user: %w", err)
    }
    
    return &user, nil
}

// GetUsersByPage 分页获取用户列表（带缓存）
func (s *UserService) GetUsersByPage(ctx context.Context, page, size int) ([]*User, error) {
    cacheKey := fmt.Sprintf("users:page:%d:size:%d", page, size)
    
    loader := cachex.CacheWrapper(s.client, cacheKey,
        func(ctx context.Context) ([]*User, error) {
            return s.getUsersFromDB(ctx, page, size)
        },
        time.Minute*15, // 缓存15分钟
    )
    
    return loader(ctx)
}

func (s *UserService) getUsersFromDB(ctx context.Context, page, size int) ([]*User, error) {
    offset := (page - 1) * size
    query := `SELECT id, name, email, created_at FROM users LIMIT ? OFFSET ?`
    
    rows, err := s.db.QueryContext(ctx, query, size, offset)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    
    var users []*User
    for rows.Next() {
        var user User
        err := rows.Scan(&user.ID, &user.Name, &user.Email, &user.CreateAt)
        if err != nil {
            return nil, err
        }
        users = append(users, &user)
    }
    
    return users, rows.Err()
}
```

### API响应缓存

```go
package api

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"
    "github.com/redis/go-redis/v9"
    "github.com/kamalyes/go-cachex"
)

type WeatherService struct {
    client     *redis.Client
    httpClient *http.Client
    apiKey     string
}

type WeatherData struct {
    City        string  `json:"city"`
    Temperature float64 `json:"temperature"`
    Humidity    int     `json:"humidity"`
    Description string  `json:"description"`
    Timestamp   int64   `json:"timestamp"`
}

func NewWeatherService(client *redis.Client, apiKey string) *WeatherService {
    return &WeatherService{
        client:     client,
        httpClient: &http.Client{Timeout: 10 * time.Second},
        apiKey:     apiKey,
    }
}

// GetWeather 获取天气信息（带缓存）
func (s *WeatherService) GetWeather(ctx context.Context, city string) (*WeatherData, error) {
    cacheKey := fmt.Sprintf("weather:%s", city)
    
    loader := cachex.CacheWrapper(s.client, cacheKey,
        func(ctx context.Context) (*WeatherData, error) {
            return s.fetchWeatherFromAPI(ctx, city)
        },
        time.Minute*15, // 天气数据缓存15分钟
    )
    
    return loader(ctx)
}

func (s *WeatherService) fetchWeatherFromAPI(ctx context.Context, city string) (*WeatherData, error) {
    url := fmt.Sprintf("https://api.weather.com/v1/current?key=%s&q=%s", s.apiKey, city)
    
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return nil, err
    }
    
    resp, err := s.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var apiResponse struct {
        Current struct {
            TempC       float64 `json:"temp_c"`
            Humidity    int     `json:"humidity"`
            Condition   struct {
                Text string `json:"text"`
            } `json:"condition"`
        } `json:"current"`
        Location struct {
            Name string `json:"name"`
        } `json:"location"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
        return nil, err
    }
    
    return &WeatherData{
        City:        apiResponse.Location.Name,
        Temperature: apiResponse.Current.TempC,
        Humidity:    apiResponse.Current.Humidity,
        Description: apiResponse.Current.Condition.Text,
        Timestamp:   time.Now().Unix(),
    }, nil
}
```

## 计算密集型任务缓存

```go
package compute

import (
    "context"
    "fmt"
    "time"
    "github.com/redis/go-redis/v9"
    "github.com/kamalyes/go-cachex"
)

type ComputeService struct {
    client *redis.Client
}

func NewComputeService(client *redis.Client) *ComputeService {
    return &ComputeService{client: client}
}

// Fibonacci 斐波那契数列计算（带缓存）
func (s *ComputeService) Fibonacci(ctx context.Context, n int) (int64, error) {
    cacheKey := fmt.Sprintf("fib:%d", n)
    
    loader := cachex.CacheWrapper(s.client, cacheKey,
        func(ctx context.Context) (int64, error) {
            fmt.Printf("Computing fibonacci(%d)...\n", n)
            return s.computeFibonacci(n), nil
        },
        time.Hour*24, // 计算结果缓存24小时
    )
    
    return loader(ctx)
}

func (s *ComputeService) computeFibonacci(n int) int64 {
    if n <= 1 {
        return int64(n)
    }
    
    a, b := int64(0), int64(1)
    for i := 2; i <= n; i++ {
        a, b = b, a+b
    }
    return b
}

// PrimeFactors 质因数分解（带缓存）
func (s *ComputeService) PrimeFactors(ctx context.Context, n int64) ([]int64, error) {
    cacheKey := fmt.Sprintf("prime_factors:%d", n)
    
    loader := cachex.CacheWrapper(s.client, cacheKey,
        func(ctx context.Context) ([]int64, error) {
            fmt.Printf("Computing prime factors of %d...\n", n)
            return s.computePrimeFactors(n), nil
        },
        time.Hour*12, // 质因数缓存12小时
    )
    
    return loader(ctx)
}

func (s *ComputeService) computePrimeFactors(n int64) []int64 {
    var factors []int64
    
    // 处理2的因子
    for n%2 == 0 {
        factors = append(factors, 2)
        n = n / 2
    }
    
    // 处理奇数因子
    for i := int64(3); i*i <= n; i += 2 {
        for n%i == 0 {
            factors = append(factors, i)
            n = n / i
        }
    }
    
    // 如果n是大于2的质数
    if n > 2 {
        factors = append(factors, n)
    }
    
    return factors
}
```

## 错误处理示例

```go
package examples

import (
    "context"
    "errors"
    "fmt"
    "time"
    "github.com/redis/go-redis/v9"
    "github.com/kamalyes/go-cachex"
)

// 带降级的数据加载
func ExampleWithFallback() {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer client.Close()

    dataLoader := cachex.CacheWrapper(client, "fallback_data",
        func(ctx context.Context) (string, error) {
            // 尝试主数据源
            data, err := loadFromPrimarySource(ctx)
            if err != nil {
                fmt.Println("Primary source failed, trying fallback...")
                // 使用备用数据源
                return loadFromFallbackSource(ctx)
            }
            return data, nil
        },
        time.Minute*10,
    )

    ctx := context.Background()
    result, err := dataLoader(ctx)
    if err != nil {
        fmt.Printf("All sources failed: %v\n", err)
        return
    }
    
    fmt.Println("Result:", result)
}

func loadFromPrimarySource(ctx context.Context) (string, error) {
    // 模拟主数据源故障
    return "", errors.New("primary source unavailable")
}

func loadFromFallbackSource(ctx context.Context) (string, error) {
    // 模拟备用数据源
    return "fallback data", nil
}

// 重试机制示例
func ExampleWithRetry() {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer client.Close()

    retryLoader := cachex.CacheWrapper(client, "retry_data",
        func(ctx context.Context) (string, error) {
            return loadDataWithRetry(ctx, 3) // 最多重试3次
        },
        time.Minute*5,
    )

    ctx := context.Background()
    result, err := retryLoader(ctx)
    if err != nil {
        fmt.Printf("Failed after retries: %v\n", err)
        return
    }
    
    fmt.Println("Result:", result)
}

func loadDataWithRetry(ctx context.Context, maxRetries int) (string, error) {
    var lastErr error
    
    for i := 0; i < maxRetries; i++ {
        select {
        case <-ctx.Done():
            return "", ctx.Err()
        default:
        }
        
        data, err := attemptLoad(ctx)
        if err == nil {
            return data, nil
        }
        
        lastErr = err
        if i < maxRetries-1 {
            fmt.Printf("Attempt %d failed: %v, retrying...\n", i+1, err)
            time.Sleep(time.Second * time.Duration(i+1))
        }
    }
    
    return "", fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}

func attemptLoad(ctx context.Context) (string, error) {
    // 模拟不稳定的数据源
    if time.Now().UnixNano()%3 == 0 {
        return "success data", nil
    }
    return "", errors.New("temporary failure")
}
```

## 并发访问示例

```go
package examples

import (
    "context"
    "fmt"
    "sync"
    "time"
    "github.com/redis/go-redis/v9"
    "github.com/kamalyes/go-cachex"
)

func ExampleConcurrentAccess() {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer client.Close()

    // 创建一个慢数据加载器
    slowLoader := cachex.CacheWrapper(client, "concurrent_data",
        func(ctx context.Context) (string, error) {
            fmt.Println("Loading data (slow operation)...")
            time.Sleep(2 * time.Second) // 模拟慢操作
            return fmt.Sprintf("data_loaded_at_%d", time.Now().Unix()), nil
        },
        time.Minute,
    )

    ctx := context.Background()
    var wg sync.WaitGroup
    
    // 并发启动10个goroutine
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            
            start := time.Now()
            result, err := slowLoader(ctx)
            duration := time.Since(start)
            
            if err != nil {
                fmt.Printf("Goroutine %d failed: %v\n", id, err)
                return
            }
            
            fmt.Printf("Goroutine %d got result: %s (took %v)\n", 
                id, result, duration)
        }(i)
    }
    
    wg.Wait()
    fmt.Println("All goroutines completed")
}
```

## 监控和统计示例

```go
package examples

import (
    "context"
    "fmt"
    "sync/atomic"
    "time"
    "github.com/redis/go-redis/v9"
    "github.com/kamalyes/go-cachex"
)

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

func ExampleWithStats() {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer client.Close()

    var stats CacheStats
    
    // 包装器用于统计
    loader := func(ctx context.Context) (string, error) {
        // 先检查缓存状态
        _, err := client.Get(ctx, "stats_key").Result()
        if err == nil {
            atomic.AddInt64(&stats.Hits, 1)
        } else if err == redis.Nil {
            atomic.AddInt64(&stats.Misses, 1)
        } else {
            atomic.AddInt64(&stats.Errors, 1)
        }
        
        // 创建实际的缓存加载器
        cacheLoader := cachex.CacheWrapper(client, "stats_key",
            func(ctx context.Context) (string, error) {
                return fmt.Sprintf("data_%d", time.Now().Unix()), nil
            },
            time.Minute,
        )
        
        return cacheLoader(ctx)
    }

    ctx := context.Background()
    
    // 多次调用以产生统计数据
    for i := 0; i < 20; i++ {
        _, err := loader(ctx)
        if err != nil {
            fmt.Printf("Call %d failed: %v\n", i, err)
        }
        
        if i%5 == 4 {
            // 每5次调用清除一次缓存以产生miss
            client.Del(ctx, "stats_key")
        }
        
        time.Sleep(100 * time.Millisecond)
    }
    
    // 打印统计信息
    fmt.Printf("Cache Statistics:\n")
    fmt.Printf("  Hits: %d\n", stats.Hits)
    fmt.Printf("  Misses: %d\n", stats.Misses)
    fmt.Printf("  Errors: %d\n", stats.Errors)
    fmt.Printf("  Hit Rate: %.2f%%\n", stats.HitRate()*100)
}
```

这些示例展示了CacheWrapper的各种使用场景，从基础用法到复杂的生产环境应用。每个示例都展示了不同的功能特性和最佳实践。