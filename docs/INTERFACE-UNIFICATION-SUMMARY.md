# Go-Cachex Handler接口统一化更新总结

## 🎯 重大更新内容

### 1. 双API设计 - Handler接口标准化 🆕

所有缓存实现现在都支持**双API设计**，提供简化版和完整版两种调用方式：

```go
type Handler interface {
    // ========== 简化版方法（不带context） ==========
    Set(key, value []byte) error
    SetWithTTL(key, value []byte, ttl time.Duration) error
    Get(key []byte) ([]byte, error)
    GetTTL(key []byte) (time.Duration, error)
    Del(key []byte) error
    BatchGet(keys [][]byte) ([][]byte, []error)
    GetOrCompute(key []byte, ttl time.Duration, loader func() ([]byte, error)) ([]byte, error)
    
    // ========== 完整版方法（带context） ==========
    SetWithCtx(ctx context.Context, key, value []byte) error
    SetWithTTLAndCtx(ctx context.Context, key, value []byte, ttl time.Duration) error
    GetWithCtx(ctx context.Context, key []byte) ([]byte, error)
    GetTTLWithCtx(ctx context.Context, key []byte) (time.Duration, error)
    DelWithCtx(ctx context.Context, key []byte) error
    BatchGetWithCtx(ctx context.Context, keys [][]byte) ([][]byte, []error)
    GetOrComputeWithCtx(ctx context.Context, key []byte, ttl time.Duration, loader func(context.Context) ([]byte, error)) ([]byte, error)
    
    // ========== 通用方法 ==========
    Stats() map[string]interface{}
    Close() error
}
```

### 2. 设计理念

- **简化版方法**：适合简单场景，快速调用，内部自动使用`context.Background()`
- **WithCtx方法**：适合需要超时控制、取消操作、链路追踪的场景
- **向后兼容**：简化版方法内部委托给WithCtx方法，保持代码统一
- **灵活选择**：开发者可根据场景自由选择使用哪种API

## 🔧 实现覆盖

所有Handler实现都已完成双API改造：

| Handler类型 | 简化API | WithCtx API | BatchGet | Stats | 特色功能 |
|------------|---------|-------------|----------|-------|----------|
| LRU | ✅ | ✅ | ✅ | ✅ | 过期项统计、容量监控 |
| LRU Optimized | ✅ | ✅ | ✅ | ✅ | 16分片统计、命中率、性能指标 |
| Ristretto | ✅ | ✅ | ✅ | ✅ | 完整Ristretto指标、成本统计 |
| TwoLevel | ✅ | ✅ | ✅ | ✅ | L1/L2分层统计、智能提升监控 |
| Sharded | ✅ | ✅ | ✅ | ✅ | 每分片详细统计、负载均衡 |
| Expiring | ✅ | ✅ | ✅ | ✅ | 过期项监控、后台清理状态 |
| Redis | ✅ | ✅ | ✅ | ✅ | Redis服务器信息、连接状态 |

## 📊 使用示例

### 简化版API - 快速简单

```go
// 基本操作
cache := cachex.NewLRUHandler(1000)
defer cache.Close()

cache.Set([]byte("key"), []byte("value"))
val, _ := cache.Get([]byte("key"))
cache.Del([]byte("key"))

// 批量操作
keys := [][]byte{[]byte("key1"), []byte("key2"), []byte("key3")}
results, errors := cache.BatchGet(keys)

for i, key := range keys {
    if errors[i] == nil {
        fmt.Printf("%s: %s\n", string(key), string(results[i]))
    } else {
        fmt.Printf("%s: %v\n", string(key), errors[i])
    }
}
```

### WithCtx API - 超时控制

```go
// 带超时的操作
ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
defer cancel()

cache.SetWithCtx(ctx, []byte("key"), []byte("value"))
val, err := cache.GetWithCtx(ctx, []byte("key"))
if err == context.DeadlineExceeded {
    // 处理超时
}

// 带取消的批量操作
results, errors := cache.BatchGetWithCtx(ctx, keys)
```

### 统计监控

```go
// 获取详细统计
stats := cache.Stats()

// 通用信息
fmt.Printf("条目数: %v\n", stats["entries"])
fmt.Printf("缓存类型: %v\n", stats["cache_type"])

// LRU Optimized 专用
if shardCount, exists := stats["shard_count"]; exists {
    fmt.Printf("分片数: %v, 命中率: %.2f%%\n", 
        shardCount, stats["hit_rate"].(float64)*100)
}
```

## 🚀 性能优势

1. **批量操作优化**:
   - 减少锁开销
   - 网络往返次数减少（Redis）
   - 并行分片处理（Sharded/LRU Optimized）

2. **统计信息零开销**:
   - 原子计数器（LRU Optimized）
   - 内置指标收集（Ristretto）
   - 实时状态监控

3. **统一API体验**:
   - 所有Handler可互换使用
   - 一致的错误处理
   - 标准化的监控接口

## 📚 文档更新

- ✅ README.md - 添加批量操作和统计功能说明
- ✅ README-ZH.md - 中文版本同步更新
- ✅ USAGE.md - 详细使用示例和最佳实践
- ✅ 性能报告更新 - 反映LRU Optimized的极致性能
- ✅ 优化报告更新 - 分片架构技术细节

## 🎯 最佳实践

1. **优先使用批量操作**:

   ```go
   // ❌ 避免
   for _, key := range keys {
       val, _ := handler.Get(key)
   }
   
   // ✅ 推荐
   results, _ := handler.BatchGet(keys)
   ```

2. **监控缓存健康**:

   ```go
   stats := handler.Stats()
   hitRate := stats["hit_rate"].(float64)
   if hitRate < 0.5 {
       log.Warning("缓存命中率过低")
   }
   ```

3. **选择最优实现**:
   - **超高性能**: LRU Optimized (23M+ ops/s)
   - **分布式**: Redis
   - **读密集**: Ristretto
   - **通用场景**: LRU Classic

## ✅ 向后兼容

- 所有现有代码无需修改
- 新功能为可选使用
- 原有接口方法保持不变
- 性能无负面影响

这次更新让go-cachex成为了真正的企业级缓存解决方案！🏆
