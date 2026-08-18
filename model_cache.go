/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-08-18 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-08-18 00:00:00
 * @FilePath: \go-cachex\model_cache.go
 * @Description: ModelCache —— 基于 gorm 反射的泛型 model 整实例缓存
 *
 * ============================================================================
 * 设计目标
 * ============================================================================
 * 与 ModelKVCache（字段级 KV，如 id→name）互补，本组件缓存完整 model 实例：
 *   - 支持复合业务键（如 tenant_id + platform_id → 整条配置记录）
 *   - 底层复用 KVCache 三层兜底（本地 → Redis Hash → BatchLoader 按 DB 精准回源）
 *   - 写后失效广播（Set/Delete 自动通知其他节点删本地缓存）
 *   - 全量 loader 缺省（配置类小表按需回源即可，不参与 autoRefresh / Warmup）
 *
 * 存储结构：
 *   Redis Hash：Key = {namespace}:{tableName}:model，Field = 复合 key（separator 拼接），
 *   Value = 整个 model 的 JSON 序列化
 *
 * ============================================================================
 * 使用方式
 * ============================================================================
 * 1. 注册（bootstrap 阶段，可复用 ModelKVBase 共享 namespace/ttl）：
 *
 *	cachex.NewModelCache[models.PaymentReceiveConfigModel](channelBase, gwglobal.DB).
 *	    KeyFields("TenantId", "PlatformId").   // 缺省自动取 gorm 主键
 *	    Register()
 *
 * 2. 读取（三层兜底，miss 时 BatchLoader 按 key 精准回源 DB）：
 *
 *	m, found, err := cachex.MustGetModelCache[models.PaymentReceiveConfigModel]().
 *	    Get(ctx, tenantID, platformID)
 *
 * 3. 写维护（通常由宿主服务的 CachedRepository 在 Create/Update/Delete 后自动调用）：
 *
 *	_ = cachex.MustGetModelCache[models.PaymentReceiveConfigModel]().Set(ctx, m)
 *	_ = cachex.MustGetModelCache[models.PaymentReceiveConfigModel]().Delete(ctx, m)
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/kamalyes/go-toolbox/pkg/mathx"
	"gorm.io/gorm"
)

// ============================================================
// Builder
// ============================================================

// ModelCacheBuilder 泛型 model 整实例缓存 builder（链式配置后 Register）
type ModelCacheBuilder[M any] struct {
	base      *ModelKVBase
	db        *gorm.DB
	keyFields []string
	separator string
	extraOpts []KVOption
}

// NewModelCache 创建指定 model 类型的 builder，继承 base 的 opts
//
//	cachex.NewModelCache[models.PaymentReceiveConfigModel](channelBase, gwglobal.DB)
func NewModelCache[M any](base *ModelKVBase, db *gorm.DB) *ModelCacheBuilder[M] {
	return &ModelCacheBuilder[M]{base: base, db: db, separator: ":"}
}

// KeyFields 设置缓存键字段（model Go 字段名，顺序即复合 key 的拼接顺序）
// 可选：未设置时自动取 gorm schema 的 PrioritizedPrimaryField（仅单主键可用，复合主键必须显式指定）
func (b *ModelCacheBuilder[M]) KeyFields(names ...string) *ModelCacheBuilder[M] {
	b.keyFields = names
	return b
}

// Separator 设置复合 key 拼接分隔符（默认 ":"，要求键字段值中不含该分隔符，如 uuid/数字）
func (b *ModelCacheBuilder[M]) Separator(sep string) *ModelCacheBuilder[M] {
	b.separator = sep
	return b
}

// ExtraOptions 追加 base 之外的额外 KVOption（覆盖或补充）
func (b *ModelCacheBuilder[M]) ExtraOptions(opts ...KVOption) *ModelCacheBuilder[M] {
	b.extraOpts = append(b.extraOpts, opts...)
	return b
}

// Register 注册到全局表，返回 ModelCache 实例
func (b *ModelCacheBuilder[M]) Register() *ModelCache[M] {
	return registerModelCache[M](b)
}

// ============================================================
// ModelCache
// ============================================================

// modelKeyField 预编译的键字段（unsafe 偏移提取 + db 列名，供回源查询构建 WHERE）
type modelKeyField[M any] struct {
	dbName    string
	extractor fieldExtractor[M]
}

// ModelCache 泛型 model 整实例缓存
//
// 底层持有一个 KVCache[string, M]，复用其本地分片缓存、Redis Hash、
// PubSub 失效广播与 BatchLoader 按需回源能力
type ModelCache[M any] struct {
	kv        *KVCache[string, M]
	db        *gorm.DB
	tableName string
	keyFields []modelKeyField[M]
	separator string
}

// 全局注册表（按 reflect.Type 索引，业务侧 MustGetModelCache[M]() 无需传 name）
var (
	modelCacheRegistry   = map[reflect.Type]any{}
	modelCacheRegistryMu sync.RWMutex
)

// registerModelCache 内部注册实现
func registerModelCache[M any](b *ModelCacheBuilder[M]) *ModelCache[M] {
	var m M
	modelType := reflect.TypeOf(m)
	if modelType.Kind() != reflect.Struct {
		panic(fmt.Sprintf("cachex: RegisterModelCache requires M to be a struct, got %s", modelType.Kind()))
	}

	// gorm schema 解析表名 + 列名
	s, err := parseSchema[M](b.db)
	if err != nil {
		panic(fmt.Sprintf("cachex: RegisterModelCache[%s] schema parse failed: %v", modelType.String(), err))
	}

	// 解析键字段：显式 KeyFields 优先；未设置则从 gorm schema 的 PrioritizedPrimaryField 自动识别
	keyNames := b.keyFields
	if len(keyNames) == 0 {
		if pf := s.PrioritizedPrimaryField; pf != nil {
			keyNames = []string{pf.Name}
		} else {
			panic(fmt.Sprintf("cachex: RegisterModelCache[%s] requires KeyFields (gorm schema has no PrioritizedPrimaryField)", modelType.String()))
		}
	}
	keyFields := make([]modelKeyField[M], 0, len(keyNames))
	for _, name := range keyNames {
		sf := s.LookUpField(name)
		if sf == nil {
			panic(fmt.Sprintf("cachex: RegisterModelCache[%s] key field %q not found in gorm schema", modelType.String(), name))
		}
		if sf.DBName == "" {
			panic(fmt.Sprintf("cachex: RegisterModelCache[%s] key field %q has no db name", modelType.String(), name))
		}
		keyFields = append(keyFields, modelKeyField[M]{dbName: sf.DBName, extractor: buildExtractor[M](sf)})
	}

	cache := &ModelCache[M]{
		db:        b.db,
		tableName: s.Table,
		keyFields: keyFields,
		separator: mathx.IfNotEmpty(b.separator, ":"),
	}

	// 合并 base opts + BatchLoader + extra opts，注册底层 KVCache（无全量 loader，按需回源）
	allOpts := make([]KVOption, 0, len(b.base.opts)+len(b.extraOpts)+1)
	if b.base != nil {
		allOpts = append(allOpts, b.base.opts...)
	}
	allOpts = append(allOpts, WithKVBatchLoader[string, M](cache.batchLoad))
	allOpts = append(allOpts, b.extraOpts...)
	cacheName := fmt.Sprintf("%s:model", s.Table)
	RegisterKV[string, M](cacheName, nil, allOpts...)
	cache.kv = MustGetKV[string, M](cacheName)

	// 注册到全局表
	modelCacheRegistryMu.Lock()
	defer modelCacheRegistryMu.Unlock()
	if _, exists := modelCacheRegistry[modelType]; exists {
		panic(fmt.Sprintf("cachex: ModelCache[%s] already registered", modelType.String()))
	}
	modelCacheRegistry[modelType] = cache
	return cache
}

// keyOf 从 model 实例提取复合 key（unsafe 提取各键字段，零反射）
func (c *ModelCache[M]) keyOf(m *M) string {
	if m == nil {
		return ""
	}
	if len(c.keyFields) == 1 {
		return c.keyFields[0].extractor(m)
	}
	parts := make([]string, len(c.keyFields))
	for i, kf := range c.keyFields {
		parts[i] = kf.extractor(m)
	}
	return strings.Join(parts, c.separator)
}

// buildKey 校验并拼接键值（个数必须与注册的 KeyFields 一致）
func (c *ModelCache[M]) buildKey(keyValues ...string) (string, error) {
	if len(keyValues) != len(c.keyFields) {
		return "", fmt.Errorf("cachex: ModelCache[%s] key fields mismatch: expect %d values, got %d",
			c.tableName, len(c.keyFields), len(keyValues))
	}
	return strings.Join(keyValues, c.separator), nil
}

// batchLoad BatchLoader：按缺失 keys 精准回源 DB
// 单键字段走 IN 查询；复合键走 (a=? AND b=?) OR (a=? AND b=?) 组合查询
func (c *ModelCache[M]) batchLoad(ctx context.Context, keys []string) (map[string]M, error) {
	result := make(map[string]M, len(keys))
	if len(keys) == 0 {
		return result, nil
	}

	// 解析并过滤非法 key（分段数与键字段数不符的跳过）
	parsed := make([][]string, 0, len(keys))
	for _, k := range keys {
		parts := strings.Split(k, c.separator)
		if len(parts) == len(c.keyFields) {
			parsed = append(parsed, parts)
		}
	}
	if len(parsed) == 0 {
		return result, nil
	}

	query := c.db.WithContext(ctx).Table(c.tableName)
	if len(c.keyFields) == 1 {
		vals := make([]string, len(parsed))
		for i, p := range parsed {
			vals[i] = p[0]
		}
		query = query.Where(fmt.Sprintf("%s IN ?", c.keyFields[0].dbName), vals)
	} else {
		// 复合键：N 组 (a=? AND b=?) 用 OR 连接，一次查询回源
		conds := make([]string, len(parsed))
		args := make([]interface{}, 0, len(parsed)*len(c.keyFields))
		for i, p := range parsed {
			eq := make([]string, len(c.keyFields))
			for j, kf := range c.keyFields {
				eq[j] = fmt.Sprintf("%s = ?", kf.dbName)
				args = append(args, p[j])
			}
			conds[i] = "(" + strings.Join(eq, " AND ") + ")"
		}
		query = query.Where(strings.Join(conds, " OR "), args...)
	}

	var items []M
	if err := query.Find(&items).Error; err != nil {
		return nil, err
	}
	for i := range items {
		if key := c.keyOf(&items[i]); key != "" {
			result[key] = items[i]
		}
	}
	return result, nil
}

// Get 按键字段值读取整实例（本地 → Redis → DB 按需回源）
// keyValues 顺序必须与注册的 KeyFields 一致
func (c *ModelCache[M]) Get(ctx context.Context, keyValues ...string) (M, bool, error) {
	key, err := c.buildKey(keyValues...)
	if err != nil {
		var zero M
		return zero, false, err
	}
	return c.kv.Get(ctx, key)
}

// Set 写穿整实例缓存（本地 + Redis + 失效广播）
func (c *ModelCache[M]) Set(ctx context.Context, m *M) error {
	if m == nil {
		return nil
	}
	return c.kv.Set(ctx, c.keyOf(m), *m)
}

// Delete 删除整实例缓存（从 model 实例提取复合 key，直接操作底层 KV）
func (c *ModelCache[M]) Delete(ctx context.Context, m *M) error {
	if m == nil {
		return nil
	}
	return c.kv.Delete(ctx, c.keyOf(m))
}

// DeleteByKey 按键字段值删除整实例缓存（无需 model 实例）
func (c *ModelCache[M]) DeleteByKey(ctx context.Context, keyValues ...string) error {
	key, err := c.buildKey(keyValues...)
	if err != nil {
		return err
	}
	return c.kv.Delete(ctx, key)
}

// InvalidateAll 清空该 model 的全部缓存条目（本地 + Redis + 广播 clear）
// 适用于按 filters 批量更新/删除后无法定位受影响 key 的兜底失效
func (c *ModelCache[M]) InvalidateAll(ctx context.Context) error {
	return c.kv.Clear(ctx)
}

// TableName 返回 gorm 解析的表名（供上层 Repository 构建原生 SQL 复用）
func (c *ModelCache[M]) TableName() string {
	return c.tableName
}

// GetModelCache 按 model 类型获取已注册的 ModelCache
func GetModelCache[M any]() (*ModelCache[M], error) {
	var m M
	t := reflect.TypeOf(m)
	modelCacheRegistryMu.RLock()
	c, ok := modelCacheRegistry[t]
	modelCacheRegistryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("cachex: ModelCache[%s] not registered", t.String())
	}
	typed, ok := c.(*ModelCache[M])
	if !ok {
		return nil, fmt.Errorf("cachex: ModelCache[%s] type mismatch", t.String())
	}
	return typed, nil
}

// MustGetModelCache 按 model 类型获取已注册的 ModelCache，未注册时 panic
//
//	cachex.MustGetModelCache[models.PaymentReceiveConfigModel]().Get(ctx, tenantID, platformID)
func MustGetModelCache[M any]() *ModelCache[M] {
	c, err := GetModelCache[M]()
	if err != nil {
		panic(err)
	}
	return c
}

// ResetModelCacheRegistry 清空全局注册表（仅测试用）
func ResetModelCacheRegistry() {
	modelCacheRegistryMu.Lock()
	defer modelCacheRegistryMu.Unlock()
	modelCacheRegistry = map[reflect.Type]any{}
}
