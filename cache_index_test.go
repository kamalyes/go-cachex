/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-05-20 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastLastEditTime: 2026-05-20 00:00:00
 * @FilePath: \go-cachex\cache_index_test.go
 * @Description: 缓存索引管理器测试 - 覆盖所有方法和边界条件
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ======================== CacheIndexManager 基础测试 ========================

// TestCacheIndexManager01_RegisterAndGetMembers 测试注册索引和查询成员
func TestCacheIndexManager01_RegisterAndGetMembers(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:register"
	client.Del(ctx, idxKey)

	// 注册多个缓存键
	mgr.Register(ctx, idxKey, "cache:key1", "cache:key2", "cache:key3")

	// 查询成员
	members, err := mgr.GetIndexMembers(ctx, idxKey)
	assert.NoError(t, err)
	assert.Len(t, members, 3)
	assert.ElementsMatch(t, []string{"cache:key1", "cache:key2", "cache:key3"}, members)

	client.Del(ctx, idxKey)
}

// TestCacheIndexManager02_RegisterSingleKey 测试注册单个缓存键
func TestCacheIndexManager02_RegisterSingleKey(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:single"
	client.Del(ctx, idxKey)

	mgr.Register(ctx, idxKey, "cache:single_key")

	members, err := mgr.GetIndexMembers(ctx, idxKey)
	assert.NoError(t, err)
	assert.Len(t, members, 1)
	assert.Contains(t, members, "cache:single_key")

	client.Del(ctx, idxKey)
}

// TestCacheIndexManager03_RegisterDuplicateKey 测试注册重复的缓存键（Set去重）
func TestCacheIndexManager03_RegisterDuplicateKey(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:dup"
	client.Del(ctx, idxKey)

	// 重复注册同一个键
	mgr.Register(ctx, idxKey, "cache:dup_key")
	mgr.Register(ctx, idxKey, "cache:dup_key")
	mgr.Register(ctx, idxKey, "cache:dup_key")

	members, err := mgr.GetIndexMembers(ctx, idxKey)
	assert.NoError(t, err)
	assert.Len(t, members, 1) // Set去重，只有一个

	client.Del(ctx, idxKey)
}

// TestCacheIndexManager04_RegisterToMultipleIndex 测试注册到不同索引集合
func TestCacheIndexManager04_RegisterToMultipleIndex(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey1 := "test_idx:multi1"
	idxKey2 := "test_idx:multi2"
	client.Del(ctx, idxKey1, idxKey2)

	// 同一个缓存键注册到不同索引
	mgr.Register(ctx, idxKey1, "cache:shared_key")
	mgr.Register(ctx, idxKey2, "cache:shared_key")

	members1, err := mgr.GetIndexMembers(ctx, idxKey1)
	assert.NoError(t, err)
	assert.Contains(t, members1, "cache:shared_key")

	members2, err := mgr.GetIndexMembers(ctx, idxKey2)
	assert.NoError(t, err)
	assert.Contains(t, members2, "cache:shared_key")

	client.Del(ctx, idxKey1, idxKey2)
}

// ======================== DeleteByIndex 测试 ========================

// TestCacheIndexManager05_DeleteByIndex 测试按索引原子删除缓存
func TestCacheIndexManager05_DeleteByIndex(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:delete"
	cacheKey1 := "test_cache:delete1"
	cacheKey2 := "test_cache:delete2"
	client.Del(ctx, idxKey, cacheKey1, cacheKey2)

	// 先写入缓存数据
	client.Set(ctx, cacheKey1, "value1", time.Minute)
	client.Set(ctx, cacheKey2, "value2", time.Minute)

	// 注册索引
	mgr.Register(ctx, idxKey, cacheKey1, cacheKey2)

	// 确认缓存存在
	val1, err := client.Get(ctx, cacheKey1).Result()
	assert.NoError(t, err)
	assert.Equal(t, "value1", val1)

	// 按索引删除
	deleted, err := mgr.DeleteByIndex(ctx, idxKey)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	// 确认缓存已被删除
	_, err = client.Get(ctx, cacheKey1).Result()
	assert.Error(t, err) // 应该不存在了

	_, err = client.Get(ctx, cacheKey2).Result()
	assert.Error(t, err)

	// 确认索引集合也被删除
	members, err := mgr.GetIndexMembers(ctx, idxKey)
	assert.NoError(t, err)
	assert.Empty(t, members)
}

// TestCacheIndexManager06_DeleteByMultipleIndex 测试按多个索引删除
func TestCacheIndexManager06_DeleteByMultipleIndex(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey1 := "test_idx:del_multi1"
	idxKey2 := "test_idx:del_multi2"
	cacheKey1 := "test_cache:del_multi1"
	cacheKey2 := "test_cache:del_multi2"
	cacheKey3 := "test_cache:del_multi3"
	client.Del(ctx, idxKey1, idxKey2, cacheKey1, cacheKey2, cacheKey3)

	// 写入缓存
	client.Set(ctx, cacheKey1, "v1", time.Minute)
	client.Set(ctx, cacheKey2, "v2", time.Minute)
	client.Set(ctx, cacheKey3, "v3", time.Minute)

	// 注册到不同索引
	mgr.Register(ctx, idxKey1, cacheKey1, cacheKey2)
	mgr.Register(ctx, idxKey2, cacheKey3)

	// 一次性删除多个索引
	deleted, err := mgr.DeleteByIndex(ctx, idxKey1, idxKey2)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), deleted)

	// 确认所有缓存已删除
	_, err = client.Get(ctx, cacheKey1).Result()
	assert.Error(t, err)
	_, err = client.Get(ctx, cacheKey2).Result()
	assert.Error(t, err)
	_, err = client.Get(ctx, cacheKey3).Result()
	assert.Error(t, err)
}

// TestCacheIndexManager07_DeleteByIndexEmpty 测试删除空索引
func TestCacheIndexManager07_DeleteByIndexEmpty(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:empty_del"
	client.Del(ctx, idxKey)

	// 删除一个没有任何成员的索引
	deleted, err := mgr.DeleteByIndex(ctx, idxKey)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), deleted)
}

// ======================== DeleteByKeys 测试 ========================

// TestCacheIndexManager08_DeleteByKeys 测试按缓存键直接删除
func TestCacheIndexManager08_DeleteByKeys(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	cacheKey1 := "test_cache:direct1"
	cacheKey2 := "test_cache:direct2"
	client.Del(ctx, cacheKey1, cacheKey2)

	// 写入缓存
	client.Set(ctx, cacheKey1, "value1", time.Minute)
	client.Set(ctx, cacheKey2, "value2", time.Minute)

	// 直接按键删除
	err := mgr.DeleteByKeys(ctx, cacheKey1, cacheKey2)
	assert.NoError(t, err)

	// 确认已删除
	_, err = client.Get(ctx, cacheKey1).Result()
	assert.Error(t, err)
	_, err = client.Get(ctx, cacheKey2).Result()
	assert.Error(t, err)
}

// ======================== IndexGroup 测试 ========================

// TestCacheIndexManager09_IndexGroup 测试索引分组
func TestCacheIndexManager09_IndexGroup(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey1 := "test_idx:group1"
	idxKey2 := "test_idx:group2"
	cacheKey1 := "test_cache:group1"
	cacheKey2 := "test_cache:group2"
	client.Del(ctx, idxKey1, idxKey2, cacheKey1, cacheKey2)

	// 写入缓存
	client.Set(ctx, cacheKey1, "v1", time.Minute)
	client.Set(ctx, cacheKey2, "v2", time.Minute)

	// 注册索引
	mgr.Register(ctx, idxKey1, cacheKey1)
	mgr.Register(ctx, idxKey2, cacheKey2)

	// 创建分组
	group := mgr.NewIndexGroup(idxKey1, idxKey2)

	// 验证分组键
	assert.ElementsMatch(t, []string{idxKey1, idxKey2}, group.IndexKeys())

	// 通过分组删除
	deleted, err := group.Delete(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	// 确认缓存已删除
	_, err = client.Get(ctx, cacheKey1).Result()
	assert.Error(t, err)
	_, err = client.Get(ctx, cacheKey2).Result()
	assert.Error(t, err)
}

// TestCacheIndexManager10_IndexGroupAddKeys 测试分组追加索引键
func TestCacheIndexManager10_IndexGroupAddKeys(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	mgr := NewCacheIndexManager(client)

	group := mgr.NewIndexGroup("idx:1")
	assert.Len(t, group.IndexKeys(), 1)

	group.AddIndexKeys("idx:2", "idx:3")
	assert.Len(t, group.IndexKeys(), 3)
	assert.ElementsMatch(t, []string{"idx:1", "idx:2", "idx:3"}, group.IndexKeys())
}

// TestCacheIndexManager11_IndexGroupDelete 测试分组完整删除流程
func TestCacheIndexManager11_IndexGroupDelete(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idx1 := "test_idx:grp_del1"
	idx2 := "test_idx:grp_del2"
	idx3 := "test_idx:grp_del3"
	ck1 := "test_cache:grp_del1"
	ck2 := "test_cache:grp_del2"
	ck3 := "test_cache:grp_del3"
	client.Del(ctx, idx1, idx2, idx3, ck1, ck2, ck3)

	// 写入缓存
	client.Set(ctx, ck1, "v1", time.Minute)
	client.Set(ctx, ck2, "v2", time.Minute)
	client.Set(ctx, ck3, "v3", time.Minute)

	mgr.Register(ctx, idx1, ck1)
	mgr.Register(ctx, idx2, ck2)
	mgr.Register(ctx, idx3, ck3)

	// 创建分组并追加
	group := mgr.NewIndexGroup(idx1, idx2)
	group.AddIndexKeys(idx3)

	deleted, err := group.Delete(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), deleted)
}

// ======================== CacheWrapperWithIndex 测试 ========================

// TestCacheIndexManager12_WrapperWithIndex 测试带索引的缓存包装器
func TestCacheIndexManager12_WrapperWithIndex(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:wrapper"
	cacheKey := "test_cache:wrapper_key"
	client.Del(ctx, idxKey, cacheKey)

	callCount := int32(0)

	loadFunc := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&callCount, 1)
		return "wrapped_data", nil
	}

	// 使用 CacheWrapperWithIndex
	loader := CacheWrapperWithIndex(mgr, idxKey, client, cacheKey, loadFunc, time.Minute)

	// 第一次调用
	result, err := loader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "wrapped_data", result)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 验证索引已注册
	members, err := mgr.GetIndexMembers(ctx, idxKey)
	assert.NoError(t, err)
	assert.Contains(t, members, cacheKey)

	// 第二次调用（缓存命中）
	result2, err := loader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "wrapped_data", result2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	client.Del(ctx, idxKey, cacheKey)
}

// TestCacheIndexManager13_WrapperWithIndexStruct 测试带索引的缓存包装器（结构体）
func TestCacheIndexManager13_WrapperWithIndexStruct(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	type User struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	idxKey := "test_idx:wrapper_struct"
	cacheKey := "test_cache:wrapper_struct"
	client.Del(ctx, idxKey, cacheKey)

	loadFunc := func(ctx context.Context) (*User, error) {
		return &User{ID: 1, Name: "Alice"}, nil
	}

	loader := CacheWrapperWithIndex(mgr, idxKey, client, cacheKey, loadFunc, time.Minute)

	result, err := loader(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 1, result.ID)
	assert.Equal(t, "Alice", result.Name)

	// 验证索引
	members, err := mgr.GetIndexMembers(ctx, idxKey)
	assert.NoError(t, err)
	assert.Contains(t, members, cacheKey)

	client.Del(ctx, idxKey, cacheKey)
}

// TestCacheIndexManager14_WrapperWithIndexThenDelete 测试缓存写入+索引注册后按索引删除
func TestCacheIndexManager14_WrapperWithIndexThenDelete(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:wrapper_del"
	cacheKey := "test_cache:wrapper_del"
	client.Del(ctx, idxKey, cacheKey)

	loadFunc := func(ctx context.Context) (string, error) {
		return "data_to_delete", nil
	}

	loader := CacheWrapperWithIndex(mgr, idxKey, client, cacheKey, loadFunc, time.Minute)

	// 写入缓存+注册索引
	result, err := loader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "data_to_delete", result)

	// 确认缓存存在
	val, err := client.Get(ctx, cacheKey).Result()
	assert.NoError(t, err)
	assert.NotEmpty(t, val)

	// 按索引删除
	deleted, err := mgr.DeleteByIndex(ctx, idxKey)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	// 确认缓存已删除
	_, err = client.Get(ctx, cacheKey).Result()
	assert.Error(t, err)
}

// TestCacheIndexManager15_WrapperWithIndexWithOptions 测试带选项的 CacheWrapperWithIndex
func TestCacheIndexManager15_WrapperWithIndexWithOptions(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:wrapper_opts"
	cacheKey := "test_cache:wrapper_opts"
	client.Del(ctx, idxKey, cacheKey)

	callCount := int32(0)

	loadFunc := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&callCount, 1)
		return "opts_data", nil
	}

	// 使用 WithoutCompression 选项
	loader := CacheWrapperWithIndex(mgr, idxKey, client, cacheKey, loadFunc, time.Minute, WithoutCompression())

	result, err := loader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "opts_data", result)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// 验证索引
	members, err := mgr.GetIndexMembers(ctx, idxKey)
	assert.NoError(t, err)
	assert.Contains(t, members, cacheKey)

	client.Del(ctx, idxKey, cacheKey)
}

// ======================== 边界条件测试 ========================

// TestCacheIndexManager16_NilClient 测试 nil client 安全性
func TestCacheIndexManager16_NilClient(t *testing.T) {
	mgr := NewCacheIndexManager(nil)
	ctx := context.Background()

	// Register 不应 panic
	mgr.Register(ctx, "idx", "key1", "key2")

	// DeleteByIndex 不应 panic
	deleted, err := mgr.DeleteByIndex(ctx, "idx")
	assert.NoError(t, err)
	assert.Equal(t, int64(0), deleted)

	// DeleteByKeys 不应 panic
	err = mgr.DeleteByKeys(ctx, "key1")
	assert.NoError(t, err)

	// GetIndexMembers 不应 panic
	members, err := mgr.GetIndexMembers(ctx, "idx")
	assert.NoError(t, err)
	assert.Nil(t, members)
}

// TestCacheIndexManager17_EmptyParams 测试空参数安全性
func TestCacheIndexManager17_EmptyParams(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	// 空 cacheKeys
	mgr.Register(ctx, "idx")
	mgr.Register(ctx, "idx")

	// 空 idxKey
	mgr.Register(ctx, "", "key1")

	// 空 idxKeys
	deleted, err := mgr.DeleteByIndex(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), deleted)

	// 空 keys
	err = mgr.DeleteByKeys(ctx)
	assert.NoError(t, err)

	// 空 idxKey 查询
	members, err := mgr.GetIndexMembers(ctx, "")
	assert.NoError(t, err)
	assert.Nil(t, members)
}

// TestCacheIndexManager18_NilManagerWrapper 测试 CacheWrapperWithIndex 传入 nil manager
func TestCacheIndexManager18_NilManagerWrapper(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	loadFunc := func(ctx context.Context) (string, error) {
		return "nil_mgr_data", nil
	}

	// nil manager 不应 panic，只是不注册索引
	loader := CacheWrapperWithIndex(nil, "idx", client, "test_cache:nil_mgr", loadFunc, time.Minute)

	result, err := loader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "nil_mgr_data", result)

	client.Del(ctx, "test_cache:nil_mgr")
}

// TestCacheIndexManager19_EmptyIdxKeyWrapper 测试 CacheWrapperWithIndex 空 idxKey
func TestCacheIndexManager19_EmptyIdxKeyWrapper(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	loadFunc := func(ctx context.Context) (string, error) {
		return "empty_idx_data", nil
	}

	// 空 idxKey 不应注册索引
	loader := CacheWrapperWithIndex(mgr, "", client, "test_cache:empty_idx", loadFunc, time.Minute)

	result, err := loader(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "empty_idx_data", result)

	client.Del(ctx, "test_cache:empty_idx")
}

// ======================== 并发测试 ========================

// TestCacheIndexManager20_ConcurrentRegister 测试并发注册
func TestCacheIndexManager20_ConcurrentRegister(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:concurrent"
	client.Del(ctx, idxKey)

	const numGoroutines = 20
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			cacheKey := "test_cache:concurrent_" + string(rune('A'+idx%26))
			mgr.Register(ctx, idxKey, cacheKey)
			done <- true
		}(i)
	}

	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// 验证索引中有成员
	members, err := mgr.GetIndexMembers(ctx, idxKey)
	assert.NoError(t, err)
	assert.NotEmpty(t, members)

	client.Del(ctx, idxKey)
}

// TestCacheIndexManager21_ConcurrentWrapper 测试并发 CacheWrapperWithIndex
func TestCacheIndexManager21_ConcurrentWrapper(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:conc_wrapper"
	cacheKey := "test_cache:conc_wrapper"
	client.Del(ctx, idxKey, cacheKey)

	callCount := int32(0)

	loadFunc := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&callCount, 1)
		return "concurrent_data", nil
	}

	loader := CacheWrapperWithIndex(mgr, idxKey, client, cacheKey, loadFunc, time.Minute)

	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			result, err := loader(ctx)
			assert.NoError(t, err)
			assert.Equal(t, "concurrent_data", result)
			done <- true
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// 验证索引已注册
	members, err := mgr.GetIndexMembers(ctx, idxKey)
	assert.NoError(t, err)
	assert.Contains(t, members, cacheKey)

	client.Del(ctx, idxKey, cacheKey)
}

// ======================== 集成测试：完整业务场景 ========================

// TestCacheIndexManager22_FullBusinessScenario 测试完整业务场景
// 模拟：租户选项缓存 -> 注册索引 -> 数据变更 -> 按索引删除
func TestCacheIndexManager22_FullBusinessScenario(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	// 定义业务索引键
	const (
		idxTenantOptions   = "idx:tenant_options"
		idxRegionOptions   = "idx:region_options"
		idxPlatformOptions = "idx:platform_options"
	)

	// 清理
	client.Del(ctx, idxTenantOptions, idxRegionOptions, idxPlatformOptions)

	// 创建业务分组
	tenantGroup := mgr.NewIndexGroup(idxTenantOptions, idxRegionOptions, idxPlatformOptions)

	// 模拟多个缓存写入
	tenantLoader := CacheWrapperWithIndex(mgr, idxTenantOptions, client, "cache:tenant:true",
		func(ctx context.Context) ([]string, error) { return []string{"tenant1", "tenant2"}, nil }, time.Minute)

	regionLoader := CacheWrapperWithIndex(mgr, idxRegionOptions, client, "cache:region:t1:cn",
		func(ctx context.Context) ([]string, error) { return []string{"CN", "US"}, nil }, time.Minute)

	platformLoader := CacheWrapperWithIndex(mgr, idxPlatformOptions, client, "cache:platform:t1:cn:true",
		func(ctx context.Context) ([]string, error) { return []string{"p1", "p2"}, nil }, time.Minute)

	// 执行缓存加载
	_, err := tenantLoader(ctx)
	require.NoError(t, err)

	_, err = regionLoader(ctx)
	require.NoError(t, err)

	_, err = platformLoader(ctx)
	require.NoError(t, err)

	// 验证所有索引都有注册
	members1, _ := mgr.GetIndexMembers(ctx, idxTenantOptions)
	assert.NotEmpty(t, members1)

	members2, _ := mgr.GetIndexMembers(ctx, idxRegionOptions)
	assert.NotEmpty(t, members2)

	members3, _ := mgr.GetIndexMembers(ctx, idxPlatformOptions)
	assert.NotEmpty(t, members3)

	// 模拟数据变更，通过分组删除所有关联缓存
	deleted, err := tenantGroup.Delete(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), deleted)

	// 确认所有缓存已清理
	for _, idx := range []string{idxTenantOptions, idxRegionOptions, idxPlatformOptions} {
		members, _ := mgr.GetIndexMembers(ctx, idx)
		assert.Empty(t, members)
	}
}

// TestCacheIndexManager23_DeleteByIndexPartialHit 测试部分缓存键已过期的删除
func TestCacheIndexManager23_DeleteByIndexPartialHit(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:partial"
	cacheKey1 := "test_cache:partial1"
	cacheKey2 := "test_cache:partial2"
	client.Del(ctx, idxKey, cacheKey1, cacheKey2)

	// 只写入一个缓存（另一个不存在）
	client.Set(ctx, cacheKey1, "value1", time.Minute)

	// 注册两个键到索引
	mgr.Register(ctx, idxKey, cacheKey1, cacheKey2)

	// 按索引删除（Lua脚本会尝试删除两个，但只有一个存在）
	deleted, err := mgr.DeleteByIndex(ctx, idxKey)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), deleted) // Lua DEL对不存在的键也返回计数

	// 确认索引集合已删除
	members, err := mgr.GetIndexMembers(ctx, idxKey)
	assert.NoError(t, err)
	assert.Empty(t, members)
}

// TestCacheIndexManager24_WrapperWithIndexError 测试 CacheWrapperWithIndex loader 返回错误
func TestCacheIndexManager24_WrapperWithIndexError(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:err"
	cacheKey := "test_cache:err"
	client.Del(ctx, idxKey, cacheKey)

	loadFunc := func(ctx context.Context) (string, error) {
		return "", assert.AnError
	}

	loader := CacheWrapperWithIndex(mgr, idxKey, client, cacheKey, loadFunc, time.Minute)

	result, err := loader(ctx)
	assert.Error(t, err)
	assert.Equal(t, "", result)
	assert.Equal(t, assert.AnError, err)

	// 即使 loader 出错，索引仍应注册（因为 Register 在 CacheWrapper 之前调用）
	members, err := mgr.GetIndexMembers(ctx, idxKey)
	assert.NoError(t, err)
	assert.Contains(t, members, cacheKey)

	client.Del(ctx, idxKey)
}

// TestCacheIndexManager25_DeleteByKeysSingle 测试 DeleteByKeys 单个键
func TestCacheIndexManager25_DeleteByKeysSingle(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	cacheKey := "test_cache:single_del"
	client.Del(ctx, cacheKey)
	client.Set(ctx, cacheKey, "value", time.Minute)

	err := mgr.DeleteByKeys(ctx, cacheKey)
	assert.NoError(t, err)

	_, err = client.Get(ctx, cacheKey).Result()
	assert.Error(t, err)
}

// TestCacheIndexManager26_NewCacheIndexManager 测试构造函数
func TestCacheIndexManager26_NewCacheIndexManager(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	mgr := NewCacheIndexManager(client)
	assert.NotNil(t, mgr)
	assert.Equal(t, client, mgr.client)

	// nil client
	mgrNil := NewCacheIndexManager(nil)
	assert.NotNil(t, mgrNil)
	assert.Nil(t, mgrNil.client)
}

// TestCacheIndexManager27_GetIndexMembersNonExistent 测试查询不存在的索引
func TestCacheIndexManager27_GetIndexMembersNonExistent(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	nonExistentKey := "test_idx:nonexistent"
	client.Del(ctx, nonExistentKey)

	members, err := mgr.GetIndexMembers(ctx, nonExistentKey)
	assert.NoError(t, err)
	assert.Empty(t, members)
}

// TestCacheIndexManager28_WrapperWithIndexMultipleKeysSameIndex 测试多个缓存键注册到同一索引后批量删除
func TestCacheIndexManager28_WrapperWithIndexMultipleKeysSameIndex(t *testing.T) {
	client := setupRedisClient(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	idxKey := "test_idx:multi_same"
	client.Del(ctx, idxKey)

	// 创建多个 loader 共享同一索引
	keys := []string{"test_cache:multi1", "test_cache:multi2", "test_cache:multi3"}
	for _, k := range keys {
		loader := CacheWrapperWithIndex(mgr, idxKey, client, k,
			func(ctx context.Context) (string, error) { return "data", nil }, time.Minute)
		_, err := loader(ctx)
		require.NoError(t, err)
	}

	// 验证索引中有3个成员
	members, err := mgr.GetIndexMembers(ctx, idxKey)
	assert.NoError(t, err)
	assert.Len(t, members, 3)

	// 按索引删除
	deleted, err := mgr.DeleteByIndex(ctx, idxKey)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), deleted)

	// 确认所有缓存已删除
	for _, k := range keys {
		_, err := client.Get(ctx, k).Result()
		assert.Error(t, err)
	}
}

// TestCacheIndexManager29_DeleteByIndexLuaError 测试 Lua 脚本执行错误
func TestCacheIndexManager29_DeleteByIndexLuaError(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DisableIdentity: true})
	defer client.Close()

	ctx := context.Background()
	mgr := NewCacheIndexManager(client)

	// 关闭 miniredis 模拟连接错误
	mr.Close()

	_, err := mgr.DeleteByIndex(ctx, "idx:lua_err")
	assert.Error(t, err)
}
