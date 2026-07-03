/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-07-01 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-07-03 17:55:06
 * @FilePath: \go-cachex\model_kv.go
 * @Description: ModelKVCache —— 基于 gorm 反射的泛型 model KV 缓存（pbmo 风格）
 *
 * ============================================================================
 * 设计目标
 * ============================================================================
 * 一行注册：自动通过 gorm 反射获取表名/列名，零 lambda、零 Select、零 name
 * 业务侧通过 MustGetModelKV[M]().Set/Delete 自动维护所有字段缓存
 *
 * 性能优化：
 *   1. unsafe 偏移读取字段（注册时预编译 extractor，零运行时反射）
 *   2. 共享 loadAll（N 个字段缓存只查 1 次 DB，短 TTL 复用）
 *   3. 缓存 *KVCache 引用（Set/Delete 不查全局 map）
 *
 * ============================================================================
 * 使用方式
 * ============================================================================
 * 1. 定义模块级 base（共享 namespace/ttl/refreshInterval）：
 *
 *	gameBase := cachex.NewModelKVBase().
 *	    Namespace(constants.KVNamespaceGame).
 *	    TTL(constants.TTLKVCacheGame).
 *	    RefreshInterval(constants.KVCacheRefreshInterval)
 *
 * 2. 各 model 一行注册（自动 gorm 查询，反射提取字段，主键自动识别）：
 *
 *	cachex.NewModelKV[models.GameBrandModel](gameBase, gwglobal.DB).
 *	    Field(constants.KVGameBrand, "Name").
 *	    Field(constants.KVGameBrandCode, "Code").
 *	    Register()
 *
 * 3. Service 层 Create/Update/Delete 自动维护所有字段缓存：
 *
 *	_ = cachex.MustGetModelKV[models.GameBrandModel]().Set(ctx, m)
 *	_ = cachex.MustGetModelKV[models.GameBrandModel]().Delete(ctx, m)
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ============================================================
// 公共类型
// ============================================================

// FieldCacheConfig 字段缓存配置：一个 model 字段对应一个独立的 KV cache
type FieldCacheConfig struct {
	CacheName string // KV cache 名称（全局唯一，如 constants.KVGameBrand）
	FieldName string // model Go 字段名（PascalCase，如 "Name"、"Code"）
}

// ModelKVBase 共享 KV 选项的 base（preset），用于多个 model 复用 namespace/ttl/refreshInterval
//
//	gameBase := cachex.NewModelKVBase().
//	    Namespace(constants.KVNamespaceGame).
//	    TTL(constants.TTLKVCacheGame).
//	    RefreshInterval(constants.KVCacheRefreshInterval)
type ModelKVBase struct {
	opts []KVOption
}

// NewModelKVBase 创建 base preset
func NewModelKVBase() *ModelKVBase {
	return &ModelKVBase{}
}

// Namespace 设置命名空间（透传到底层 KVCache）
func (b *ModelKVBase) Namespace(ns string) *ModelKVBase {
	b.opts = append(b.opts, WithKVNamespace(ns))
	return b
}

// TTL 设置缓存过期时间
func (b *ModelKVBase) TTL(ttl time.Duration) *ModelKVBase {
	b.opts = append(b.opts, WithKVTTL(ttl))
	return b
}

// RefreshInterval 设置自动刷新间隔
func (b *ModelKVBase) RefreshInterval(d time.Duration) *ModelKVBase {
	b.opts = append(b.opts, WithKVRefreshInterval(d))
	return b
}

// Options 追加任意底层 KVOption
func (b *ModelKVBase) Options(opts ...KVOption) *ModelKVBase {
	b.opts = append(b.opts, opts...)
	return b
}

// ============================================================
// Builder
// ============================================================

// ModelKVBuilder 泛型 model KV 缓存 builder（链式配置后 Register）
type ModelKVBuilder[M any] struct {
	base      *ModelKVBase
	db        *gorm.DB
	keyField  string
	fields    []FieldCacheConfig
	extraOpts []KVOption
	loadTTL   time.Duration // 共享 loadAll 的复用窗口，默认 1s
}

// NewModelKV 创建指定 model 类型的 builder，继承 base 的 opts
//
//	cachex.NewModelKV[models.GameBrandModel](gameBase, gwglobal.DB)
func NewModelKV[M any](base *ModelKVBase, db *gorm.DB) *ModelKVBuilder[M] {
	b := &ModelKVBuilder[M]{base: base, db: db, loadTTL: time.Second}
	return b
}

// KeyField 设置主键字段名（model Go 字段名，如 "Id"、"TenantId"）
// 可选：未设置时自动从 gorm schema 的 PrioritizedPrimaryField 检测
func (b *ModelKVBuilder[M]) KeyField(name string) *ModelKVBuilder[M] {
	b.keyField = name
	return b
}

// Field 添加字段缓存（cacheName 全局唯一，fieldName 为 model Go 字段名）
func (b *ModelKVBuilder[M]) Field(cacheName, fieldName string) *ModelKVBuilder[M] {
	b.fields = append(b.fields, FieldCacheConfig{CacheName: cacheName, FieldName: fieldName})
	return b
}

// LoadTTL 设置共享 loadAll 的复用窗口（默认 1s，N 个字段缓存只查 1 次 DB）
func (b *ModelKVBuilder[M]) LoadTTL(ttl time.Duration) *ModelKVBuilder[M] {
	b.loadTTL = ttl
	return b
}

// ExtraOptions 追加 base 之外的额外 KVOption（覆盖或补充）
func (b *ModelKVBuilder[M]) ExtraOptions(opts ...KVOption) *ModelKVBuilder[M] {
	b.extraOpts = append(b.extraOpts, opts...)
	return b
}

// Register 注册到全局表，返回 ModelKVCache 实例
func (b *ModelKVBuilder[M]) Register() *ModelKVCache[M] {
	return registerModelKV[M](b)
}

// ============================================================
// ModelKVCache
// ============================================================

// fieldExtractor 预编译的字段提取器（unsafe 偏移读取，零运行时反射）
type fieldExtractor[M any] func(*M) string

// fieldKVEntry 单个字段缓存条目（持有 *KVCache 引用 + extractor）
type fieldKVEntry[M any] struct {
	cacheName string
	kv        *KVCache[string, string]
	extractor fieldExtractor[M]
}

// ModelKVCache 泛型 model KV 缓存
//
// 一个 model 可对应多个字段缓存，通过 gorm 自动查询 + unsafe 反射提取
type ModelKVCache[M any] struct {
	db            *gorm.DB
	tableName     string
	selectColumns []string // keyField 列 + 所有 field 列（db 列名）
	keyExtractor  fieldExtractor[M]
	fieldKVs      []fieldKVEntry[M]
	modelType     reflect.Type
	// 共享 loadAll（N 个字段缓存复用一次查询结果）
	cachedItems   []*M
	cachedItemsAt time.Time
	cacheMu       sync.RWMutex
	loadTTL       time.Duration
}

// 全局注册表（按 reflect.Type 索引，业务侧 MustGetModelKV[M]() 无需传 name）
var (
	modelKVRegistry   = map[reflect.Type]any{}
	modelKVRegistryMu sync.RWMutex
	schemaCache       sync.Map // gorm schema 解析缓存
)

// registerModelKV 内部注册实现
func registerModelKV[M any](b *ModelKVBuilder[M]) *ModelKVCache[M] {
	var m M
	modelType := reflect.TypeOf(m)
	if modelType.Kind() != reflect.Struct {
		panic(fmt.Sprintf("cachex: RegisterModelKV requires M to be a struct, got %s", modelType.Kind()))
	}
	if len(b.fields) == 0 {
		panic(fmt.Sprintf("cachex: RegisterModelKV[%s] requires at least one Field", modelType.String()))
	}

	// gorm schema 解析表名 + 列名
	s, err := parseSchema[M](b.db)
	if err != nil {
		panic(fmt.Sprintf("cachex: RegisterModelKV[%s] schema parse failed: %v", modelType.String(), err))
	}

	// 解析主键字段：显式 KeyField 优先；未设置则从 gorm schema 的 PrioritizedPrimaryField 自动识别
	keyFieldName := b.keyField
	if keyFieldName == "" {
		if pf := s.PrioritizedPrimaryField; pf != nil {
			keyFieldName = pf.Name
		} else {
			panic(fmt.Sprintf("cachex: RegisterModelKV[%s] requires KeyField (gorm schema has no PrioritizedPrimaryField)", modelType.String()))
		}
	}
	keySchemaField := s.LookUpField(keyFieldName)
	if keySchemaField == nil {
		panic(fmt.Sprintf("cachex: RegisterModelKV[%s] key field %q not found in gorm schema", modelType.String(), keyFieldName))
	}
	keyExtractor := buildExtractor[M](keySchemaField)
	selectColumns := []string{keySchemaField.DBName}

	// 预编译各字段提取器
	fieldKVs := make([]fieldKVEntry[M], 0, len(b.fields))
	for _, fc := range b.fields {
		sf := s.LookUpField(fc.FieldName)
		if sf == nil {
			panic(fmt.Sprintf("cachex: RegisterModelKV[%s] field %q not found in gorm schema", modelType.String(), fc.FieldName))
		}
		fieldKVs = append(fieldKVs, fieldKVEntry[M]{
			cacheName: fc.CacheName,
			extractor: buildExtractor[M](sf),
		})
		selectColumns = append(selectColumns, sf.DBName)
	}

	cache := &ModelKVCache[M]{
		db:            b.db,
		tableName:     s.Table,
		selectColumns: selectColumns,
		keyExtractor:  keyExtractor,
		fieldKVs:      fieldKVs,
		modelType:     modelType,
		loadTTL:       b.loadTTL,
	}

	// 合并 base opts + extra opts
	allOpts := make([]KVOption, 0, len(b.base.opts)+len(b.extraOpts))
	if b.base != nil {
		allOpts = append(allOpts, b.base.opts...)
	}
	allOpts = append(allOpts, b.extraOpts...)

	// 为每个 fieldCache 注册底层 KVCache，loader 共享 loadAll
	for i, fc := range b.fields {
		idx := i // 闭包捕获
		loader := func(ctx context.Context) (map[string]string, error) {
			return cache.loadFieldMap(ctx, idx)
		}
		RegisterKV(fc.CacheName, loader, allOpts...)
		fieldKVs[i].kv = MustGetKV[string, string](fc.CacheName)
	}

	// 注册到全局表
	modelKVRegistryMu.Lock()
	defer modelKVRegistryMu.Unlock()
	if _, exists := modelKVRegistry[modelType]; exists {
		panic(fmt.Sprintf("cachex: ModelKVCache[%s] already registered", modelType.String()))
	}
	modelKVRegistry[modelType] = cache

	return cache
}

// parseSchema 解析 model 的 gorm schema（带缓存）
func parseSchema[M any](db *gorm.DB) (*schema.Schema, error) {
	var m M
	t := reflect.TypeOf(m)
	if cached, ok := schemaCache.Load(t); ok {
		return cached.(*schema.Schema), nil
	}
	s, err := schema.Parse(m, &schemaCache, db.NamingStrategy)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// buildExtractor 预编译字段提取器（unsafe 偏移读取，零运行时反射）
//
// 性能：unsafe.Pointer 偏移读取比 reflect.Value.Field 快 5-10 倍
// 安全性：Go 的 unsafe.Pointer 在非移动 GC 下稳定读取 struct 字段
func buildExtractor[M any](sf *schema.Field) fieldExtractor[M] {
	offset := sf.StructField.Offset
	kind := sf.StructField.Type.Kind()
	switch kind {
	case reflect.String:
		return func(m *M) string {
			if m == nil {
				return ""
			}
			return *(*string)(unsafe.Pointer(uintptr(unsafe.Pointer(m)) + offset))
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(m *M) string {
			if m == nil {
				return ""
			}
			v := *(*int64)(unsafe.Pointer(uintptr(unsafe.Pointer(m)) + offset))
			return strconv.FormatInt(v, 10)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return func(m *M) string {
			if m == nil {
				return ""
			}
			v := *(*uint64)(unsafe.Pointer(uintptr(unsafe.Pointer(m)) + offset))
			return strconv.FormatUint(v, 10)
		}
	case reflect.Bool:
		return func(m *M) string {
			if m == nil {
				return ""
			}
			v := *(*bool)(unsafe.Pointer(uintptr(unsafe.Pointer(m)) + offset))
			return strconv.FormatBool(v)
		}
	default:
		// fallback：复杂类型用 reflect（性能稍差但兼容）
		idx := sf.StructField.Index[0]
		return func(m *M) string {
			if m == nil {
				return ""
			}
			return fmt.Sprintf("%v", reflect.ValueOf(m).Elem().Field(idx).Interface())
		}
	}
}

// loadAll 全量查询 model（共享缓存，N 个 loader 只查 1 次 DB）
func (c *ModelKVCache[M]) loadAll(ctx context.Context) ([]*M, error) {
	// fast path：读锁检查缓存
	c.cacheMu.RLock()
	if c.cachedItems != nil && time.Since(c.cachedItemsAt) < c.loadTTL {
		items := c.cachedItems
		c.cacheMu.RUnlock()
		return items, nil
	}
	c.cacheMu.RUnlock()

	// slow path：写锁查询
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	// double-check，避免多个 goroutine 重复查询
	if c.cachedItems != nil && time.Since(c.cachedItemsAt) < c.loadTTL {
		return c.cachedItems, nil
	}
	var items []*M
	if err := c.db.WithContext(ctx).Table(c.tableName).Select(c.selectColumns).Find(&items).Error; err != nil {
		return nil, err
	}
	c.cachedItems = items
	c.cachedItemsAt = time.Now()
	return items, nil
}

// loadFieldMap 全量加载某个字段的映射（供底层 KVCache loader 调用）
func (c *ModelKVCache[M]) loadFieldMap(ctx context.Context, fieldIdx int) (map[string]string, error) {
	items, err := c.loadAll(ctx)
	if err != nil {
		return nil, err
	}
	extractor := c.fieldKVs[fieldIdx].extractor
	result := make(map[string]string, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		key := c.keyExtractor(item)
		if key == "" {
			continue
		}
		result[key] = extractor(item)
	}
	return result, nil
}

// Set 写入 model 的所有字段缓存（unsafe 提取 key 和各字段 value，零反射）
//
// 多字段时使用 Redis Pipeline 批量 HSET，N 次网络往返合并为 1 次；
// 单字段直接走原路径，避免 pipeline 开销。
func (c *ModelKVCache[M]) Set(ctx context.Context, m *M) error {
	if m == nil {
		return nil
	}
	key := c.keyExtractor(m)

	// 单字段：直接 Set，零 pipeline 开销
	if len(c.fieldKVs) == 1 {
		return c.fieldKVs[0].kv.Set(ctx, key, c.fieldKVs[0].extractor(m))
	}

	// 多字段：Pipeline 批量 HSET，N 次 RTT → 1 次
	client := c.fieldKVs[0].kv.Client()
	pipe := client.Pipeline()
	for _, entry := range c.fieldKVs {
		if err := entry.kv.SetToPipe(pipe, key, entry.extractor(m)); err != nil {
			return err
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	// 广播失效（本节点本地缓存已在 SetToPipe 写入，广播仅通知其他节点）
	for _, entry := range c.fieldKVs {
		entry.kv.PublishInvalidation([]string{key})
	}
	return nil
}

// Delete 删除 model 的所有字段缓存（unsafe 提取 key）
func (c *ModelKVCache[M]) Delete(ctx context.Context, m *M) error {
	if m == nil {
		return nil
	}
	return c.DeleteByKey(ctx, c.keyExtractor(m))
}

// DeleteByKey 按 key 删除所有字段缓存（无需 model 实例）
//
// 多字段时使用 Redis Pipeline 批量 HDel，N 次网络往返合并为 1 次。
func (c *ModelKVCache[M]) DeleteByKey(ctx context.Context, key string) error {
	// 单字段：直接 Delete
	if len(c.fieldKVs) == 1 {
		return c.fieldKVs[0].kv.Delete(ctx, key)
	}

	// 多字段：Pipeline 批量 HDel，N 次 RTT → 1 次
	client := c.fieldKVs[0].kv.Client()
	pipe := client.Pipeline()
	for _, entry := range c.fieldKVs {
		entry.kv.DeleteFromPipe(pipe, key)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	// 广播失效
	for _, entry := range c.fieldKVs {
		entry.kv.PublishInvalidation([]string{key})
	}
	return nil
}

// GetModelKV 按 model 类型获取已注册的 ModelKVCache
func GetModelKV[M any]() (*ModelKVCache[M], error) {
	var m M
	t := reflect.TypeOf(m)
	modelKVRegistryMu.RLock()
	c, ok := modelKVRegistry[t]
	modelKVRegistryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("cachex: ModelKVCache[%s] not registered", t.String())
	}
	typed, ok := c.(*ModelKVCache[M])
	if !ok {
		return nil, fmt.Errorf("cachex: ModelKVCache[%s] type mismatch", t.String())
	}
	return typed, nil
}

// MustGetModelKV 按 model 类型获取已注册的 ModelKVCache，未注册时 panic
//
//	cachex.MustGetModelKV[models.GameBrandModel]().Set(ctx, m)
func MustGetModelKV[M any]() *ModelKVCache[M] {
	c, err := GetModelKV[M]()
	if err != nil {
		panic(err)
	}
	return c
}

// ResetModelKVRegistry 清空全局注册表（仅测试用）
func ResetModelKVRegistry() {
	modelKVRegistryMu.Lock()
	defer modelKVRegistryMu.Unlock()
	modelKVRegistry = map[reflect.Type]any{}
}
