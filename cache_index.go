/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-05-20 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastLastEditTime: 2026-05-20 00:00:00
 * @FilePath: \go-cachex\cache_index.go
 * @Description: 缓存索引管理器 - 基于索引集合实现缓存键的注册、查询与原子删除
 *
 * 核心思路：
 *   将缓存键注册到 Redis Set（索引集合）中，删除时通过 Lua 脚本原子地
 *   获取集合内所有成员并批量删除，最后删除索引集合本身。
 *
 * 设计原则：
 *   - 傻瓜式操作：CacheWrapperWithIndex 一行代码同时完成缓存+索引注册
 *   - 分组管理：NewIndexGroup 预定义业务分组，删除时一行调用
 *   - 原子删除：Lua 脚本保证索引+缓存键的原子清理
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// luaDeleteByIndexSet Lua脚本：从索引集合中获取所有缓存键，批量删除后删除索引集合本身
// KEYS[1..N] = 索引集合名
// 返回：删除的缓存键数量
var luaDeleteByIndexSet = `
local deleted = 0
for i = 1, #KEYS do
    local members = redis.call('SMEMBERS', KEYS[i])
    for _, key in ipairs(members) do
        redis.call('DEL', key)
        deleted = deleted + 1
    end
    redis.call('DEL', KEYS[i])
end
return deleted
`

// CacheIndexManager 缓存索引管理器
// 基于索引集合（Redis Set）追踪缓存键，支持按分组原子删除
//
// 使用方式：
//
//	mgr := cachex.NewCacheIndexManager(redisClient)
//
//	// 写缓存时自动注册索引
//	loader := cachex.CacheWrapperWithIndex(mgr, "idx:tenant", redisClient, "cache:key", loadFunc, time.Minute)
//	result, err := loader(ctx)  // 缓存+索引注册一步完成
//
//	// 删缓存时按分组原子删除
//	mgr.DeleteByIndex(ctx, "idx:tenant")  // 删除该索引下所有缓存键
type CacheIndexManager struct {
	client *redis.Client
}

// NewCacheIndexManager 创建缓存索引管理器
func NewCacheIndexManager(client *redis.Client) *CacheIndexManager {
	return &CacheIndexManager{client: client}
}

// Register 将缓存键注册到指定的索引集合中
// 写入缓存时调用，将实际缓存键记录到对应分类的索引集合
func (m *CacheIndexManager) Register(ctx context.Context, idxKey string, cacheKeys ...string) {
	if m.client == nil || idxKey == "" || len(cacheKeys) == 0 {
		return
	}
	members := make([]interface{}, len(cacheKeys))
	for i, k := range cacheKeys {
		members[i] = k
	}
	m.client.SAdd(ctx, idxKey, members...)
}

// DeleteByIndex 根据索引集合原子删除所有关联缓存键
// 使用Lua脚本保证原子性，避免SCAN操作
// 返回删除的缓存键数量
func (m *CacheIndexManager) DeleteByIndex(ctx context.Context, idxKeys ...string) (int64, error) {
	if m.client == nil || len(idxKeys) == 0 {
		return 0, nil
	}
	result, err := m.client.Eval(ctx, luaDeleteByIndexSet, idxKeys).Int64()
	if err != nil {
		return 0, err
	}
	return result, nil
}

// DeleteByKeys 按缓存键直接删除缓存（用于已知确切键名的场景）
func (m *CacheIndexManager) DeleteByKeys(ctx context.Context, keys ...string) error {
	if m.client == nil || len(keys) == 0 {
		return nil
	}
	return m.client.Del(ctx, keys...).Err()
}

// GetIndexMembers 获取索引集合中的所有缓存键
func (m *CacheIndexManager) GetIndexMembers(ctx context.Context, idxKey string) ([]string, error) {
	if m.client == nil || idxKey == "" {
		return nil, nil
	}
	return m.client.SMembers(ctx, idxKey).Result()
}

// IndexGroup 索引分组，用于按业务维度批量管理多个索引集合
// 预定义分组后，一行调用即可删除该分组下所有缓存
//
// 使用方式：
//
//	tenantGroup := mgr.NewIndexGroup("idx:tenant_options", "idx:region_options", "idx:platform_options")
//	tenantGroup.Delete(ctx)  // 一行删除所有关联缓存
type IndexGroup struct {
	manager *CacheIndexManager
	idxKeys []string
}

// NewIndexGroup 创建索引分组
func (m *CacheIndexManager) NewIndexGroup(idxKeys ...string) *IndexGroup {
	return &IndexGroup{
		manager: m,
		idxKeys: idxKeys,
	}
}

// Delete 删除分组内所有索引集合关联的缓存键
func (g *IndexGroup) Delete(ctx context.Context) (int64, error) {
	return g.manager.DeleteByIndex(ctx, g.idxKeys...)
}

// IndexKeys 返回分组内的索引键列表
func (g *IndexGroup) IndexKeys() []string {
	return g.idxKeys
}

// AddIndexKeys 向分组中追加索引键
func (g *IndexGroup) AddIndexKeys(keys ...string) {
	g.idxKeys = append(g.idxKeys, keys...)
}

// CacheWrapperWithIndex 带自动索引注册的 CacheWrapper
// 在执行 CacheWrapper 的同时自动将缓存键注册到指定索引集合
// 这是推荐的缓存+索引一体化入口，用户无需手动调用 Register
//
// 使用方式：
//
//	loader := cachex.CacheWrapperWithIndex(mgr, "idx:tenant_options", redisClient, cacheKey, loadFunc, ttl, opts...)
//	result, err := loader(ctx)  // 缓存读取+索引注册一步完成
func CacheWrapperWithIndex[T any](
	manager *CacheIndexManager,
	idxKey string,
	client *redis.Client,
	key string,
	cacheFunc CacheFunc[T],
	expiration time.Duration,
	opts ...CacheOption,
) CacheFunc[T] {
	wrappedFunc := CacheWrapper(client, key, cacheFunc, expiration, opts...)

	return func(ctx context.Context) (T, error) {
		if manager != nil && idxKey != "" {
			manager.Register(ctx, idxKey, key)
		}
		return wrappedFunc(ctx)
	}
}
