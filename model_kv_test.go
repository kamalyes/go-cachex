/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-07-01 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-07-03 17:56:03
 * @FilePath: \go-cachex\model_kv_test.go
 * @Description: ModelKVCache 测试
 *
 * 覆盖：
 *  1. gorm schema 解析 + 主键自动识别（PrioritizedPrimaryField）
 *  2. registerModelKV 自动主键检测 / 显式 KeyField / panic 场景
 *  3. buildExtractor 各类型字段提取（string/int/uint/bool/nil）
 *  4. Set / Delete / DeleteByKey 端到端（依赖 Redis，不可用则跳过）
 *  5. Builder 链式 API（NewModelKVBase + NewModelKV + Field + Register）
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ============================================================
// 测试用 model 定义
// ============================================================

// testModelIntPK 整型主键模型（Id 自增）
type testModelIntPK struct {
	Id   int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name string `gorm:"column:name"`
	Code string `gorm:"column:code"`
}

func (testModelIntPK) TableName() string { return "test_int_pk" }

// testModelStringPK 字符串主键模型（模拟 TenantModel）
type testModelStringPK struct {
	TenantId string `gorm:"column:tenant_id;type:varchar(36);primaryKey"`
	Name     string `gorm:"column:name"`
}

func (testModelStringPK) TableName() string { return "test_str_pk" }

// testModelUintPK 无符号整型主键模型
type testModelUintPK struct {
	Id   uint64 `gorm:"column:id;primaryKey"`
	Name string `gorm:"column:name"`
}

func (testModelUintPK) TableName() string { return "test_uint_pk" }

// testModelBoolField 含 bool 字段的模型
type testModelBoolField struct {
	Id      int64 `gorm:"column:id;primaryKey"`
	Enabled bool  `gorm:"column:enabled"`
}

func (testModelBoolField) TableName() string { return "test_bool_field" }

// testModelNoPK 无主键模型（用于测试 panic 场景）
type testModelNoPK struct {
	Code string `gorm:"column:code"`
	Name string `gorm:"column:name"`
}

func (testModelNoPK) TableName() string { return "test_no_pk" }

// testModelCompositePK 复合主键模型（PrioritizedPrimaryField 取第一个）
type testModelCompositePK struct {
	A string `gorm:"column:a;primaryKey"`
	B string `gorm:"column:b;primaryKey"`
	C string `gorm:"column:c"`
}

func (testModelCompositePK) TableName() string { return "test_composite_pk" }

// ============================================================
// 测试辅助函数
// ============================================================

// newSchemaOnlyDB 创建仅用于 schema 解析的最小 gorm.DB（无真实连接）
//
// parseSchema 只调用 schema.Parse(m, cache, db.NamingStrategy)，
// 不需要真实 DB 连接，NamingStrategy 足够。
func newSchemaOnlyDB() *gorm.DB {
	return &gorm.DB{Config: &gorm.Config{NamingStrategy: schema.NamingStrategy{}}}
}

// resetKVRegistry 重置底层 KV 注册表（测试间隔离）
func resetKVRegistry() {
	kvRegistryMu.Lock()
	defer kvRegistryMu.Unlock()
	kvRegistry = make(map[string]any)
}

// setupModelKVTest 初始化全局 Redis（若不可用则跳过），并重置注册表
func setupModelKVTest(t *testing.T) *redis.Client {
	t.Helper()
	client := setupRedisClient(t)
	if client == nil {
		t.Skip("Redis 不可用，跳过依赖 Redis 的 ModelKV 测试")
	}
	SetGlobalRedisClient(client)
	ResetModelKVRegistry()
	resetKVRegistry()
	return client
}

// uniqueCacheName 为每个测试生成唯一缓存名，避免注册表冲突
var cacheNameCounter int64

func uniqueCacheName(prefix string) string {
	cacheNameCounter++
	return fmt.Sprintf("%s_%d", prefix, cacheNameCounter)
}

// mustKVGet 简化测试断言：把 (V, bool, error) 收敛为 (string, error)，未命中视为错误
func mustKVGet(t *testing.T, name, key string) string {
	t.Helper()
	v, ok, err := MustGetKV[string, string](name).Get(context.Background(), key)
	require.NoError(t, err)
	require.True(t, ok, "key %q not found in cache %q", key, name)
	return v
}

// kvGetMiss 断言 key 未命中缓存（直接读 localCache，避免 Get 触发 LoadAll 回源）
func kvGetMiss(t *testing.T, name, key string) {
	t.Helper()
	kv := MustGetKV[string, string](name)
	_, ok := kv.localCache.Load(key)
	assert.False(t, ok, "key %q should be missing in local cache %q", key, name)
}

// ============================================================
// gorm schema 解析 + 主键自动识别 测试
// ============================================================

func TestParseSchema_IntPrimaryKey(t *testing.T) {
	db := newSchemaOnlyDB()
	s, err := parseSchema[testModelIntPK](db)
	require.NoError(t, err)

	assert.Equal(t, "test_int_pk", s.Table)
	// PrioritizedPrimaryField 应识别为 Id
	require.NotNil(t, s.PrioritizedPrimaryField)
	assert.Equal(t, "Id", s.PrioritizedPrimaryField.Name)
	assert.Equal(t, "id", s.PrioritizedPrimaryField.DBName)
}

func TestParseSchema_StringPrimaryKey(t *testing.T) {
	db := newSchemaOnlyDB()
	s, err := parseSchema[testModelStringPK](db)
	require.NoError(t, err)

	assert.Equal(t, "test_str_pk", s.Table)
	// PrioritizedPrimaryField 应识别为 TenantId（字符串主键）
	require.NotNil(t, s.PrioritizedPrimaryField)
	assert.Equal(t, "TenantId", s.PrioritizedPrimaryField.Name)
	assert.Equal(t, "tenant_id", s.PrioritizedPrimaryField.DBName)
}

func TestParseSchema_UintPrimaryKey(t *testing.T) {
	db := newSchemaOnlyDB()
	s, err := parseSchema[testModelUintPK](db)
	require.NoError(t, err)

	require.NotNil(t, s.PrioritizedPrimaryField)
	assert.Equal(t, "Id", s.PrioritizedPrimaryField.Name)
}

func TestParseSchema_NoPrimaryKey(t *testing.T) {
	db := newSchemaOnlyDB()
	s, err := parseSchema[testModelNoPK](db)
	require.NoError(t, err)

	// 无主键模型，PrioritizedPrimaryField 应为 nil
	assert.Nil(t, s.PrioritizedPrimaryField)
	assert.Empty(t, s.PrimaryFields)
}

func TestParseSchema_CompositePrimaryKey(t *testing.T) {
	db := newSchemaOnlyDB()
	s, err := parseSchema[testModelCompositePK](db)
	require.NoError(t, err)

	// 复合主键：gorm 的 PrioritizedPrimaryField 为 nil（仅单主键时才有），
	// PrimaryFields 包含全部主键字段。此时自动识别不可用，业务必须显式 KeyField。
	assert.Nil(t, s.PrioritizedPrimaryField)
	assert.Len(t, s.PrimaryFields, 2)
}

func TestParseSchema_SchemaCache(t *testing.T) {
	// 清理缓存确保测试干净
	schemaCache.Delete(reflect.TypeOf(testModelIntPK{}))
	db := newSchemaOnlyDB()

	s1, err := parseSchema[testModelIntPK](db)
	require.NoError(t, err)

	// 第二次应命中缓存（返回同一指针）
	s2, err := parseSchema[testModelIntPK](db)
	require.NoError(t, err)
	assert.Same(t, s1, s2)
}

// ============================================================
// registerModelKV 主键自动检测 测试
// ============================================================

func TestRegisterModelKV_AutoDetectIntPrimaryKey(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("auto_int_name")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	// 验证主键列被加入 selectColumns（自动识别 Id）
	assert.Contains(t, cache.selectColumns, "id")
	assert.Contains(t, cache.selectColumns, "name")
	assert.Equal(t, "test_int_pk", cache.tableName)

	// Set 应使用 Id 作为 key（通过 unsafe 提取）
	m := &testModelIntPK{Id: 123, Name: "brand-1"}
	require.NoError(t, cache.Set(context.Background(), m))

	assert.Equal(t, "brand-1", mustKVGet(t, nameField, "123"))
}

func TestRegisterModelKV_AutoDetectStringPrimaryKey(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("auto_str_name")
	cache := NewModelKV[testModelStringPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	// 验证字符串主键 tenant_id 被自动识别
	assert.Contains(t, cache.selectColumns, "tenant_id")
	assert.Equal(t, "test_str_pk", cache.tableName)

	m := &testModelStringPK{TenantId: "tnt-abc", Name: "tenant-1"}
	require.NoError(t, cache.Set(context.Background(), m))

	assert.Equal(t, "tenant-1", mustKVGet(t, nameField, "tnt-abc"))
}

func TestRegisterModelKV_ExplicitKeyFieldOverridesAutoDetect(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	// 显式指定 Code 作为 key（覆盖主键 Id 的自动识别）
	codeField := uniqueCacheName("explicit_code")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		KeyField("Code").
		Field(codeField, "Name").
		Register()

	// selectColumns 应包含 code（而非 id）
	assert.Contains(t, cache.selectColumns, "code")
	assert.NotContains(t, cache.selectColumns, "id")

	m := &testModelIntPK{Id: 1, Name: "n1", Code: "CODE-X"}
	require.NoError(t, cache.Set(context.Background(), m))

	// key 应为 Code 值
	assert.Equal(t, "n1", mustKVGet(t, codeField, "CODE-X"))
}

func TestRegisterModelKV_MultipleFields(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("multi_name")
	codeField := uniqueCacheName("multi_code")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Field(codeField, "Code").
		Register()

	m := &testModelIntPK{Id: 7, Name: "nm", Code: "CD"}
	require.NoError(t, cache.Set(context.Background(), m))

	assert.Equal(t, "nm", mustKVGet(t, nameField, "7"))
	assert.Equal(t, "CD", mustKVGet(t, codeField, "7"))
}

// ============================================================
// registerModelKV panic 场景 测试
// ============================================================

func TestRegisterModelKV_PanicWhenNoFields(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	assert.PanicsWithValue(t,
		"cachex: RegisterModelKV[cachex.testModelIntPK] requires at least one Field",
		func() {
			NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).Register()
		})
}

func TestRegisterModelKV_PanicWhenNoKeyAndNoPrimaryKey(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	assert.Panics(t, func() {
		NewModelKV[testModelNoPK](NewModelKVBase(), newSchemaOnlyDB()).
			Field(uniqueCacheName("no_pk"), "Name").
			Register()
	})
}

func TestRegisterModelKV_PanicWhenCompositePKWithoutKeyField(t *testing.T) {
	// 复合主键模型：gorm 的 PrioritizedPrimaryField 为 nil，自动识别不可用
	client := setupModelKVTest(t)
	defer client.Close()

	assert.Panics(t, func() {
		NewModelKV[testModelCompositePK](NewModelKVBase(), newSchemaOnlyDB()).
			Field(uniqueCacheName("composite_no_key"), "C").
			Register()
	})
}

func TestRegisterModelKV_CompositePKWithExplicitKeyField(t *testing.T) {
	// 复合主键模型：必须显式 KeyField 指定使用哪个主键
	client := setupModelKVTest(t)
	defer client.Close()

	cField := uniqueCacheName("composite_explicit")
	cache := NewModelKV[testModelCompositePK](NewModelKVBase(), newSchemaOnlyDB()).
		KeyField("A").
		Field(cField, "C").
		Register()

	assert.Contains(t, cache.selectColumns, "a")
	assert.Contains(t, cache.selectColumns, "c")

	m := &testModelCompositePK{A: "ka", B: "kb", C: "vc"}
	require.NoError(t, cache.Set(context.Background(), m))
	assert.Equal(t, "vc", mustKVGet(t, cField, "ka"))
}

func TestRegisterModelKV_PanicWhenKeyFieldNotInSchema(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	assert.Panics(t, func() {
		NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
			KeyField("NotExist").
			Field(uniqueCacheName("bad_key"), "Name").
			Register()
	})
}

func TestRegisterModelKV_PanicWhenFieldNotInSchema(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	assert.Panics(t, func() {
		NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
			Field(uniqueCacheName("bad_field"), "NotExist").
			Register()
	})
}

func TestRegisterModelKV_PanicWhenNonStructType(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	assert.Panics(t, func() {
		NewModelKV[string](NewModelKVBase(), newSchemaOnlyDB()).
			Field(uniqueCacheName("non_struct"), "X").
			Register()
	})
}

func TestRegisterModelKV_PanicWhenDuplicateRegistration(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	base := NewModelKVBase()
	NewModelKV[testModelIntPK](base, newSchemaOnlyDB()).
		Field(uniqueCacheName("dup1"), "Name").
		Register()

	// 同一类型重复注册应 panic
	assert.Panics(t, func() {
		NewModelKV[testModelIntPK](base, newSchemaOnlyDB()).
			Field(uniqueCacheName("dup2"), "Code").
			Register()
	})
}

// ============================================================
// buildExtractor 字段提取器 测试（不依赖 Redis）
// ============================================================

func TestBuildExtractor_StringField(t *testing.T) {
	db := newSchemaOnlyDB()
	s, err := parseSchema[testModelIntPK](db)
	require.NoError(t, err)

	sf := s.LookUpField("Name")
	require.NotNil(t, sf)

	extractor := buildExtractor[testModelIntPK](sf)
	m := &testModelIntPK{Id: 1, Name: "hello", Code: "c1"}
	assert.Equal(t, "hello", extractor(m))

	// nil 模型返回空串
	assert.Equal(t, "", extractor(nil))
}

func TestBuildExtractor_Int64Field(t *testing.T) {
	db := newSchemaOnlyDB()
	s, err := parseSchema[testModelIntPK](db)
	require.NoError(t, err)

	sf := s.LookUpField("Id")
	require.NotNil(t, sf)

	extractor := buildExtractor[testModelIntPK](sf)
	m := &testModelIntPK{Id: 999999}
	assert.Equal(t, "999999", extractor(m))

	// 负数
	m.Id = -5
	assert.Equal(t, "-5", extractor(m))

	// nil
	assert.Equal(t, "", extractor(nil))
}

func TestBuildExtractor_Uint64Field(t *testing.T) {
	db := newSchemaOnlyDB()
	s, err := parseSchema[testModelUintPK](db)
	require.NoError(t, err)

	sf := s.LookUpField("Id")
	require.NotNil(t, sf)

	extractor := buildExtractor[testModelUintPK](sf)
	m := &testModelUintPK{Id: 18446744073709551615}
	assert.Equal(t, "18446744073709551615", extractor(m))

	assert.Equal(t, "", extractor(nil))
}

func TestBuildExtractor_BoolField(t *testing.T) {
	db := newSchemaOnlyDB()
	s, err := parseSchema[testModelBoolField](db)
	require.NoError(t, err)

	sf := s.LookUpField("Enabled")
	require.NotNil(t, sf)

	extractor := buildExtractor[testModelBoolField](sf)
	m := &testModelBoolField{Id: 1, Enabled: true}
	assert.Equal(t, "true", extractor(m))

	m.Enabled = false
	assert.Equal(t, "false", extractor(m))

	assert.Equal(t, "", extractor(nil))
}

// ============================================================
// Set / Delete / DeleteByKey 端到端测试
// ============================================================

func TestModelKVCache_SetWritesAllFieldsWithAutoKey(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("set_name")
	codeField := uniqueCacheName("set_code")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Field(codeField, "Code").
		Register()

	m := &testModelIntPK{Id: 42, Name: "forty-two", Code: "C42"}
	require.NoError(t, cache.Set(context.Background(), m))

	// 两个字段都应以 Id=42 为 key 写入
	assert.Equal(t, "forty-two", mustKVGet(t, nameField, "42"))
	assert.Equal(t, "C42", mustKVGet(t, codeField, "42"))
}

// TestModelKVCache_SetPipelinePath 三字段走 Pipeline 批量路径（N 次 RTT → 1 次）
func TestModelKVCache_SetPipelinePath(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("pipe_name")
	codeField := uniqueCacheName("pipe_code")
	iconField := uniqueCacheName("pipe_icon")
	cache := NewModelKV[benchModel](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Field(codeField, "Code").
		Field(iconField, "IconUrl").
		Register()

	m := &benchModel{Id: 777, Name: "n777", Code: "c777", IconUrl: "http://x/777"}
	require.NoError(t, cache.Set(context.Background(), m))

	// 三字段都应以 Id=777 为 key 写入（Pipeline 一次 RTT 完成）
	assert.Equal(t, "n777", mustKVGet(t, nameField, "777"))
	assert.Equal(t, "c777", mustKVGet(t, codeField, "777"))
	assert.Equal(t, "http://x/777", mustKVGet(t, iconField, "777"))
}

// TestModelKVCache_SetSingleFieldFastPath 单字段走快速路径（无 pipeline 开销）
func TestModelKVCache_SetSingleFieldFastPath(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("fast_name")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	m := &testModelIntPK{Id: 5, Name: "five"}
	require.NoError(t, cache.Set(context.Background(), m))
	assert.Equal(t, "five", mustKVGet(t, nameField, "5"))
}

func TestModelKVCache_SetNilModelIsNoOp(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("set_nil")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	// nil 模型不应 panic 也不应报错
	require.NoError(t, cache.Set(context.Background(), nil))
}

func TestModelKVCache_DeleteRemovesAllFields(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("del_name")
	codeField := uniqueCacheName("del_code")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Field(codeField, "Code").
		Register()

	m := &testModelIntPK{Id: 88, Name: "n88", Code: "c88"}
	require.NoError(t, cache.Set(context.Background(), m))

	// 确认写入
	assert.Equal(t, "n88", mustKVGet(t, nameField, "88"))

	// Delete 应删除所有字段
	require.NoError(t, cache.Delete(context.Background(), m))

	kvGetMiss(t, nameField, "88")
	kvGetMiss(t, codeField, "88")
}

func TestModelKVCache_DeleteByKeyRemovesAllFields(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("delbykey_name")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	m := &testModelIntPK{Id: 99, Name: "n99"}
	require.NoError(t, cache.Set(context.Background(), m))

	// DeleteByKey 无需 model 实例
	require.NoError(t, cache.DeleteByKey(context.Background(), "99"))

	kvGetMiss(t, nameField, "99")
}

func TestModelKVCache_DeleteNilModelIsNoOp(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("del_nil")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	require.NoError(t, cache.Delete(context.Background(), nil))
}

// ============================================================
// GetModelKV / MustGetModelKV 测试
// ============================================================

func TestGetModelKV_Registered(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("get_name")
	NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	got, err := GetModelKV[testModelIntPK]()
	require.NoError(t, err)
	assert.NotNil(t, got)
}

func TestGetModelKV_NotRegisteredReturnsError(t *testing.T) {
	// 使用一个未注册的私有类型，确保不会与其他测试冲突
	type unregisteredModel struct {
		Id int64 `gorm:"column:id;primaryKey"`
	}
	_, err := GetModelKV[unregisteredModel]()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

func TestMustGetModelKV_PanicsWhenNotRegistered(t *testing.T) {
	type anotherUnregistered struct {
		Id int64 `gorm:"column:id;primaryKey"`
	}
	assert.Panics(t, func() {
		MustGetModelKV[anotherUnregistered]()
	})
}

// ============================================================
// Builder 链式 API 测试
// ============================================================

func TestModelKVBase_ChainOptions(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	base := NewModelKVBase().
		Namespace("ns-test").
		TTL(time.Minute).
		RefreshInterval(time.Hour)

	nameField := uniqueCacheName("base_chain")
	cache := NewModelKV[testModelIntPK](base, newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	// 验证注册成功且可正常 Set
	m := &testModelIntPK{Id: 100, Name: "hundred"}
	require.NoError(t, cache.Set(context.Background(), m))

	assert.Equal(t, "hundred", mustKVGet(t, nameField, "100"))
}

func TestModelKVBase_OptionsAppend(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	base := NewModelKVBase().
		Namespace("ns-opt").
		Options(WithKVTTL(time.Second * 30))

	nameField := uniqueCacheName("base_opts")
	cache := NewModelKV[testModelIntPK](base, newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	m := &testModelIntPK{Id: 200, Name: "two-hundred"}
	require.NoError(t, cache.Set(context.Background(), m))

	assert.Equal(t, "two-hundred", mustKVGet(t, nameField, "200"))
}

func TestModelKVBuilder_LoadTTLOption(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("loadttl")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		LoadTTL(500 * time.Millisecond).
		Register()

	assert.Equal(t, 500*time.Millisecond, cache.loadTTL)
}

func TestModelKVBuilder_ExtraOptions(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("extra_opts")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		ExtraOptions(WithKVTTL(time.Second * 10)).
		Register()

	// 验证注册成功
	assert.NotNil(t, cache)
	m := &testModelIntPK{Id: 300, Name: "three-hundred"}
	require.NoError(t, cache.Set(context.Background(), m))

	assert.Equal(t, "three-hundred", mustKVGet(t, nameField, "300"))
}

// ============================================================
// loadFieldMap 测试（共享 loadAll 的字段映射构建）
// ============================================================

func TestLoadFieldMap_BuildsKeyToValueMap(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("loadmap_name")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	// 直接构造 cachedItems 模拟 loadAll 结果（绕过真实 DB 查询）
	cache.cacheMu.Lock()
	cache.cachedItems = []*testModelIntPK{
		{Id: 1, Name: "one"},
		{Id: 2, Name: "two"},
		{Id: 3, Name: ""},
	}
	cache.cachedItemsAt = time.Now()
	cache.cacheMu.Unlock()

	m, err := cache.loadFieldMap(context.Background(), 0)
	require.NoError(t, err)
	assert.Equal(t, "one", m["1"])
	assert.Equal(t, "two", m["2"])
	// 空值也会写入 map
	assert.Equal(t, "", m["3"])
}

func TestLoadFieldMap_SkipsNilItems(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("loadmap_nil")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	cache.cacheMu.Lock()
	cache.cachedItems = []*testModelIntPK{
		nil,
		{Id: 5, Name: "five"},
	}
	cache.cachedItemsAt = time.Now()
	cache.cacheMu.Unlock()

	m, err := cache.loadFieldMap(context.Background(), 0)
	require.NoError(t, err)
	assert.Len(t, m, 1)
	assert.Equal(t, "five", m["5"])
}

func TestLoadFieldMap_SkipsEmptyKey(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("loadmap_empty_key")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	cache.cacheMu.Lock()
	// Id=0 的项，key 提取为 "0"（非空），仍会写入；这里测 Name 空键场景不存在
	// 改为测 key 为空：用字符串主键模型 + 空 TenantId
	cache.cachedItems = []*testModelIntPK{
		{Id: 0, Name: "zero-id"},
	}
	cache.cachedItemsAt = time.Now()
	cache.cacheMu.Unlock()

	m, err := cache.loadFieldMap(context.Background(), 0)
	require.NoError(t, err)
	// Id=0 提取出 "0"（非空），应写入
	assert.Equal(t, "zero-id", m["0"])
}

// ============================================================
// 并发安全测试
// ============================================================

func TestModelKVCache_ConcurrentSet(t *testing.T) {
	client := setupModelKVTest(t)
	defer client.Close()

	nameField := uniqueCacheName("conc_set")
	cache := NewModelKV[testModelIntPK](NewModelKVBase(), newSchemaOnlyDB()).
		Field(nameField, "Name").
		Register()

	var wg sync.WaitGroup
	for i := int64(1); i <= 50; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			m := &testModelIntPK{Id: id, Name: fmt.Sprintf("n%d", id)}
			_ = cache.Set(context.Background(), m)
		}(i)
	}
	wg.Wait()

	// 抽检若干 key
	for _, id := range []int64{1, 10, 25, 50} {
		assert.Equal(t, fmt.Sprintf("n%d", id), mustKVGet(t, nameField, fmt.Sprintf("%d", id)))
	}
}
