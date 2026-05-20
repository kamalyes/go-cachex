# 缓存索引管理器 (Cache Index Manager) 使用指南

> 傻瓜式缓存索引管理 —— 一行代码完成缓存读取+索引注册，一行代码批量删除关联缓存

## 📚 目录

- [核心概念](#核心概念)
- [快速开始](#快速开始)
- [API 详解](#api-详解)
- [业务场景示例](#业务场景示例)
- [最佳实践](#最佳实践)
- [常见问题](#常见问题)

## 核心概念

### 问题背景

在分布式缓存中，经常需要按业务维度批量删除缓存。例如：

- 租户配置变更 → 需要删除该租户下所有相关缓存
- 区域设置更新 → 需要删除该区域的所有缓存键
- 但缓存键是分散的，如何知道哪些键需要删除？

### 解决方案

**缓存索引管理器** 通过 Redis Set 作为索引集合来追踪缓存键：

```
写入缓存时：缓存键 → 注册到索引集合
删除缓存时：索引集合 → 批量删除所有关联缓存键
```

### 设计原则

| 原则 | 说明 |
|------|------|
| 傻瓜式操作 | `CacheWrapperWithIndex` 一行代码同时完成缓存读取+索引注册 |
| 原子删除 | Lua 脚本保证索引集合与缓存键的原子清理 |
| 分组管理 | `NewIndexGroup` 预定义业务分组，一行调用批量删除 |
| 零侵入 | 与现有 `CacheWrapper` 完全兼容，无需修改已有代码 |

## 快速开始

### 1. 创建管理器

```go
mgr := cachex.NewCacheIndexManager(redisClient)
```

### 2. 写缓存 + 自动注册索引（一行搞定）

```go
loader := cachex.CacheWrapperWithIndex(
    mgr,                    // 索引管理器
    "idx:tenant_options",   // 索引集合键
    redisClient,            // Redis客户端
    "cache:tenant:true",    // 缓存键
    loadFunc,               // 数据加载函数
    time.Minute,            // TTL
)

result, err := loader(ctx)  // 缓存读取 + 索引注册，一步完成
```

### 3. 按索引删除缓存（一行搞定）

```go
// 删除 "idx:tenant_options" 索引下所有缓存
mgr.DeleteByIndex(ctx, "idx:tenant_options")
```

## API 详解

### CacheIndexManager

#### NewCacheIndexManager

创建缓存索引管理器。

```go
mgr := cachex.NewCacheIndexManager(redisClient)
```

参数：

- `client` - Redis 客户端，传 `nil` 时所有操作安全跳过

#### Register

将缓存键注册到索引集合。

```go
mgr.Register(ctx, "idx:tenant_options", "cache:tenant:true", "cache:tenant:region")
```

> 通常不需要手动调用，`CacheWrapperWithIndex` 会自动注册。

#### DeleteByIndex

根据索引集合原子删除所有关联缓存键，返回删除数量。

```go
deleted, err := mgr.DeleteByIndex(ctx, "idx:tenant_options")

// 支持同时删除多个索引
deleted, err := mgr.DeleteByIndex(ctx, "idx:tenant", "idx:region", "idx:platform")
```

特性：

- 使用 Lua 脚本保证原子性
- 删除缓存键后自动删除索引集合本身
- 支持一次删除多个索引集合

#### DeleteByKeys

按缓存键直接删除（用于已知确切键名的场景）。

```go
err := mgr.DeleteByKeys(ctx, "cache:key1", "cache:key2")
```

#### GetIndexMembers

查询索引集合中的所有缓存键（用于调试和监控）。

```go
members, err := mgr.GetIndexMembers(ctx, "idx:tenant_options")
// members = ["cache:tenant:true", "cache:tenant:region", ...]
```

### IndexGroup（索引分组）

#### NewIndexGroup

创建索引分组，预定义业务维度。

```go
tenantGroup := mgr.NewIndexGroup(
    "idx:tenant_options",
    "idx:region_options",
    "idx:platform_options",
)
```

#### Delete

删除分组内所有索引集合关联的缓存键。

```go
deleted, err := tenantGroup.Delete(ctx)
```

#### AddIndexKeys

动态追加索引键到分组。

```go
tenantGroup.AddIndexKeys("idx:new_index")
```

#### IndexKeys

查看分组内的索引键列表。

```go
keys := tenantGroup.IndexKeys()
```

### CacheWrapperWithIndex

带自动索引注册的缓存包装器，是推荐的缓存+索引一体化入口。

```go
loader := cachex.CacheWrapperWithIndex[T](
    manager,      // *CacheIndexManager
    idxKey,       // 索引集合键
    client,       // *redis.Client
    key,          // 缓存键
    cacheFunc,    // CacheFunc[T] 数据加载函数
    expiration,   // time.Duration TTL
    opts...,      // 可选 CacheOption（如 WithoutCompression）
)
```

支持所有 `CacheWrapper` 的选项：

```go
loader := cachex.CacheWrapperWithIndex(
    mgr, "idx:users", client, "cache:user:1",
    loadFunc, time.Hour,
    cachex.WithoutCompression(),  // 禁用压缩
)
```

## 业务场景示例

### 场景1：租户配置缓存

```go
// 初始化
mgr := cachex.NewCacheIndexManager(redisClient)

// 定义业务分组
tenantGroup := mgr.NewIndexGroup(
    "idx:tenant_options",
    "idx:tenant_permissions",
    "idx:tenant_features",
)

// ---- 写缓存（每个接口各自调用）----

// 租户选项
tenantLoader := cachex.CacheWrapperWithIndex(
    mgr, "idx:tenant_options", client, "cache:tenant:true",
    func(ctx context.Context) ([]Option, error) {
        return db.GetTenantOptions(ctx, true)
    }, time.Hour,
)

// 租户权限
permLoader := cachex.CacheWrapperWithIndex(
    mgr, "idx:tenant_permissions", client, "cache:tenant_perms:true",
    func(ctx context.Context) ([]Permission, error) {
        return db.GetTenantPermissions(ctx, true)
    }, time.Hour,
)

// 租户特性
featLoader := cachex.CacheWrapperWithIndex(
    mgr, "idx:tenant_features", client, "cache:tenant_features",
    func(ctx context.Context) ([]Feature, error) {
        return db.GetTenantFeatures(ctx)
    }, time.Hour,
)

// ---- 删缓存（数据变更时一行搞定）----

func OnTenantConfigChanged(ctx context.Context) {
    // 一行删除租户下所有关联缓存
    tenantGroup.Delete(ctx)
}
```

### 场景2：多区域多平台缓存

```go
// 按区域+平台组合缓存
regionLoader := cachex.CacheWrapperWithIndex(
    mgr, "idx:region_options", client, "cache:region:cn",
    loadRegionOptions, time.Hour,
)

platformLoader := cachex.CacheWrapperWithIndex(
    mgr, "idx:platform_options", client, "cache:platform:cn:true",
    loadPlatformOptions, time.Hour,
)

// 区域变更时只删区域相关缓存
func OnRegionChanged(ctx context.Context, region string) {
    mgr.DeleteByIndex(ctx, "idx:region_options")
}

// 全量更新时删所有
func OnFullUpdate(ctx context.Context) {
    mgr.DeleteByIndex(ctx, "idx:region_options", "idx:platform_options")
}
```

### 场景3：与现有 CacheWrapper 共存

```go
// 不需要索引的缓存，继续用 CacheWrapper
simpleLoader := cachex.CacheWrapper(client, "cache:simple", loadFunc, time.Hour)

// 需要索引的缓存，用 CacheWrapperWithIndex
indexedLoader := cachex.CacheWrapperWithIndex(
    mgr, "idx:important", client, "cache:important:data",
    loadFunc, time.Hour,
)
```

## 最佳实践

### 1. 索引键命名规范

```go
// 推荐：idx:业务维度:子维度
"idx:tenant_options"
"idx:tenant_permissions"
"idx:region_options"
"idx:platform_options"
```

### 2. 分组预定义

在服务初始化时预定义分组，避免散落在各处：

```go
var (
    tenantGroup  = mgr.NewIndexGroup("idx:tenant_options", "idx:tenant_permissions")
    regionGroup  = mgr.NewIndexGroup("idx:region_options", "idx:region_features")
    allGroup     = mgr.NewIndexGroup("idx:tenant_options", "idx:region_options", "idx:platform_options")
)
```

### 3. 缓存键与索引键的对应关系

```
缓存键: cache:tenant:true          → 索引: idx:tenant_options
缓存键: cache:tenant_perms:true    → 索引: idx:tenant_permissions
缓存键: cache:region:cn            → 索引: idx:region_options
```

一个缓存键可以注册到多个索引集合（多对多关系）。

### 4. 安全性保证

- `nil` 管理器：所有操作安全跳过，不会 panic
- 空参数：`Register`/`DeleteByIndex`/`DeleteByKeys` 传入空参数安全返回
- 原子性：`DeleteByIndex` 使用 Lua 脚本，保证索引和缓存键的原子清理

## 常见问题

### Q: 索引集合会占用多少内存？

A: Redis Set 存储的是缓存键名字符串，每个键通常几十字节。即使有 1000 个缓存键，索引集合也只占约 50KB。

### Q: 缓存过期后索引集合会残留吗？

A: 缓存键自然过期后，索引集合中仍会保留该键名。但 `DeleteByIndex` 在删除时会尝试 DEL 所有成员键，对已过期的键 DEL 操作是无害的。如果需要清理残留索引，可以在业务低峰期调用 `DeleteByIndex`。

### Q: 和手动管理索引相比有什么优势？

A:

- **手动方式**：需要在每个写缓存的地方手动 `SAdd`，每个删缓存的地方手动 `SMEMBERS` + `DEL`，容易遗漏
- **自动方式**：`CacheWrapperWithIndex` 一行代码自动注册，`DeleteByIndex` 一行代码原子删除，不会遗漏

### Q: 可以和 CacheWrapper 的选项一起使用吗？

A: 完全可以。`CacheWrapperWithIndex` 支持所有 `CacheWrapper` 的选项：

```go
loader := cachex.CacheWrapperWithIndex(
    mgr, "idx:data", client, "cache:data",
    loadFunc, time.Hour,
    cachex.WithoutCompression(),  // 禁用压缩
)
```
