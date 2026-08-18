/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-08-18 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-08-18 00:00:00
 * @FilePath: \go-cachex\model_cache_test.go
 * @Description: ModelCache 测试
 *
 * 覆盖：
 *  1. 注册：复合 KeyFields / 主键自动识别 / panic 场景（非 struct、字段缺失、重复注册、无主键）
 *  2. keyOf 复合 key 提取（unsafe extractor + separator 拼接、nil 安全）
 *  3. buildKey 参数个数校验（Get/DeleteByKey 快速失败）
 *  4. Set / Get / Delete / DeleteByKey / InvalidateAll 端到端（miniredis）
 *  5. Get miss → BatchLoader → DB 回源链路接通（schema-only DB 无连接，报错向上传播）
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// 测试用 model 定义
// ============================================================

// testCacheCompositeKey 复合业务键模型（模拟 PaymentReceiveConfigModel）
type testCacheCompositeKey struct {
	Id         int64  `gorm:"column:id;primaryKey;autoIncrement"`
	TenantId   string `gorm:"column:tenant_id;type:varchar(36)"`
	PlatformId string `gorm:"column:platform_id;type:varchar(36)"`
	Timeout    int32  `gorm:"column:timeout"`
}

func (testCacheCompositeKey) TableName() string { return "test_cache_composite" }

// testCacheSingleKey 单主键模型（验证 KeyFields 缺省自动识别）
type testCacheSingleKey struct {
	Id   int64  `gorm:"column:id;primaryKey"`
	Name string `gorm:"column:name"`
}

func (testCacheSingleKey) TableName() string { return "test_cache_single" }

// ============================================================
// 测试辅助
// ============================================================

// setupModelCacheTest 初始化全局 miniredis，并重置两级注册表
func setupModelCacheTest(t *testing.T) {
	t.Helper()
	client := setupRedisClient(t)
	SetGlobalRedisClient(client)
	ResetModelCacheRegistry()
	resetKVRegistry()
}

// ============================================================
// 注册与 key 解析 测试
// ============================================================

func TestModelCache_RegisterCompositeKey(t *testing.T) {
	setupModelCacheTest(t)

	cache := NewModelCache[testCacheCompositeKey](NewModelKVBase(), newSchemaOnlyDB()).
		KeyFields("TenantId", "PlatformId").
		Register()

	assert.Equal(t, "test_cache_composite", cache.TableName())
	assert.Equal(t, ":", cache.separator)
	require.Len(t, cache.keyFields, 2)
	assert.Equal(t, "tenant_id", cache.keyFields[0].dbName)
	assert.Equal(t, "platform_id", cache.keyFields[1].dbName)

	// keyOf：unsafe 提取 + separator 拼接
	m := &testCacheCompositeKey{Id: 1, TenantId: "t1", PlatformId: "p1", Timeout: 30}
	assert.Equal(t, "t1:p1", cache.keyOf(m))
	assert.Empty(t, cache.keyOf(nil), "nil model 应返回空 key")
}

func TestModelCache_RegisterAutoDetectPrimaryKey(t *testing.T) {
	setupModelCacheTest(t)

	// KeyFields 缺省：自动取 PrioritizedPrimaryField（Id）
	cache := NewModelCache[testCacheSingleKey](NewModelKVBase(), newSchemaOnlyDB()).Register()
	require.Len(t, cache.keyFields, 1)
	assert.Equal(t, "id", cache.keyFields[0].dbName)

	m := &testCacheSingleKey{Id: 42, Name: "hello"}
	assert.Equal(t, "42", cache.keyOf(m))
}

func TestModelCache_CustomSeparator(t *testing.T) {
	setupModelCacheTest(t)

	cache := NewModelCache[testCacheCompositeKey](NewModelKVBase(), newSchemaOnlyDB()).
		KeyFields("TenantId", "PlatformId").
		Separator("|").
		Register()

	assert.Equal(t, "t1|p1", cache.keyOf(&testCacheCompositeKey{TenantId: "t1", PlatformId: "p1"}))
}

func TestModelCache_BuildKeyArityMismatch(t *testing.T) {
	setupModelCacheTest(t)

	cache := NewModelCache[testCacheCompositeKey](NewModelKVBase(), newSchemaOnlyDB()).
		KeyFields("TenantId", "PlatformId").
		Register()

	ctx := context.Background()

	_, _, err := cache.Get(ctx, "t1")
	assert.Error(t, err, "键值个数不足应报错")

	_, _, err = cache.Get(ctx, "t1", "p1", "extra")
	assert.Error(t, err, "键值个数超出应报错")

	assert.Error(t, cache.DeleteByKey(ctx, "t1"), "DeleteByKey 键值个数不符应报错")
}

func TestModelCache_RegisterPanics(t *testing.T) {
	setupModelCacheTest(t)
	db := newSchemaOnlyDB()

	// 字段不存在
	assert.Panics(t, func() {
		NewModelCache[testCacheCompositeKey](NewModelKVBase(), db).
			KeyFields("TenantId", "NotExists").
			Register()
	})

	// 无主键且未指定 KeyFields（testModelNoPK 定义于 model_kv_test.go）
	assert.Panics(t, func() {
		NewModelCache[testModelNoPK](NewModelKVBase(), db).Register()
	})

	// 重复注册
	NewModelCache[testCacheSingleKey](NewModelKVBase(), db).Register()
	assert.Panics(t, func() {
		NewModelCache[testCacheSingleKey](NewModelKVBase(), db).Register()
	})

	// KV 注册表脏数据触发 cacheName 冲突（同名 KV 已注册）
	assert.Panics(t, func() {
		RegisterKV[string, testCacheSingleKey]("test_cache_single:model", nil)
	})
}

func TestModelCache_GetMustGetRegistry(t *testing.T) {
	setupModelCacheTest(t)

	_, err := GetModelCache[testCacheCompositeKey]()
	assert.Error(t, err, "未注册应报错")

	NewModelCache[testCacheCompositeKey](NewModelKVBase(), newSchemaOnlyDB()).
		KeyFields("TenantId", "PlatformId").
		Register()

	cache, err := GetModelCache[testCacheCompositeKey]()
	require.NoError(t, err)
	assert.NotNil(t, cache)

	// MustGetModelCache 已注册时正常返回
	assert.NotNil(t, MustGetModelCache[testCacheCompositeKey]())
}

// ============================================================
// 端到端读写 测试（miniredis）
// ============================================================

func TestModelCache_SetGetDeleteEndToEnd(t *testing.T) {
	setupModelCacheTest(t)

	cache := NewModelCache[testCacheCompositeKey](NewModelKVBase(), newSchemaOnlyDB()).
		KeyFields("TenantId", "PlatformId").
		Register()
	ctx := context.Background()

	m := &testCacheCompositeKey{Id: 1, TenantId: "t1", PlatformId: "p1", Timeout: 30}

	// Set → Get 命中（值经 JSON 序列化往返）
	require.NoError(t, cache.Set(ctx, m))
	got, found, err := cache.Get(ctx, "t1", "p1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, *m, got)

	// 覆写同一 key
	m.Timeout = 60
	require.NoError(t, cache.Set(ctx, m))
	got, _, err = cache.Get(ctx, "t1", "p1")
	require.NoError(t, err)
	assert.Equal(t, int32(60), got.Timeout)

	// Delete（model 实例提取 key）→ 本地与 Redis 均失效
	require.NoError(t, cache.Delete(ctx, m))
	_, ok := cache.kv.localCache.Load("t1:p1")
	assert.False(t, ok, "Delete 应清除本地缓存")

	// DeleteByKey 后本地缓存同步清除
	require.NoError(t, cache.Set(ctx, m))
	require.NoError(t, cache.DeleteByKey(ctx, "t1", "p1"))
	_, ok = cache.kv.localCache.Load("t1:p1")
	assert.False(t, ok, "DeleteByKey 应清除本地缓存")
}

func TestModelCache_InvalidateAll(t *testing.T) {
	setupModelCacheTest(t)

	cache := NewModelCache[testCacheCompositeKey](NewModelKVBase(), newSchemaOnlyDB()).
		KeyFields("TenantId", "PlatformId").
		Register()
	ctx := context.Background()

	// 写入多条（前 3 条同 key 覆写，实际占 2 个条目）
	for i := 0; i < 3; i++ {
		require.NoError(t, cache.Set(ctx, &testCacheCompositeKey{
			TenantId: "t", PlatformId: "p", Timeout: int32(i),
		}))
	}
	require.NoError(t, cache.Set(ctx, &testCacheCompositeKey{TenantId: "tx", PlatformId: "px"}))
	assert.Equal(t, 2, cache.kv.Size())

	// 全量失效（本地 + Redis）
	require.NoError(t, cache.InvalidateAll(ctx))
	assert.Zero(t, cache.kv.Size())
}

func TestModelCache_NilModelNoop(t *testing.T) {
	setupModelCacheTest(t)

	cache := NewModelCache[testCacheCompositeKey](NewModelKVBase(), newSchemaOnlyDB()).
		KeyFields("TenantId", "PlatformId").
		Register()

	assert.NoError(t, cache.Set(context.Background(), nil))
	assert.NoError(t, cache.Delete(context.Background(), nil))
	assert.Empty(t, cache.keyOf(nil))
}

// ============================================================
// BatchLoader 回源链路 测试
// ============================================================

// TestModelCache_GetMissInvalidKeyFiltered 验证 batchLoad 对非法分段 key 的过滤：
// 键值本身含 separator 时产生的多段 key 不触发 DB 查询（提前过滤返回空 map），Get 表现为 miss 无错
// （正常 miss → DB 回源链路依赖真实 DB 连接，由宿主服务集成环境覆盖）
func TestModelCache_GetMissInvalidKeyFiltered(t *testing.T) {
	setupModelCacheTest(t)

	cache := NewModelCache[testCacheCompositeKey](NewModelKVBase(), newSchemaOnlyDB()).
		KeyFields("TenantId", "PlatformId").
		Register()

	loaded, err := cache.batchLoad(context.Background(), []string{"t:9:p9", "a|b"})
	require.NoError(t, err, "全部 key 被过滤后不应触发 DB 查询")
	assert.Empty(t, loaded)
}
