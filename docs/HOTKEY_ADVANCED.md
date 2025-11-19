# 热键缓存(HotKey)组件高级使用指南

## 🏗️ 架构设计

### 核心组件架构
```
                    HotKeyCache Manager
                           ↓
        ┌─────────────┬─────────────┬─────────────┐
        │ Local Cache │ Data Loader │ Auto Refresh│
        │   (Memory)  │  (SQL/API)  │ (Background)│
        └─────────────┴─────────────┴─────────────┘
                ↓           ↓           ↓
    ┌───────────────────────────────────────────────┐
    │               Redis Storage                    │
    │    Hash/String    TTL Control    Namespace    │
    └───────────────────────────────────────────────┘
                           ↓
        ┌─────────────────────────────────────────┐
        │          数据同步策略                     │
        │   写入更新 → 定时刷新 → 失效重建          │
        └─────────────────────────────────────────┘
```

### 数据流转架构
```
DataSource              HotKeyCache              Application
    │                       │                       │
    │──── 批量加载 ──────────→│                       │
    │                       │───── 快速查询 ────────→│
    │                       │                       │
    │                       │←───── 缓存更新 ────────│
    │←───── 刷新触发 ─────────│                       │
```

### 特性对比

| 功能特性 | 支持度 | 性能 | 内存效率 | 适用场景 |
|----------|--------|------|----------|----------|
| 字典缓存 | ✅ | 极高 | 高 | 配置数据 |
| 自动刷新 | ✅ | 高 | 中 | 动态数据 |
| 命名空间 | ✅ | 高 | 高 | 多租户 |
| 泛型支持 | ✅ | 高 | 高 | 类型安全 |
| TTL控制 | ✅ | 高 | 中 | 时效数据 |

## ✅ 推荐使用模式

### 1. 基础配置 - 推荐写法

```go
// ✅ 推荐：数据字典缓存配置
config := HotKeyConfig{
    DefaultTTL:        time.Hour * 24,    // 24小时TTL
    RefreshInterval:   time.Hour,         // 1小时刷新
    EnableAutoRefresh: true,              // 自动刷新
    Namespace:        "dict",             // 命名空间
}

// ✅ 推荐：SQL数据加载器
sqlLoader := &SQLDataLoader[string, UserInfo]{
    QueryFunc: func(ctx context.Context) (map[string]UserInfo, error) {
        users := make(map[string]UserInfo)
        
        rows, err := db.QueryContext(ctx, `
            SELECT id, name, email, department 
            FROM users 
            WHERE status = 'active'
        `)
        if err != nil {
            return nil, err
        }
        defer rows.Close()
        
        for rows.Next() {
            var user UserInfo
            if err := rows.Scan(&user.ID, &user.Name, &user.Email, &user.Department); err != nil {
                continue
            }
            users[user.ID] = user
        }
        
        return users, nil
    },
}

cache := NewHotKeyCache(client, "user_dict", sqlLoader, config)
defer cache.Close() // ✅ 确保资源清理
```

### 2. 配置字典缓存 - 推荐模式

```go
// ✅ 推荐：应用配置管理
type ConfigManager struct {
    configCache  *HotKeyCache[string, ConfigItem]
    featureCache *HotKeyCache[string, FeatureFlag]
}

type ConfigItem struct {
    Key         string      `json:"key"`
    Value       interface{} `json:"value"`
    Type        string      `json:"type"`        // string, int, bool, json
    Environment string      `json:"environment"` // dev, prod, test
    UpdatedAt   time.Time   `json:"updated_at"`
}

type FeatureFlag struct {
    Name        string    `json:"name"`
    Enabled     bool      `json:"enabled"`
    Percentage  float64   `json:"percentage"`  // 0-100
    Environment string    `json:"environment"`
    UpdatedAt   time.Time `json:"updated_at"`
}

// ✅ 推荐：配置项加载器
func NewConfigManager(client *redis.Client, db *sql.DB) *ConfigManager {
    configLoader := &SQLDataLoader[string, ConfigItem]{
        QueryFunc: func(ctx context.Context) (map[string]ConfigItem, error) {
            configs := make(map[string]ConfigItem)
            
            query := `
                SELECT key_name, value, value_type, environment, updated_at 
                FROM app_configs 
                WHERE deleted_at IS NULL
            `
            
            rows, err := db.QueryContext(ctx, query)
            if err != nil {
                return nil, fmt.Errorf("failed to load configs: %w", err)
            }
            defer rows.Close()
            
            for rows.Next() {
                var config ConfigItem
                if err := rows.Scan(&config.Key, &config.Value, &config.Type, 
                                 &config.Environment, &config.UpdatedAt); err != nil {
                    log.Printf("Failed to scan config: %v", err)
                    continue
                }
                configs[config.Key] = config
            }
            
            return configs, nil
        },
    }
    
    featureLoader := &SQLDataLoader[string, FeatureFlag]{
        QueryFunc: func(ctx context.Context) (map[string]FeatureFlag, error) {
            features := make(map[string]FeatureFlag)
            
            query := `
                SELECT name, enabled, percentage, environment, updated_at 
                FROM feature_flags 
                WHERE deleted_at IS NULL
            `
            
            rows, err := db.QueryContext(ctx, query)
            if err != nil {
                return nil, fmt.Errorf("failed to load features: %w", err)
            }
            defer rows.Close()
            
            for rows.Next() {
                var feature FeatureFlag
                if err := rows.Scan(&feature.Name, &feature.Enabled, &feature.Percentage,
                                 &feature.Environment, &feature.UpdatedAt); err != nil {
                    log.Printf("Failed to scan feature: %v", err)
                    continue
                }
                features[feature.Name] = feature
            }
            
            return features, nil
        },
    }
    
    configCache := NewHotKeyCache(client, "app_configs", configLoader, HotKeyConfig{
        DefaultTTL:        time.Hour * 12,
        RefreshInterval:   time.Minute * 30,
        EnableAutoRefresh: true,
        Namespace:        "config",
    })
    
    featureCache := NewHotKeyCache(client, "feature_flags", featureLoader, HotKeyConfig{
        DefaultTTL:        time.Hour * 6,
        RefreshInterval:   time.Minute * 15,
        EnableAutoRefresh: true,
        Namespace:        "feature",
    })
    
    return &ConfigManager{
        configCache:  configCache,
        featureCache: featureCache,
    }
}

// ✅ 推荐：类型安全的配置获取
func (cm *ConfigManager) GetString(key string, defaultValue string) string {
    config, exists := cm.configCache.Get(key)
    if !exists || config.Type != "string" {
        return defaultValue
    }
    
    if str, ok := config.Value.(string); ok {
        return str
    }
    return defaultValue
}

func (cm *ConfigManager) GetInt(key string, defaultValue int) int {
    config, exists := cm.configCache.Get(key)
    if !exists || config.Type != "int" {
        return defaultValue
    }
    
    switch v := config.Value.(type) {
    case int:
        return v
    case float64:
        return int(v)
    case string:
        if val, err := strconv.Atoi(v); err == nil {
            return val
        }
    }
    return defaultValue
}

func (cm *ConfigManager) IsFeatureEnabled(featureName string) bool {
    feature, exists := cm.featureCache.Get(featureName)
    if !exists {
        return false
    }
    
    if !feature.Enabled {
        return false
    }
    
    // ✅ 推荐：按百分比进行灰度发布
    if feature.Percentage < 100 {
        hash := calculateHash(featureName + getCurrentUserID())
        return (hash % 100) < int(feature.Percentage)
    }
    
    return true
}
```

### 3. 用户权限缓存 - 推荐模式

```go
// ✅ 推荐：用户权限管理
type PermissionManager struct {
    userCache       *HotKeyCache[string, UserPermissions]
    roleCache       *HotKeyCache[string, RoleDefinition]
    permissionCache *HotKeyCache[string, PermissionRule]
}

type UserPermissions struct {
    UserID      string              `json:"user_id"`
    Roles       []string            `json:"roles"`
    Permissions map[string][]string `json:"permissions"` // resource -> actions
    ExpiresAt   time.Time           `json:"expires_at"`
}

type RoleDefinition struct {
    RoleName    string              `json:"role_name"`
    Permissions map[string][]string `json:"permissions"`
    IsSystem    bool                `json:"is_system"`
    CreatedAt   time.Time           `json:"created_at"`
}

// ✅ 推荐：权限检查方法
func (pm *PermissionManager) HasPermission(userID, resource, action string) bool {
    userPerms, exists := pm.userCache.Get(userID)
    if !exists {
        // ✅ 推荐：缓存未命中时的降级策略
        return pm.checkPermissionFromDB(userID, resource, action)
    }
    
    // 检查过期时间
    if !userPerms.ExpiresAt.IsZero() && time.Now().After(userPerms.ExpiresAt) {
        // ✅ 推荐：权限过期时主动刷新
        go pm.refreshUserPermissions(userID)
        return false
    }
    
    // 直接权限检查
    if actions, hasResource := userPerms.Permissions[resource]; hasResource {
        for _, a := range actions {
            if a == action || a == "*" {
                return true
            }
        }
    }
    
    // 角色权限检查
    for _, roleName := range userPerms.Roles {
        if role, roleExists := pm.roleCache.Get(roleName); roleExists {
            if actions, hasResource := role.Permissions[resource]; hasResource {
                for _, a := range actions {
                    if a == action || a == "*" {
                        return true
                    }
                }
            }
        }
    }
    
    return false
}

// ✅ 推荐：权限批量检查
func (pm *PermissionManager) BatchCheckPermissions(userID string, checks []PermissionCheck) map[string]bool {
    results := make(map[string]bool, len(checks))
    
    userPerms, exists := pm.userCache.Get(userID)
    if !exists {
        // 所有检查都返回false
        for _, check := range checks {
            results[check.String()] = false
        }
        return results
    }
    
    for _, check := range checks {
        results[check.String()] = pm.checkSinglePermission(userPerms, check.Resource, check.Action)
    }
    
    return results
}
```

### 4. 地理位置数据缓存 - 推荐模式

```go
// ✅ 推荐：地理数据缓存
type GeoDataManager struct {
    countryCache *HotKeyCache[string, CountryInfo]
    cityCache    *HotKeyCache[string, CityInfo]
    regionCache  *HotKeyCache[string, RegionInfo]
}

type CountryInfo struct {
    Code        string  `json:"code"`        // CN, US, JP
    Name        string  `json:"name"`        // China, United States
    Continent   string  `json:"continent"`   // Asia, North America
    Currency    string  `json:"currency"`    // CNY, USD
    Timezone    string  `json:"timezone"`    // UTC+8, UTC-5
    Latitude    float64 `json:"latitude"`
    Longitude   float64 `json:"longitude"`
}

type CityInfo struct {
    ID          int     `json:"id"`
    Name        string  `json:"name"`
    CountryCode string  `json:"country_code"`
    StateCode   string  `json:"state_code"`
    Population  int64   `json:"population"`
    Latitude    float64 `json:"latitude"`
    Longitude   float64 `json:"longitude"`
    Timezone    string  `json:"timezone"`
}

// ✅ 推荐：API数据加载器
type APIDataLoader[K comparable, V any] struct {
    EndpointURL string
    Headers     map[string]string
    Transform   func([]byte) (map[K]V, error)
}

func (a *APIDataLoader[K, V]) Load(ctx context.Context) (map[K]V, error) {
    req, err := http.NewRequestWithContext(ctx, "GET", a.EndpointURL, nil)
    if err != nil {
        return nil, err
    }
    
    for key, value := range a.Headers {
        req.Header.Set(key, value)
    }
    
    client := &http.Client{Timeout: time.Second * 30}
    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    data, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, err
    }
    
    return a.Transform(data)
}

// ✅ 推荐：地理数据管理器初始化
func NewGeoDataManager(client *redis.Client) *GeoDataManager {
    countryLoader := &APIDataLoader[string, CountryInfo]{
        EndpointURL: "https://restcountries.com/v3.1/all",
        Headers: map[string]string{
            "Accept": "application/json",
        },
        Transform: func(data []byte) (map[string]CountryInfo, error) {
            var countries []map[string]interface{}
            if err := json.Unmarshal(data, &countries); err != nil {
                return nil, err
            }
            
            result := make(map[string]CountryInfo)
            for _, country := range countries {
                // 解析国家数据
                if cca2, ok := country["cca2"].(string); ok {
                    info := CountryInfo{
                        Code: cca2,
                        // ... 其他字段解析
                    }
                    result[cca2] = info
                }
            }
            
            return result, nil
        },
    }
    
    return &GeoDataManager{
        countryCache: NewHotKeyCache(client, "countries", countryLoader, HotKeyConfig{
            DefaultTTL:        time.Hour * 24 * 7, // 一周更新一次
            RefreshInterval:   time.Hour * 24,      // 每天检查
            EnableAutoRefresh: true,
            Namespace:        "geo",
        }),
    }
}
```

### 5. 多级缓存策略 - 推荐架构

```go
// ✅ 推荐：多级缓存管理器
type MultiLevelCacheManager struct {
    l1Cache map[string]interface{} // 内存缓存
    l2Cache *HotKeyCache[string, interface{}] // Redis缓存
    l3Cache DataLoader[string, interface{}] // 数据源
    
    l1TTL   time.Duration
    l1Mutex sync.RWMutex
    
    stats CacheStats
}

type CacheStats struct {
    L1Hits   int64
    L2Hits   int64
    L3Hits   int64
    Misses   int64
    Evictions int64
}

func NewMultiLevelCache(client *redis.Client, loader DataLoader[string, interface{}]) *MultiLevelCacheManager {
    return &MultiLevelCacheManager{
        l1Cache: make(map[string]interface{}),
        l2Cache: NewHotKeyCache(client, "l2_cache", loader, HotKeyConfig{
            DefaultTTL:        time.Hour,
            RefreshInterval:   time.Minute * 30,
            EnableAutoRefresh: true,
            Namespace:        "multi_level",
        }),
        l3Cache: loader,
        l1TTL:   time.Minute * 5,
    }
}

// ✅ 推荐：多级缓存查询
func (mlc *MultiLevelCacheManager) Get(key string) (interface{}, bool) {
    // L1缓存查询
    mlc.l1Mutex.RLock()
    if value, exists := mlc.l1Cache[key]; exists {
        mlc.l1Mutex.RUnlock()
        atomic.AddInt64(&mlc.stats.L1Hits, 1)
        return value, true
    }
    mlc.l1Mutex.RUnlock()
    
    // L2缓存查询
    if value, exists := mlc.l2Cache.Get(key); exists {
        atomic.AddInt64(&mlc.stats.L2Hits, 1)
        
        // 回填L1缓存
        mlc.setL1Cache(key, value)
        return value, true
    }
    
    // L3数据源查询（通过L2缓存的自动加载）
    // 这里可以实现直接从数据源加载的逻辑
    atomic.AddInt64(&mlc.stats.Misses, 1)
    return nil, false
}

func (mlc *MultiLevelCacheManager) setL1Cache(key string, value interface{}) {
    mlc.l1Mutex.Lock()
    defer mlc.l1Mutex.Unlock()
    
    // 简单的LRU淘汰策略
    if len(mlc.l1Cache) > 1000 {
        // 删除一部分旧数据
        count := 0
        for k := range mlc.l1Cache {
            delete(mlc.l1Cache, k)
            count++
            if count >= 100 {
                break
            }
        }
        atomic.AddInt64(&mlc.stats.Evictions, int64(count))
    }
    
    mlc.l1Cache[key] = value
}
```

## ❌ 不推荐使用模式

### 1. 内存泄露反模式

```go
// ❌ 不推荐：无限制的缓存增长
type BadCache struct {
    data map[string]interface{} // 永远不清理
}

func (bc *BadCache) Set(key string, value interface{}) {
    bc.data[key] = value // 无大小限制
}

// ❌ 不推荐：忘记关闭自动刷新
func BadCacheUsage() {
    cache := NewHotKeyCache(client, "data", loader, HotKeyConfig{
        EnableAutoRefresh: true,
    })
    // 忘记调用cache.Close()，goroutine泄露
}
```

### 2. 数据一致性反模式

```go
// ❌ 不推荐：缓存与数据库不一致
func UpdateUser(userID string, data UserInfo) error {
    // 先更新缓存
    cache.Set(userID, data)
    
    // 后更新数据库
    err := db.UpdateUser(userID, data)
    if err != nil {
        // ❌ 忘记回滚缓存
        return err
    }
    return nil
}

// ❌ 不推荐：无版本控制的并发更新
func ConcurrentUpdate(key string, updateFunc func(interface{}) interface{}) {
    value, _ := cache.Get(key)
    newValue := updateFunc(value)
    cache.Set(key, newValue) // 可能覆盖其他线程的更新
}
```

### 3. 性能反模式

```go
// ❌ 不推荐：同步数据加载阻塞业务
func SyncLoadData() map[string]interface{} {
    // ❌ 在业务线程中同步加载大量数据
    data := make(map[string]interface{})
    
    rows, _ := db.Query("SELECT * FROM large_table") // 可能很慢
    defer rows.Close()
    
    for rows.Next() {
        // 处理大量数据
    }
    
    return data
}

// ❌ 不推荐：频繁的完整刷新
func BadRefreshPattern() {
    ticker := time.NewTicker(time.Second) // 过于频繁
    defer ticker.Stop()
    
    for range ticker.C {
        cache.Refresh() // 每次都全量刷新
    }
}
```

## 🛠️ 最佳实践

### 1. 缓存预热策略

```go
// ✅ 推荐：应用启动时预热关键数据
type CacheWarmer struct {
    caches []Warmable
}

type Warmable interface {
    Warmup(ctx context.Context) error
}

func (cw *CacheWarmer) WarmupAll(ctx context.Context) error {
    var wg sync.WaitGroup
    errors := make(chan error, len(cw.caches))
    
    for _, cache := range cw.caches {
        wg.Add(1)
        go func(c Warmable) {
            defer wg.Done()
            if err := c.Warmup(ctx); err != nil {
                errors <- err
            }
        }(cache)
    }
    
    wg.Wait()
    close(errors)
    
    var allErrors []error
    for err := range errors {
        allErrors = append(allErrors, err)
    }
    
    if len(allErrors) > 0 {
        return fmt.Errorf("warmup errors: %v", allErrors)
    }
    
    return nil
}
```

### 2. 缓存降级策略

```go
// ✅ 推荐：缓存故障时的降级机制
type FallbackCache[K comparable, V any] struct {
    primary   *HotKeyCache[K, V]
    secondary DataLoader[K, V]
    fallbackStats FallbackStats
}

type FallbackStats struct {
    PrimaryHits   int64
    FallbackHits  int64
    TotalRequests int64
}

func (fc *FallbackCache[K, V]) Get(key K) (V, bool) {
    atomic.AddInt64(&fc.fallbackStats.TotalRequests, 1)
    
    // 尝试主缓存
    if value, exists := fc.primary.Get(key); exists {
        atomic.AddInt64(&fc.fallbackStats.PrimaryHits, 1)
        return value, true
    }
    
    // 降级到直接数据源
    data, err := fc.secondary.Load(context.Background())
    if err != nil {
        var zero V
        return zero, false
    }
    
    if value, exists := data[key]; exists {
        atomic.AddInt64(&fc.fallbackStats.FallbackHits, 1)
        
        // 异步回填缓存
        go func() {
            fc.primary.Set(key, value)
        }()
        
        return value, true
    }
    
    var zero V
    return zero, false
}
```

### 3. 监控和告警

```go
// ✅ 推荐：缓存健康监控
type CacheHealthMonitor struct {
    caches []HealthCheckable
    alerts AlertManager
}

type HealthCheckable interface {
    HealthCheck() HealthStatus
    GetStats() CacheStats
}

type HealthStatus struct {
    IsHealthy     bool
    LastRefresh   time.Time
    ErrorCount    int64
    HitRate       float64
    RefreshErrors []error
}

func (chm *CacheHealthMonitor) StartMonitoring() {
    ticker := time.NewTicker(time.Minute)
    defer ticker.Stop()
    
    for range ticker.C {
        for _, cache := range chm.caches {
            status := cache.HealthCheck()
            
            if !status.IsHealthy {
                chm.alerts.SendAlert(Alert{
                    Level:   "ERROR",
                    Message: fmt.Sprintf("Cache unhealthy: %+v", status),
                    Time:    time.Now(),
                })
            }
            
            if status.HitRate < 0.8 { // 命中率低于80%
                chm.alerts.SendAlert(Alert{
                    Level:   "WARN",
                    Message: fmt.Sprintf("Low cache hit rate: %.2f%%", status.HitRate*100),
                    Time:    time.Now(),
                })
            }
        }
    }
}
```

## 🔧 配置调优建议

### 1. 不同场景的配置

```go
// 静态配置数据（很少变化）
staticConfig := HotKeyConfig{
    DefaultTTL:        time.Hour * 24 * 7, // 7天
    RefreshInterval:   time.Hour * 24,      // 每天检查
    EnableAutoRefresh: true,
    Namespace:        "static",
}

// 用户权限数据（中等变化频率）
permissionConfig := HotKeyConfig{
    DefaultTTL:        time.Hour * 2,       // 2小时
    RefreshInterval:   time.Minute * 30,    // 30分钟检查
    EnableAutoRefresh: true,
    Namespace:        "permissions",
}

// 实时数据（高变化频率）
realtimeConfig := HotKeyConfig{
    DefaultTTL:        time.Minute * 10,    // 10分钟
    RefreshInterval:   time.Minute * 2,     // 2分钟检查
    EnableAutoRefresh: true,
    Namespace:        "realtime",
}
```

### 2. 内存优化策略

```go
// ✅ 推荐：大数据集的分片缓存
type ShardedHotKeyCache[K comparable, V any] struct {
    shards []*HotKeyCache[K, V]
    count  int
}

func NewShardedHotKeyCache[K comparable, V any](client *redis.Client, shardCount int, 
    loaderFunc func(shard int) DataLoader[K, V], config HotKeyConfig) *ShardedHotKeyCache[K, V] {
    
    shards := make([]*HotKeyCache[K, V], shardCount)
    
    for i := 0; i < shardCount; i++ {
        shardKey := fmt.Sprintf("%s_shard_%d", config.Namespace, i)
        shards[i] = NewHotKeyCache(client, shardKey, loaderFunc(i), config)
    }
    
    return &ShardedHotKeyCache[K, V]{
        shards: shards,
        count:  shardCount,
    }
}

func (shc *ShardedHotKeyCache[K, V]) Get(key K) (V, bool) {
    shard := shc.getShard(key)
    return shc.shards[shard].Get(key)
}

func (shc *ShardedHotKeyCache[K, V]) getShard(key K) int {
    hash := calculateHash(fmt.Sprintf("%v", key))
    return int(hash) % shc.count
}
```

## 📊 性能基准

| 数据规模 | 内存使用 | 查询延迟 | 刷新时间 | 适用场景 |
|----------|----------|----------|----------|----------|
| 1K条目 | ~1MB | <0.1ms | <100ms | 配置项 |
| 10K条目 | ~10MB | <0.2ms | <500ms | 用户信息 |
| 100K条目 | ~100MB | <0.5ms | <2s | 产品目录 |
| 1M条目 | ~1GB | <1ms | <10s | 大型字典 |

### 架构扩展建议

1. **分布式缓存**：多Redis实例负载均衡
2. **缓存分层**：热点数据本地缓存 + 完整数据Redis缓存
3. **一致性保证**：使用版本号或时间戳控制数据一致性
4. **容灾备份**：主从复制 + 数据源直接访问降级