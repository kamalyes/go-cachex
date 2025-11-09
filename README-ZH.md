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

## 架构设计

Go-Cachex 采用分层架构设计，提供灵活且强大的缓存解决方案：

```
用户代码
    ↓
Client (统一入口 + 配置管理)
    ↓  
CtxCache (context 支持 + singleflight 去重)
    ↓
Handler (具体缓存实现：LRU/Redis/Ristretto/Expiring)
```

### 架构层次

- **Client 层**：统一的用户接口，提供配置管理和便利函数
- **CtxCache 层**：为底层 Handler 添加 context 支持和并发去重功能
- **Handler 层**：具体的缓存实现，支持多种存储后端

## 功能特性

### 🚀 统一客户端接口
- 简洁一致的 API，支持所有缓存实现
- 便利构造函数：`NewLRUClient`、`NewLRUOptimizedClient`、`NewRedisClient`、`NewRistrettoClient` 等
- 统一的错误处理和参数验证

### 💾 多种缓存后端
- **LRU Cache**: 内存 LRU 缓存，支持容量限制和 TTL
- **LRU Optimized**: 高性能 LRU，具有读写锁分离、对象池和批量操作
- **Expiring Cache**: 基于 map 的内存缓存，自动清理过期键
- **Redis Cache**: 分布式缓存，支持单节点和集群模式
- **Ristretto Cache**: 高性能缓存，基于 dgraph-io/ristretto
- **Sharded Cache**: 分片缓存，提升并发性能
- **Two-Level Cache**: 两级缓存，优化访问模式

### ⚡ Context 支持
- **上下文取消**: 所有操作支持 context 传入，可实现超时控制
- **并发去重**: 内置 singleflight 机制，避免重复计算
- **GetOrCompute**: 智能加载函数，缓存未命中时自动计算并缓存

### 🔒 高级特性
- **线程安全**: 所有实现都是并发安全的
- **TTL 支持**: 灵活的过期时间设置
- **自动清理**: 过期键自动清理，无需手动干预
- **容量管理**: LRU 驱逐策略，智能管理内存使用
- **一致性错误**: 标准化错误类型，便于处理

## 文档链接

- [详细使用指南](./USAGE.md)
- [API 文档](https://pkg.go.dev/github.com/kamalyes/go-cachex)
- [示例代码](examples/)
- [性能测试](docs/benchmarks.md)

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

## 贡献

欢迎贡献！请随时提交 Pull Request。
