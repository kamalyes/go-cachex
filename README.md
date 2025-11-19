# Go-Cachex

> Go-Cachex 是一个全面的缓存库，提供多种缓存实现和适配器，支持 TTL、LRU 驱逐、并发安全、分布式锁、队列、发布订阅和上下文感知等特性。

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

## 📚 目录

- [架构设计](#架构设计)
- [功能特性](#功能特性)
- [快速开始](#快速开始)
- [核心组件](#核心组件)
- [文档索引](#文档索引)
- [示例代码](#示例代码)
- [性能报告](#性能报告)
- [贡献指南](#贡献指南)

## 🏗️ 架构设计

Go-Cachex 采用模块化分层架构设计，提供灵活且强大的缓存解决方案：

```
┌─────────────────────────────────────────────────────────────┐
│                        用户代码                              │
└─────────────────────────┬───────────────────────────────────┘
                         │
┌─────────────────────────▼───────────────────────────────────┐
│              Client (统一入口 + 配置管理)                    │
└─────────────────────────┬───────────────────────────────────┘
                         │
┌─────────────────────────▼───────────────────────────────────┐
│        CtxCache (context 支持 + singleflight 去重)          │
└─────────────────────────┬───────────────────────────────────┘
                         │
┌─────────────────────────▼───────────────────────────────────┐
│  Handler (缓存实现：LRU/Redis/Ristretto/Expiring/等)        │
└─────────────────────┬───┬───┬───┬─────────────────────────────┘
                     │   │   │   │
              ┌──────▼─┐ ┌▼─┐ ┌▼─┐ ┌▼──────────────┐
              │ Memory │ │队列│ │锁│ │ 发布订阅      │
              └────────┘ └──┘ └──┘ └───────────────┘
```

### 🎯 架构层次

- **Client 层**：统一的用户接口，提供配置管理和便利函数
- **CtxCache 层**：为底层 Handler 添加 context 支持和并发去重功能  
- **Handler 层**：具体的缓存实现，支持多种存储后端
- **扩展层**：队列、锁、发布订阅等高级功能

## ✨ 功能特性

### 🚀 **核心缓存功能**

#### 多种缓存后端
- **LRU Cache**: 内存 LRU 缓存，支持容量限制和 TTL
- **LRU Optimized**: 超高性能分片架构LRU (500%+性能提升)
- **Ristretto Cache**: 基于频率的并发缓存，最佳缓存命中率
- **Redis Cache**: 分布式缓存后端，支持故障转移
- **TwoLevel Cache**: 智能分层缓存，L1快速缓存 + L2存储缓存
- **Sharded Cache**: 分布式负载，减少锁竞争
- **Expiring Cache**: 简单的 TTL 缓存，后台自动清理

#### 统一Handler接口
```go
type Handler interface {
    Set(key, value []byte) error
    SetWithTTL(key, value []byte, ttl time.Duration) error
    Get(key []byte) ([]byte, error)
    GetTTL(key []byte) ([]byte, time.Duration, error)
    Del(key []byte) error
    BatchGet(keys [][]byte) ([][]byte, []error)
    Stats() Stats
    Close() error
}
```

### 🔥 **高级特性**

#### 队列系统 
- **多种队列类型**: FIFO、LIFO、优先级、延迟队列
- **批量操作**: 支持批量入队/出队提升性能
- **分布式锁**: 可选的队列操作锁定机制
- **重试机制**: 失败任务自动重试
- **统计监控**: 队列状态和性能统计

#### 分布式锁
- **Redis分布式锁**: 基于Redis的高可用分布式锁
- **可选看门狗**: 自动续期防止锁过期
- **锁竞争统计**: 详细的锁使用统计
- **超时控制**: 灵活的锁获取超时配置

#### 发布订阅系统
- **Redis Pub/Sub**: 高性能消息发布订阅
- **模式订阅**: 支持通配符模式订阅
- **请求响应**: 内置请求响应通信模式
- **消息重试**: 可配置的消息处理重试
- **统计监控**: 消息传递统计和监控

#### 热Key缓存
- **SQL数据加载**: 支持从数据库加载热点数据
- **Redis存储**: 使用Redis作为分布式存储
- **自动刷新**: 可配置的定时数据刷新
- **缓存预热**: 启动时预加载热点数据

### ⚡ **性能与监控**

#### Context 支持
- **上下文取消**: 所有操作支持 context，可实现超时控制
- **并发去重**: 内置 singleflight 机制，避免重复计算  
- **GetOrCompute**: 智能加载函数，缓存未命中时自动计算

#### 统计与监控
- **性能指标**: 命中率、操作计数、延迟统计
- **容量信息**: 当前条目、最大容量、内存使用
- **架构细节**: 分片计数、驱逐统计、后端状态
- **健康状态**: 连接状态、错误率、过期计数

#### 高性能优化
- **零拷贝技术**: 减少内存分配和复制
- **分片架构**: 减少锁竞争，提升并发性能
- **批量操作**: 优化网络和CPU使用率
- **内存池**: 对象重用减少GC压力

## 🚀 快速开始

### 环境要求

建议需要 [Go](https://go.dev/) 版本 [1.23](https://go.dev/doc/devel/release#go1.23.0) 或更高版本

### 安装

```sh
go get -u github.com/kamalyes/go-cachex
```

### 快速示例

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
    
    fmt.Printf("Value: %s\n", string(val))
    
    // 使用 GetOrCompute
    result, err := client.GetOrCompute(ctx, []byte("computed"), 
        func(ctx context.Context) ([]byte, error) {
            return []byte("computed value"), nil
        })
    if err != nil {
        panic(err)
    }
    
    fmt.Printf("Computed: %s\n", string(result))
}
```

## 🧩 核心组件

### 缓存客户端
- `NewLRUClient()` - 内存LRU缓存
- `NewLRUOptimizedClient()` - 高性能分片LRU  
- `NewRedisClient()` - Redis分布式缓存
- `NewRistrettoClient()` - 高性能Ristretto缓存
- `NewTwoLevelClient()` - 两级缓存系统

### 队列系统
- `NewQueueHandler()` - 创建队列处理器
- `QueueType` - FIFO/LIFO/Priority/Delayed
- `ProcessWithLock()` - 支持分布式锁的处理

### 分布式锁
- `NewDistributedLock()` - 创建分布式锁
- `TryLock()` / `Lock()` - 获取锁
- `Unlock()` - 释放锁

### 发布订阅
- `NewPubSub()` - 创建发布订阅客户端  
- `Subscribe()` - 订阅消息
- `Publish()` - 发布消息

### 热Key缓存
- `NewHotKeyCache()` - 创建热key缓存
- `DataLoader` - 数据加载器接口
- `Refresh()` - 手动刷新数据

## 📖 文档索引

### 📋 **基础文档**
- [详细使用指南](./USAGE.md) - 完整的API使用说明
- [Redis配置指南](./REDIS_CONFIG.md) - Redis相关配置说明

### 🔧 **组件文档**  
- [队列系统高级指南](./docs/QUEUE_ADVANCED.md) - 队列的详细配置和使用
- [发布订阅高级指南](./docs/PUBSUB_ADVANCED.md) - PubSub的高级特性
- [热Key缓存指南](./docs/HOTKEY_ADVANCED.md) - 热点数据缓存最佳实践

### 📊 **性能与架构**
- [性能测试报告](./docs/PERFORMANCE-REPORT.md) - 详细性能基准测试
- [LRU优化报告](./docs/LRU-OPTIMIZATION-REPORT.md) - LRU优化技术细节
- [接口统一总结](./docs/INTERFACE-UNIFICATION-SUMMARY.md) - 架构设计文档

### 💻 **开发指南**
- [API 文档](https://pkg.go.dev/github.com/kamalyes/go-cachex) - 在线API文档
- [示例代码](examples/) - 各种使用场景的示例
- [测试状态报告](./TEST-STATUS-REPORT.md) - 测试覆盖率和已知问题
- [贡献指南](.github/CODE_OF_CONDUCT.md) - 贡献代码的规范

## 🏃‍♂️ 示例代码

各组件的详细示例请查看 [examples/](examples/) 目录：

- [`examples/lru/`](examples/lru/) - LRU缓存示例
- [`examples/lru_optimized/`](examples/lru_optimized/) - 优化LRU示例  
- [`examples/ristretto/`](examples/ristretto/) - Ristretto缓存示例
- [`examples/twolevel/`](examples/twolevel/) - 两级缓存示例
- [`examples/ctxcache/`](examples/ctxcache/) - Context缓存示例
- [`examples/expiring/`](examples/expiring/) - 过期缓存示例

## 📈 性能报告

### 基准测试结果

| 组件 | 操作/秒 | 延迟 | 内存使用 | 提升 |
|------|---------|------|----------|------|
| LRU Optimized | 2.8M ops/s | 0.35μs | 354 bytes/entry | +500% |
| Standard LRU | 500K ops/s | 2.0μs | 18TB bytes/entry | baseline |
| Ristretto | 1.2M ops/s | 0.83μs | Variable | +140% |
| Redis Cache | 100K ops/s | 10μs | Network | N/A |

详细性能分析请查看 [PERFORMANCE-REPORT.md](./docs/PERFORMANCE-REPORT.md)

### 优化特性

- **零拷贝技术**: 减少70%的内存分配
- **分片架构**: 16分片设计，减少99%的锁竞争  
- **批量操作**: 网络操作性能提升10倍
- **对象池**: GC压力减少80%

## 🤝 贡献指南

我们欢迎各种形式的贡献！

### 如何贡献
1. Fork 项目
2. 创建特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交更改 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 打开 Pull Request

### 开发要求
- Go 1.23+
- 单元测试覆盖率 > 90%
- 遵循 Go 代码规范
- 添加适当的文档和示例

### 问题报告
如果发现 bug 或有功能建议，请通过 [Issues](https://github.com/kamalyes/go-cachex/issues) 提交。

## 📄 许可证

本项目采用 MIT 许可证 - 查看 [LICENSE](LICENSE) 文件了解详情。

## 🙏 致谢

感谢所有为本项目做出贡献的开发者！

---

如果觉得这个项目对您有帮助，请给我们一个 ⭐ Star！
