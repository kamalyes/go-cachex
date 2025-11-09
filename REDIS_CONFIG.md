# Redis 配置指南

## 常见警告及解决方案

### MAINT_NOTIFICATIONS 警告

如果您看到这样的警告：
```
redis: auto mode fallback: maintnotifications disabled due to handshake error: ERR Unknown subcommand or wrong number of arguments for 'maint_notifications'. Try CLIENT HELP.
```

这是因为Redis客户端尝试使用Redis 6.2+的新功能，但您的Redis服务器版本较低不支持该功能。

### 解决方案

有几种方式可以解决这个问题：

#### 1. 使用推荐的配置（推荐）

```go
// 使用便捷函数创建Handler，自动设置推荐配置
handler, err := cachex.NewRedisHandlerSimple("localhost:6379", "password", 0)
if err != nil {
    log.Fatal(err)
}
```

#### 2. 手动配置Redis选项

```go
import "github.com/redis/go-redis/v9"

// 手动创建配置
cfg := &redis.Options{
    Addr:     "localhost:6379",
    Password: "your-password",
    DB:       0,
    // 关键：禁用身份设置以避免警告
    DisableIdentity: true,
    // 其他推荐设置
    DialTimeout:     5 * time.Second,
    ReadTimeout:     3 * time.Second,
    WriteTimeout:    3 * time.Second,
    PoolSize:        10,
    MinIdleConns:    5,
    ConnMaxIdleTime: 30 * time.Minute,
}

handler, err := cachex.NewRedisHandler(cfg)
if err != nil {
    log.Fatal(err)
}
```

#### 3. 使用推荐配置函数

```go
// 创建推荐配置
cfg := cachex.NewRedisOptions("localhost:6379", "password", 0)

// 可以进一步自定义配置
cfg.PoolSize = 20
cfg.ReadTimeout = 5 * time.Second

// 创建Handler
handler, err := cachex.NewRedisHandler(cfg)
if err != nil {
    log.Fatal(err)
}
```

### 配置说明

- `DisableIdentity: true` - 禁用CLIENT SETINFO命令，避免MAINT_NOTIFICATIONS相关警告
- `DialTimeout` - 连接超时时间
- `ReadTimeout/WriteTimeout` - 读写超时时间
- `PoolSize` - 连接池大小
- `MinIdleConns` - 最小空闲连接数
- `ConnMaxIdleTime` - 连接最大空闲时间

### 分布式锁功能

Redis Handler的GetOrCompute方法实现了分布式锁，确保在多个实例中只有一个会执行计算：

```go
result, err := handler.GetOrCompute(
    []byte("expensive-key"),
    5*time.Minute, // TTL
    func() ([]byte, error) {
        // 昂贵的计算操作
        return computeExpensiveValue(), nil
    },
)
```

这在分布式环境中可以避免重复计算，提高性能。