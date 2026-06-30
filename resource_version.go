/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-06-30 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-06-30 11:03:26
 * @FilePath: \go-cachex\resource_version.go
 * @Description: 资源版本追踪器，用于分布式环境下的资源变更检测和缓存失效
 *              支持通过 Redis 共享版本号，实现多节点间的配置/资源热更新感知
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// VersionTracker 资源版本追踪器
// 用于追踪任意资源的版本号，支持分布式环境下的版本变更检测
// 使用场景：配置热更新、连接池重建、缓存失效等
type VersionTracker struct {
	redisClient redis.UniversalClient
	prefix      string
	mu          sync.RWMutex
}

// NewVersionTracker 创建版本追踪器
// prefix 为版本键的前缀，建议使用业务模块名，如 "product:"、"payment:"
func NewVersionTracker(redisClient redis.UniversalClient, prefix string) *VersionTracker {
	if prefix == "" {
		prefix = "cachex:version:"
	}
	return &VersionTracker{
		redisClient: redisClient,
		prefix:      prefix,
	}
}

// GetVersionKey 获取资源版本的完整 Redis key
func (v *VersionTracker) GetVersionKey(resourceID string) string {
	return fmt.Sprintf("%s%s", v.prefix, resourceID)
}

// UpdateVersion 更新资源的版本号
// version 通常使用时间戳或递增序号
func (v *VersionTracker) UpdateVersion(ctx context.Context, resourceID string, version int64) error {
	if v.redisClient == nil {
		return nil
	}
	key := v.GetVersionKey(resourceID)
	return v.redisClient.Set(ctx, key, version, 0).Err()
}

// UpdateVersionWithNow 使用当前时间戳更新版本号
func (v *VersionTracker) UpdateVersionWithNow(ctx context.Context, resourceID string) error {
	return v.UpdateVersion(ctx, resourceID, nowTimestamp())
}

// GetVersion 获取资源的当前版本号（从 Redis）
func (v *VersionTracker) GetVersion(ctx context.Context, resourceID string) (int64, error) {
	if v.redisClient == nil {
		return 0, ErrUnavailable
	}
	key := v.GetVersionKey(resourceID)
	val, err := v.redisClient.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(val, 10, 64)
}

// HasChanged 检查资源版本是否发生变化
// lastVersion 为上次记录的版本号，返回 (是否变化, 最新版本号)
func (v *VersionTracker) HasChanged(ctx context.Context, resourceID string, lastVersion int64) (bool, int64) {
	if v.redisClient == nil {
		return true, 0
	}

	currentVersion, err := v.GetVersion(ctx, resourceID)
	if err != nil {
		if err == ErrNotFound {
			return true, 0
		}
		return true, currentVersion
	}

	if currentVersion != lastVersion {
		return true, currentVersion
	}
	return false, currentVersion
}

// BatchUpdateVersion 批量更新多个资源的版本号
func (v *VersionTracker) BatchUpdateVersion(ctx context.Context, resourceIDs []string, version int64) error {
	if v.redisClient == nil {
		return nil
	}
	pipe := v.redisClient.Pipeline()
	for _, resourceID := range resourceIDs {
		key := v.GetVersionKey(resourceID)
		pipe.Set(ctx, key, version, 0)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// BatchUpdateVersionWithNow 批量使用当前时间戳更新版本号
func (v *VersionTracker) BatchUpdateVersionWithNow(ctx context.Context, resourceIDs []string) error {
	return v.BatchUpdateVersion(ctx, resourceIDs, nowTimestamp())
}

// DeleteVersion 删除资源版本记录
func (v *VersionTracker) DeleteVersion(ctx context.Context, resourceID string) error {
	if v.redisClient == nil {
		return nil
	}
	key := v.GetVersionKey(resourceID)
	return v.redisClient.Del(ctx, key).Err()
}

// DeleteVersions 批量删除资源版本记录
func (v *VersionTracker) DeleteVersions(ctx context.Context, resourceIDs []string) error {
	if v.redisClient == nil {
		return nil
	}
	keys := make([]string, len(resourceIDs))
	for i, resourceID := range resourceIDs {
		keys[i] = v.GetVersionKey(resourceID)
	}
	return v.redisClient.Del(ctx, keys...).Err()
}

// GetOrCreateVersion 获取或创建版本号（如果不存在则设置为当前时间戳）
func (v *VersionTracker) GetOrCreateVersion(ctx context.Context, resourceID string) (int64, error) {
	if v.redisClient == nil {
		return nowTimestamp(), nil
	}

	currentVersion, err := v.GetVersion(ctx, resourceID)
	if err == ErrNotFound {
		newVersion := nowTimestamp()
		if setErr := v.UpdateVersion(ctx, resourceID, newVersion); setErr != nil {
			return 0, setErr
		}
		return newVersion, nil
	}
	if err != nil {
		return 0, err
	}
	return currentVersion, nil
}

// Touch 触碰资源版本号（使其递增）
// 适用于配置变更后需要触发缓存失效的场景
func (v *VersionTracker) Touch(ctx context.Context, resourceID string) error {
	return v.UpdateVersionWithNow(ctx, resourceID)
}

// TouchAll 触碰多个资源版本号
func (v *VersionTracker) TouchAll(ctx context.Context, resourceIDs []string) error {
	return v.BatchUpdateVersionWithNow(ctx, resourceIDs)
}

// nowTimestamp 获取当前纳秒时间戳作为版本号
func nowTimestamp() int64 {
	return time.Now().UnixNano()
}
