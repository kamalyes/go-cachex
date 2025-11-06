<!--
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:37:05
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 22:51:56
 * @FilePath: \go-cachex\README-ZH.md
 * @Description: 
 * 
 * Copyright (c) 2025 by kamalyes, All Rights Reserved. 
-->
# Go-Cachex

> Go-Cachex 是一个全面的缓存库，提供多种缓存实现和适配器，支持 TTL、LRU 驱逐、并发安全和上下文感知等特性。

[![stable](https://img.shields.io/badge/stable-stable-green.svg)](https://github.com/kamalyes/go-cachex)
[![license](https://img.shields.io/github/license/kamalyes/go-cachex)]()
[![download](https://img.shields.io/github/downloads/kamalyes/go-cachex/total)]()
[![release](https://img.shields.io/github/v/release/kamalyes/go-cachex)]()
[![commit](https://img.shields.io/github/last-commit/kamalyes/go-cachex)]()
[![issues](https://img.shields.io/github/issues/kamalyes/go-cachex)]()
[![pull](https://img.shields.io/github/issues-pr/kamalyes/go-cachex)]()
[![fork](https://img.shields.io/github/forks/kamalyes/go-cachex)]()
[![star](https://img.shields.io/github/stars/kamalyes/go-cachex)]()
[![go](https://img.shields.io/github/go-mod/go-version/kamalyes/go-cachex)]()
[![size](https://img.shields.io/github/repo-size/kamalyes/go-cachex)]()
[![contributors](https://img.shields.io/github/contributors/kamalyes/go-cachex)]()
[![codecov](https://codecov.io/gh/kamalyes/go-cachex/branch/master/graph/badge.svg)](https://codecov.io/gh/kamalyes/go-cachex)
[![Go Report Card](https://goreportcard.com/badge/github.com/kamalyes/go-cachex)](https://goreportcard.com/report/github.com/kamalyes/go-cachex)
[![Go Reference](https://pkg.go.dev/badge/github.com/kamalyes/go-cachex?status.svg)](https://pkg.go.dev/github.com/kamalyes/go-cachex?tab=doc)
[![Sourcegraph](https://sourcegraph.com/github.com/kamalyes/go-cachex/-/badge.svg)](https://sourcegraph.com/github.com/kamalyes/go-cachex?badge)

## 功能特性

- 多种缓存实现：
  - 支持 TTL 的内存 LRU 缓存
  - 带自动清理的过期缓存
  - 基于 Redis 的分布式缓存
  - 高性能 Ristretto 缓存
  - 上下文感知缓存包装器
  - 用于提高并发性能的分片缓存
  - 用于分层缓存的两级缓存
- 所有实现的共同特性：
  - 线程安全操作
  - TTL（存活时间）支持
  - 字节切片键值对
  - 一致的错误处理
  - 可选的上下文支持
- 高级特性：
  - 并发加载去重
  - 上下文取消支持
  - 自动过期键清理
  - LRU/容量驱逐策略
  - Redis 集群支持

## 开始使用

### 环境要求

建议需要 [Go](https://go.dev/) 版本 [1.23](https://go.dev/doc/devel/release#go1.23.0) 或更高版本

### 安装

使用 [Go 的模块支持](https://go.dev/wiki/Modules#how-to-use-modules)，当您在代码中添加导入时，`go [build|run|test]` 将自动获取所需的依赖项：

```go
import "github.com/kamalyes/go-cachex"
```

或者，使用 `go get` 命令：

```sh
go get -u github.com/kamalyes/go-cachex
```

## 使用方法

### 通用接口

所有缓存实现都实现了 `Handler` 接口：

```go
type Handler interface {
    // Set 设置键值对
    Set(key, value []byte) error
    
    // Get 获取键对应的值
    Get(key []byte) ([]byte, error)
    
    // Del 删除键
    Del(key []byte) error
    
    // SetWithTTL 设置带 TTL 的键值对
    SetWithTTL(key, value []byte, ttl time.Duration) error
    
    // GetTTL 获取键的剩余 TTL
    GetTTL(key []byte) (time.Duration, error)
    
    // Close 关闭缓存
    Close() error
}
```

### LRU 缓存使用

```go
// 创建一个容量为 1000 的 LRU 缓存
cache := cachex.NewLRUHandler(1000)
defer cache.Close()

// 基本操作
err := cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))
err = cache.Del([]byte("key"))

// TTL 操作
err = cache.SetWithTTL([]byte("key-ttl"), []byte("value"), 5*time.Second)
ttl, err := cache.GetTTL([]byte("key-ttl"))
```

### 过期缓存使用

```go
// 创建一个过期缓存（自动清理过期键）
cache := cachex.NewExpiringHandler()
defer cache.Close()

// 基本操作与 TTL
err := cache.Set([]byte("key"), []byte("value"))
err = cache.SetWithTTL([]byte("temp"), []byte("value"), 30*time.Second)

// 过期键会自动清理
time.Sleep(31 * time.Second)
_, err = cache.Get([]byte("temp")) // 返回 ErrNotFound
```

### Ristretto 缓存使用

```go
// 创建高性能 Ristretto 缓存
config := &cachex.RistrettoConfig{
    NumCounters: 1e7,     // 预期的唯一键数量
    MaxCost:     1 << 30, // 最大内存使用量（字节）
    BufferItems: 64,      // 写入缓冲区大小
}
cache, err := cachex.NewRistrettoHandler(config)
if err != nil {
    log.Fatal(err)
}
defer cache.Close()

// 基本操作
err = cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))

// TTL 支持
err = cache.SetWithTTL([]byte("key-ttl"), []byte("value"), time.Minute)
```

### Redis 缓存使用

```go
// 创建 Redis 缓存（单节点）
cache, err := cachex.NewRedisHandler(&cachex.RedisConfig{
    Addrs: []string{"localhost:6379"},
})
if err != nil {
    log.Fatal(err)
}
defer cache.Close()

// Redis 集群配置
clusterCache, err := cachex.NewRedisHandler(&cachex.RedisConfig{
    Addrs: []string{
        "localhost:7000",
        "localhost:7001",
        "localhost:7002",
    },
    IsCluster: true,
})

// 基本操作
err = cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))
err = cache.Del([]byte("key"))

// 带 TTL 的操作
err = cache.SetWithTTL([]byte("key-ttl"), []byte("value"), 24*time.Hour)
ttl, err := cache.GetTTL([]byte("key-ttl"))
```

### 上下文感知缓存使用

```go
// 创建上下文感知缓存包装器
baseCache := cachex.NewRistrettoHandler(nil)
cache := cachex.NewCtxCache(baseCache)

// 基本上下文操作
ctx := context.Background()
ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
defer cancel()

// GetOrCompute - 并发请求去重
loader := func(ctx context.Context) ([]byte, error) {
    // 昂贵的计算或远程调用
    return []byte("computed"), nil
}
val, err := cache.GetOrCompute(ctx, []byte("key"), loader)

// WithCache - 在缓存中执行操作
err = cache.WithCache(ctx, []byte("key"), func(val []byte) error {
    // 使用缓存值的操作
    return nil
})
```

### 分片缓存使用

```go
// 创建分片缓存
factory := func() cachex.Handler {
    return cachex.NewLRUHandler(1000)
}
cache := cachex.NewShardedHandler(16, factory) // 16 个分片
defer cache.Close()

// 使用方式与普通缓存相同
err := cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))

// 键会自动分配到不同分片，提高并发性能
```

### 两级缓存使用

```go
// 创建两级缓存系统
l1 := cachex.NewLRUHandler(1000)         // 快速本地缓存
l2 := cachex.NewRedisHandler(redisConfig) // 慢速共享缓存

cache := cachex.NewTwoLevelHandler(l1, l2, &cachex.TwoLevelConfig{
    WriteStrategy: cachex.WriteThrough, // 写透策略
})
defer cache.Close()

// 自动处理两级缓存
err := cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key")) // 优先从 L1 获取，未命中则回退到 L2

// TTL 在两级中保持一致
err = cache.SetWithTTL([]byte("key"), []byte("value"), time.Hour)
```

### 错误处理最佳实践

```go
// 1. 检查特定错误类型
if err == cachex.ErrNotFound {
    // 处理键不存在的情况
}

// 2. 优雅降级
val, err := cache.Get([]byte("key"))
if err == cachex.ErrNotFound {
    // 从备用源加载数据
    val = loadFromBackup()
}

// 3. TTL 验证
if err == cachex.ErrInvalidTTL {
    // 使用默认 TTL
    err = cache.SetWithTTL(key, value, defaultTTL)
}

// 4. 容量检查
if err == cachex.ErrCapacityExceeded {
    // 执行清理或扩容操作
    cache.Del(oldKeys...)
}

// 5. 优雅关闭
defer func() {
    if err := cache.Close(); err != nil {
        log.Printf("关闭缓存时出错: %v", err)
    }
}()
```

### 上下文感知缓存

```go
// 创建上下文感知缓存包装器
ctx := context.Background()
base := cachex.NewRistrettoHandler(nil)
cache := cachex.NewCtxCache(base)

// 使用 GetOrCompute 来去重并发加载
val, err := cache.GetOrCompute(ctx, []byte("key"), func(ctx context.Context) ([]byte, error) {
    // 对于并发请求，此函数只会被调用一次
    return []byte("computed-value"), nil
})
```

### Redis 缓存

```go
// 创建基于 Redis 的缓存
redisCache, err := cachex.NewRedisHandler(&cachex.RedisConfig{
    Addrs: []string{"localhost:6379"},
})
if err != nil {
    log.Fatal(err)
}
defer redisCache.Close()

// 像使用其他缓存实现一样使用
err = redisCache.Set([]byte("key"), []byte("value"))
```

### 两级缓存

```go
// 创建 L1（快速）和 L2（慢速）缓存
l1 := cachex.NewLRUHandler(1000)
l2 := cachex.NewRedisHandler(&cachex.RedisConfig{
    Addrs: []string{"localhost:6379"},
})

// 创建两级缓存
cache := cachex.NewTwoLevelHandler(l1, l2, &cachex.TwoLevelConfig{
    WriteStrategy: cachex.WriteThrough,
})
defer cache.Close()

// 正常使用 - 它会自动处理两个级别
val, err := cache.Get([]byte("key"))
```

## 可用的缓存实现

### LRUHandler
- 简单的内存 LRU 缓存
- 支持 TTL
- 线程安全
- 适合本地缓存或测试

### ExpiringHandler
- 基于 map 的内存缓存
- 自动清理过期键
- 线程安全
- 适合临时数据缓存

### RistrettoHandler
- 基于 dgraph-io/ristretto 的高性能缓存
- 自动项目驱逐
- 内存受限
- 适合高并发生产环境使用

### RedisHandler
- 使用 Redis 的分布式缓存
- 支持 Redis 集群
- 持久化存储
- 适合分布式系统

### CtxCache
- 上下文感知缓存包装器
- 并发加载去重
- 支持取消
- 适合昂贵计算

### ShardedHandler
- 用于提高并发性能的分片缓存
- 使用多个底层缓存
- 减少锁竞争
- 适合高吞吐量场景

### TwoLevelHandler
- 分层缓存（如内存 + Redis）
- 不同的写入策略
- 自动 L1/L2 同步
- 适合优化访问模式

## 错误处理

该库为常见场景使用标准错误类型：

- `ErrNotFound`: 缓存中未找到键
- `ErrInvalidKey`: 无效或空键
- `ErrInvalidValue`: 无效或空值
- `ErrInvalidTTL`: 无效的 TTL 值
- `ErrClosed`: 缓存实例已关闭
- `ErrCapacityExceeded`: 超出缓存容量限制

## 贡献

欢迎贡献！请随时提交 Pull Request。
