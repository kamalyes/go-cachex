<!--
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 21:55:05
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 22:51:00
 * @FilePath: \go-cachex\README.md
 * @Description: 
 * 
 * Copyright (c) 2025 by kamalyes, All Rights Reserved. 
-->
# Go-Cachex
Chinese Documentation - [中文文档](./README-ZH.md)

> Go-Cachex is a comprehensive caching library that provides multiple cache implementations and adapters with features like TTL support, LRU eviction, concurrent access safety, and context awareness.

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

## Features

- Multiple cache implementations:
  - In-memory LRU cache with TTL support
  - Expiring cache with automatic cleanup
  - Redis-based distributed cache
  - High-performance Ristretto cache
  - Context-aware cache wrapper
  - Sharded cache for better concurrency
  - Two-level cache for hierarchical caching
- Common features across implementations:
  - Thread-safe operations
  - TTL (Time-To-Live) support
  - Byte slice key-value pairs
  - Consistent error handling
  - Optional context support
- Advanced features:
  - Deduplication of concurrent loads
  - Context cancellation support
  - Automatic expired key cleanup
  - LRU/capacity-based eviction
  - Redis cluster support

## Getting started

### Prerequisites

Requires [Go](https://go.dev/) version [1.23](https://go.dev/doc/devel/release#go1.23.0) or above.

### Installation

With [Go's module support](https://go.dev/wiki/Modules#how-to-use-modules), `go [build|run|test]` automatically fetches the necessary dependencies when you add the import in your code:

```go
import "github.com/kamalyes/go-cachex"
```

Alternatively, use `go get`:

```sh
go get -u github.com/kamalyes/go-cachex
```

## Usage

### Common Interface

All cache implementations implement the `Handler` interface:

```go
type Handler interface {
    // Set stores a key-value pair
    Set(key, value []byte) error
    
    // Get retrieves the value for a key
    Get(key []byte) ([]byte, error)
    
    // Del removes a key
    Del(key []byte) error
    
    // SetWithTTL stores a key-value pair with TTL
    SetWithTTL(key, value []byte, ttl time.Duration) error
    
    // GetTTL gets the remaining TTL for a key
    GetTTL(key []byte) (time.Duration, error)
    
    // Close shuts down the cache
    Close() error
}
```

### LRU Cache Usage

```go
// Create a new LRU cache with capacity of 1000
cache := cachex.NewLRUHandler(1000)
defer cache.Close()

// Basic operations
err := cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))
err = cache.Del([]byte("key"))

// TTL operations
err = cache.SetWithTTL([]byte("key-ttl"), []byte("value"), 5*time.Second)
ttl, err := cache.GetTTL([]byte("key-ttl"))
```

### Expiring Cache Usage

```go
// Create an expiring cache (auto-cleanup of expired keys)
cache := cachex.NewExpiringHandler()
defer cache.Close()

// Basic operations with TTL
err := cache.Set([]byte("key"), []byte("value"))
err = cache.SetWithTTL([]byte("temp"), []byte("value"), 30*time.Second)

// Expired keys are automatically cleaned up
time.Sleep(31 * time.Second)
_, err = cache.Get([]byte("temp")) // Returns ErrNotFound
```

### Ristretto Cache Usage

```go
// Create high-performance Ristretto cache
config := &cachex.RistrettoConfig{
    NumCounters: 1e7,     // Expected number of unique keys
    MaxCost:     1 << 30, // Maximum memory usage in bytes
    BufferItems: 64,      // Size of the write buffer
}
cache, err := cachex.NewRistrettoHandler(config)
if err != nil {
    log.Fatal(err)
}
defer cache.Close()

// Basic operations
err = cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))

// TTL support
err = cache.SetWithTTL([]byte("key-ttl"), []byte("value"), time.Minute)
```

### Redis Cache Usage

```go
// Create Redis cache (single node)
cache, err := cachex.NewRedisHandler(&cachex.RedisConfig{
    Addrs: []string{"localhost:6379"},
})
if err != nil {
    log.Fatal(err)
}
defer cache.Close()

// Redis cluster configuration
clusterCache, err := cachex.NewRedisHandler(&cachex.RedisConfig{
    Addrs: []string{
        "localhost:7000",
        "localhost:7001",
        "localhost:7002",
    },
    IsCluster: true,
})

// Basic operations
err = cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))
err = cache.Del([]byte("key"))

// Operations with TTL
err = cache.SetWithTTL([]byte("key-ttl"), []byte("value"), 24*time.Hour)
ttl, err := cache.GetTTL([]byte("key-ttl"))
```

### Context-Aware Cache Usage

```go
// Create context-aware cache wrapper
baseCache := cachex.NewRistrettoHandler(nil)
cache := cachex.NewCtxCache(baseCache)

// Basic context operations
ctx := context.Background()
ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
defer cancel()

// GetOrCompute - deduplicate concurrent requests
loader := func(ctx context.Context) ([]byte, error) {
    // Expensive computation or remote call
    return []byte("computed"), nil
}
val, err := cache.GetOrCompute(ctx, []byte("key"), loader)

// WithCache - operate with cached value
err = cache.WithCache(ctx, []byte("key"), func(val []byte) error {
    // Operations with cached value
    return nil
})
```

### Sharded Cache Usage

```go
// Create sharded cache
factory := func() cachex.Handler {
    return cachex.NewLRUHandler(1000)
}
cache := cachex.NewShardedHandler(16, factory) // 16 shards
defer cache.Close()

// Use like any other cache
err := cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key"))

// Keys are automatically distributed across shards for better concurrency
```

### Two-Level Cache Usage

```go
// Create two-level cache system
l1 := cachex.NewLRUHandler(1000)         // Fast local cache
l2 := cachex.NewRedisHandler(redisConfig) // Slow shared cache

cache := cachex.NewTwoLevelHandler(l1, l2, &cachex.TwoLevelConfig{
    WriteStrategy: cachex.WriteThrough, // Write-through strategy
})
defer cache.Close()

// Automatically handles both levels
err := cache.Set([]byte("key"), []byte("value"))
val, err := cache.Get([]byte("key")) // Tries L1 first, falls back to L2

// TTL is maintained across both levels
err = cache.SetWithTTL([]byte("key"), []byte("value"), time.Hour)
```

### Error Handling Best Practices

```go
// 1. Check for specific error types
if err == cachex.ErrNotFound {
    // Handle missing key case
}

// 2. Graceful degradation
val, err := cache.Get([]byte("key"))
if err == cachex.ErrNotFound {
    // Load from backup source
    val = loadFromBackup()
}

// 3. TTL validation
if err == cachex.ErrInvalidTTL {
    // Use default TTL
    err = cache.SetWithTTL(key, value, defaultTTL)
}

// 4. Capacity checks
if err == cachex.ErrCapacityExceeded {
    // Perform cleanup or expansion
    cache.Del(oldKeys...)
}

// 5. Graceful shutdown
defer func() {
    if err := cache.Close(); err != nil {
        log.Printf("Error closing cache: %v", err)
    }
}()
```

### Context-Aware Cache

```go
// Create a context-aware cache wrapper
ctx := context.Background()
base := cachex.NewRistrettoHandler(nil)
cache := cachex.NewCtxCache(base)

// Use GetOrCompute to deduplicate concurrent loads
val, err := cache.GetOrCompute(ctx, []byte("key"), func(ctx context.Context) ([]byte, error) {
    // This function will only be called once for concurrent requests
    return []byte("computed-value"), nil
})
```

### Redis Cache

```go
// Create a Redis-based cache
redisCache, err := cachex.NewRedisHandler(&cachex.RedisConfig{
    Addrs: []string{"localhost:6379"},
})
if err != nil {
    log.Fatal(err)
}
defer redisCache.Close()

// Use like any other cache implementation
err = redisCache.Set([]byte("key"), []byte("value"))
```

### Two-Level Cache

```go
// Create L1 (fast) and L2 (slow) caches
l1 := cachex.NewLRUHandler(1000)
l2 := cachex.NewRedisHandler(&cachex.RedisConfig{
    Addrs: []string{"localhost:6379"},
})

// Create a two-level cache
cache := cachex.NewTwoLevelHandler(l1, l2, &cachex.TwoLevelConfig{
    WriteStrategy: cachex.WriteThrough,
})
defer cache.Close()

// Use normally - it will automatically handle the two levels
val, err := cache.Get([]byte("key"))
```

## Available Cache Implementations

### LRUHandler
- Simple in-memory LRU cache
- Supports TTL
- Thread-safe
- Good for local caching or testing

### ExpiringHandler
- Map-based in-memory cache
- Automatic expired key cleanup
- Thread-safe
- Suitable for temporary data caching

### RistrettoHandler
- High-performance cache based on dgraph-io/ristretto
- Automatic item eviction
- Memory-bounded
- Good for production use with high concurrency

### RedisHandler
- Distributed cache using Redis
- Supports Redis clusters
- Persistent storage
- Good for distributed systems

### CtxCache
- Context-aware cache wrapper
- Deduplicates concurrent loads
- Supports cancellation
- Good for expensive computations

### ShardedHandler
- Sharded cache for better concurrency
- Uses multiple underlying caches
- Reduces lock contention
- Good for high-throughput scenarios

### TwoLevelHandler
- Hierarchical caching (e.g., memory + Redis)
- Different write strategies
- Automatic L1/L2 synchronization
- Good for optimizing access patterns

## Error Handling

The library uses standard error types for common scenarios:

- `ErrNotFound`: Key not found in cache
- `ErrInvalidKey`: Invalid or nil key
- `ErrInvalidValue`: Invalid or nil value
- `ErrInvalidTTL`: Invalid TTL value
- `ErrClosed`: Cache instance is closed
- `ErrCapacityExceeded`: Cache capacity limit reached

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
