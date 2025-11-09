# Go-Cachex
Chinese Documentation - [中文文档](./README-ZH.md)

> Go-Cachex is a comprehensive caching library that provides multiple cache implementations and adapters, supporting TTL, LRU eviction, concurrency safety, and context-aware features.

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

## Architecture Design

Go-Cachex adopts a layered architecture design, providing flexible and powerful caching solutions:

```
User Code
    ↓
Client (Unified Entry Point + Configuration Management)
    ↓  
CtxCache (Context Support + Singleflight Deduplication)
    ↓
Handler (Concrete Cache Implementations: LRU/Redis/Ristretto/Expiring)
```

### Architecture Layers

- **Client Layer**: Unified user interface, providing configuration management and convenience functions
- **CtxCache Layer**: Adds context support and concurrent deduplication to underlying Handlers
- **Handler Layer**: Concrete cache implementations, supporting multiple storage backends

## Features

### 🚀 Unified Client Interface
- Simple and consistent API supporting all cache implementations
- Convenience constructors: `NewLRUClient`, `NewLRUOptimizedClient`, `NewRedisClient`, `NewRistrettoClient`, etc.
- Unified error handling and parameter validation

### 💾 Multiple Cache Backends
- **LRU Cache**: In-memory LRU cache with capacity limits and TTL support
- **LRU Optimized**: High-performance LRU with read-write locks, object pooling, and batch operations
- **Expiring Cache**: Map-based in-memory cache with automatic expired key cleanup
- **Redis Cache**: Distributed cache supporting single node and cluster modes
- **Ristretto Cache**: High-performance cache based on dgraph-io/ristretto
- **Sharded Cache**: Sharded cache for improved concurrent performance
- **Two-Level Cache**: Two-tier cache for optimized access patterns

### ⚡ Context Support
- **Context Cancellation**: All operations support context input for timeout control
- **Concurrent Deduplication**: Built-in singleflight mechanism to avoid duplicate computations
- **GetOrCompute**: Smart loading function that automatically computes and caches on cache misses

### 🔒 Advanced Features
- **Thread Safety**: All implementations are concurrency-safe
- **TTL Support**: Flexible expiration time settings
- **Automatic Cleanup**: Expired keys are automatically cleaned up without manual intervention
- **Capacity Management**: LRU eviction policy for intelligent memory usage management
- **Consistent Errors**: Standardized error types for easy handling

## Documentation Links

- [Detailed Usage Guide](./docs/usage.md)
- [API Documentation](https://pkg.go.dev/github.com/kamalyes/go-cachex)
- [Example Code](examples/)
- [Performance Benchmarks](docs/benchmarks.md)

## Getting Started

### Requirements

Requires [Go](https://go.dev/) version [1.23](https://go.dev/doc/devel/release#go1.23.0) or higher

### Installation

Using [Go's module support](https://go.dev/wiki/Modules#how-to-use-modules), `go [build|run|test]` will automatically fetch the necessary dependencies when you add the import in your code:

```go
import "github.com/kamalyes/go-cachex"
```

Alternatively, use `go get`:

```sh
go get -u github.com/kamalyes/go-cachex
```

## Contributing

Contributions are welcome! Feel free to submit a Pull Request.
