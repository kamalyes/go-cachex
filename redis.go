/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-06 22:01:16
 * @FilePath: \go-cachex\redis.go
 * @Description: Redis 的缓存适配器（RedisHandler）
 * 接口映射到 Redis 客户端调用主要职责：
 *  - 提供 Redis 版本的 Get/Set/SetWithTTL/GetTTL/Del/Close 方法实现，返回和接受 []byte
 *    以保持与抽象 `Handler` 的兼容性
 *  - 封装连接配置结构 `RedisConfig`，并提供从文件加载配置的辅助函数
 *  - 适合作为分布式或远程的 L2 缓存（或共享缓存）使用
 *
 * 使用注意：
 *  - 值以二进制形式写入 Redis，序列化/反序列化由调用方负责（这里直接保存 []byte）
 *  - 调用方应管理好配置与凭据（`RedisConfig`），并注意 Redis 客户端的生命周期
 *  - Redis 操作会使用内部 context（可通过 WithCtx 创建带自定义 ctx 的 handler）
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewRedisOptions 创建推荐的Redis配置
// 这个函数设置了一些推荐的默认值以避免常见的警告和问题
func NewRedisOptions(addr, password string, db int) *redis.Options {
	return &redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
		// 禁用身份设置以避免 MAINT_NOTIFICATIONS 等警告
		// 特别是在较老的Redis版本中
		DisableIdentity: true,
		// 使用RESP2协议以提高兼容性
		Protocol: 2,
		// 设置合理的超时值
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		// 设置连接池参数
		PoolSize:        10,
		MinIdleConns:    5,
		ConnMaxIdleTime: 30 * time.Minute,
	}
}

// RedisHandler 是 Redis 缓存的实现
type RedisHandler struct {
	redis     *redis.Client
	ctx       context.Context
	loadGroup sync.Map // map[string]*redisLoadCall for GetOrCompute coordination
}

// redisLoadCall represents an in-flight loader call for RedisHandler GetOrCompute
type redisLoadCall struct {
	wg  sync.WaitGroup
	val []byte
	err error
}

// NewRedisHandler 创建新的 RedisHandler
func NewRedisHandler(cfg *redis.Options) (Handler, error) {
	// 如果没有设置 DisableIdentity，建议设置为 true 以避免 MAINT_NOTIFICATIONS 警告
	if !cfg.DisableIdentity {
		// 注意：这里只是建议，不强制修改用户配置
		// 用户可以通过在配置中显式设置 DisableIdentity: false 来启用身份功能
	}

	redis := redis.NewClient(cfg)
	return &RedisHandler{redis: redis, ctx: context.Background()}, nil
}

// NewRedisHandlerSimple 创建新的 RedisHandler，使用推荐的配置
// 这是一个便捷函数，使用了优化的默认配置以避免常见问题
func NewRedisHandlerSimple(addr, password string, db int) (Handler, error) {
	cfg := NewRedisOptions(addr, password, db)
	return NewRedisHandler(cfg)
}

func (h *RedisHandler) WithCtx(ctx context.Context) *RedisHandler {
	return &RedisHandler{
		redis: h.redis,
		ctx:   ctx,
	}
}

// ========== 简化版方法（不带context） ==========

// Get 获取缓存值
func (h *RedisHandler) Get(k []byte) ([]byte, error) {
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return h.GetWithCtx(ctx, k)
}

// Set 设置缓存值
func (h *RedisHandler) Set(k, v []byte) error {
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return h.SetWithCtx(ctx, k, v)
}

// SetWithTTL 设置带TTL的缓存值
func (h *RedisHandler) SetWithTTL(k, v []byte, ttl time.Duration) error {
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return h.SetWithTTLAndCtx(ctx, k, v, ttl)
}

// GetTTL 获取键的剩余TTL
func (h *RedisHandler) GetTTL(k []byte) (time.Duration, error) {
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return h.GetTTLWithCtx(ctx, k)
}

// Del 删除缓存键
func (h *RedisHandler) Del(k []byte) error {
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return h.DelWithCtx(ctx, k)
}

// BatchGet 批量获取
func (h *RedisHandler) BatchGet(keys [][]byte) ([][]byte, []error) {
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return h.BatchGetWithCtx(ctx, keys)
}

// ========== 完整版方法（带context） ==========

// GetWithCtx 获取缓存值
func (h *RedisHandler) GetWithCtx(ctx context.Context, k []byte) ([]byte, error) {
	if err := ValidateBasicOp(k, h.redis != nil, false); err != nil {
		return nil, err
	}

	sCmd := h.redis.Get(ctx, string(k))
	if errors.Is(sCmd.Err(), redis.Nil) {
		return nil, ErrNotFound
	}
	if err := sCmd.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout
		}
		return nil, ErrUnavailable
	}
	return sCmd.Bytes()
}

// GetTTLWithCtx 获取键的剩余TTL
func (h *RedisHandler) GetTTLWithCtx(ctx context.Context, k []byte) (time.Duration, error) {
	if err := ValidateBasicOp(k, h.redis != nil, false); err != nil {
		return 0, err
	}

	dCmd := h.redis.TTL(ctx, string(k))
	if errors.Is(dCmd.Err(), redis.Nil) {
		return 0, ErrNotFound
	}
	if err := dCmd.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, ErrTimeout
		}
		return 0, ErrUnavailable
	}
	return dCmd.Val(), nil
}

// SetWithCtx 设置缓存值
func (h *RedisHandler) SetWithCtx(ctx context.Context, k, v []byte) error {
	return h.SetWithTTLAndCtx(ctx, k, v, redis.KeepTTL)
}

// SetWithTTLAndCtx 设置带TTL的缓存值
func (h *RedisHandler) SetWithTTLAndCtx(ctx context.Context, k, v []byte, ttl time.Duration) error {
	if err := ValidateWriteWithTTLOp(k, v, ttl, h.redis != nil, false); err != nil {
		return err
	}

	sCmd := h.redis.Set(ctx, string(k), v, ttl)
	if err := sCmd.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return ErrTimeout
		}
		return ErrUnavailable
	}
	return nil
}

// DelWithCtx 删除缓存键
func (h *RedisHandler) DelWithCtx(ctx context.Context, k []byte) error {
	if err := ValidateBasicOp(k, h.redis != nil, false); err != nil {
		return err
	}

	sCmd := h.redis.Del(ctx, string(k))
	if err := sCmd.Err(); err != nil {
		if err != redis.Nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return ErrTimeout
			}
			return ErrUnavailable
		}
	}
	return nil
}

// BatchGetWithCtx 批量获取多个键的值
func (h *RedisHandler) BatchGetWithCtx(ctx context.Context, keys [][]byte) ([][]byte, []error) {
	if len(keys) == 0 {
		return nil, nil
	}

	results := make([][]byte, len(keys))
	errors := make([]error, len(keys))

	// 转换为字符串键
	strKeys := make([]string, len(keys))
	for i, key := range keys {
		if len(key) == 0 {
			errors[i] = ErrInvalidKey
			continue
		}
		strKeys[i] = string(key)
	}

	// 使用Redis MGET命令批量获取
	result := h.redis.MGet(ctx, strKeys...)
	vals, err := result.Result()

	if err != nil {
		// 如果整个批量操作失败，所有键都返回错误
		for i := range errors {
			if errors[i] == nil { // 只覆盖非invalid key的错误
				errors[i] = err
			}
		}
		return results, errors
	}

	// 处理结果
	for i, val := range vals {
		if errors[i] != nil {
			continue // 跳过invalid key
		}

		if val == nil {
			errors[i] = ErrNotFound
		} else {
			if strVal, ok := val.(string); ok {
				results[i] = []byte(strVal)
			} else {
				errors[i] = ErrDataRead
			}
		}
	}

	return results, errors
}

// Stats 返回Redis缓存统计信息
func (h *RedisHandler) Stats() map[string]interface{} {
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	stats := make(map[string]interface{})
	stats["cache_type"] = "redis"

	// 获取Redis信息
	infoResult := h.redis.Info(ctx, "stats", "memory", "keyspace")
	if info, err := infoResult.Result(); err == nil {
		stats["redis_info"] = info
	} else {
		stats["redis_info_error"] = err.Error()
	}

	// 获取数据库大小
	dbSizeResult := h.redis.DBSize(ctx)
	if dbSize, err := dbSizeResult.Result(); err == nil {
		stats["db_size"] = dbSize
	} else {
		stats["db_size_error"] = err.Error()
	}

	// 获取内存使用情况
	memResult := h.redis.MemoryUsage(ctx, "nonexistent") // 这个命令在Redis 4.0+可用
	if mem, err := memResult.Result(); err == nil {
		stats["memory_usage"] = mem
	}

	return stats
}

// GetOrCompute 获取缓存值，如果不存在则计算并设置（简化版，不带context）
func (h *RedisHandler) GetOrCompute(key []byte, ttl time.Duration, loader func() ([]byte, error)) ([]byte, error) {
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	// 包装loader以适配带ctx的版本
	ctxLoader := func(context.Context) ([]byte, error) {
		return loader()
	}
	return h.GetOrComputeWithCtx(ctx, key, ttl, ctxLoader)
}

// GetOrComputeWithCtx 获取缓存值，如果不存在则计算并设置
func (h *RedisHandler) GetOrComputeWithCtx(ctx context.Context, key []byte, ttl time.Duration, loader func(context.Context) ([]byte, error)) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrInvalidKey
	}

	strKey := string(key)

	// 首先尝试从缓存获取
	if value, err := h.GetWithCtx(ctx, key); err == nil {
		result := make([]byte, len(value))
		copy(result, value)
		return result, nil
	}

	// Singleflight: 检查是否有正在进行的加载
	if v, loaded := h.loadGroup.Load(strKey); loaded {
		call := v.(*redisLoadCall)
		call.wg.Wait()
		if call.err != nil {
			return nil, call.err
		}
		result := make([]byte, len(call.val))
		copy(result, call.val)
		return result, nil
	}

	// 创建新的加载调用
	call := &redisLoadCall{}
	call.wg.Add(1)

	actual, loaded := h.loadGroup.LoadOrStore(strKey, call)
	if loaded {
		// 其他goroutine已经在加载了，等待结果
		actualCall := actual.(*redisLoadCall)
		actualCall.wg.Wait()
		if actualCall.err != nil {
			return nil, actualCall.err
		}
		result := make([]byte, len(actualCall.val))
		copy(result, actualCall.val)
		return result, nil
	}

	// 当前goroutine赢得了加载权，延迟清理和通知
	defer func() {
		call.wg.Done()
		time.AfterFunc(10*time.Millisecond, func() {
			h.loadGroup.Delete(strKey)
		})
	}()

	// 双重检查：可能在等待LoadOrStore期间已经被缓存
	if value, err := h.GetWithCtx(ctx, key); err == nil {
		call.val = value
		result := make([]byte, len(value))
		copy(result, value)
		return result, nil
	}

	// 使用Redis分布式锁防止跨实例的重复计算
	lockKey := strKey + ":lock"
	lockValue := fmt.Sprintf("%d", time.Now().UnixNano())
	lockTTL := 30 * time.Second

	// 尝试获取分布式锁
	acquired := h.redis.SetNX(ctx, lockKey, lockValue, lockTTL).Val()

	if acquired {
		// 成功获取锁，执行计算
		defer func() {
			// 使用Lua脚本安全释放锁
			script := `
				if redis.call("get", KEYS[1]) == ARGV[1] then
					return redis.call("del", KEYS[1])
				else
					return 0
				end
			`
			h.redis.Eval(ctx, script, []string{lockKey}, lockValue)
		}()

		// 三重检查缓存（可能在等待锁期间已被其他实例设置）
		if value, err := h.GetWithCtx(ctx, key); err == nil {
			call.val = value
			result := make([]byte, len(value))
			copy(result, value)
			return result, nil
		}

		// 执行实际的计算
		value, err := loader(ctx)
		if err != nil {
			call.err = err
			return nil, err
		}

		// 将结果写入缓存
		if ttl <= 0 {
			h.SetWithCtx(ctx, key, value)
		} else {
			h.SetWithTTLAndCtx(ctx, key, value, ttl)
		}

		// 保存结果
		call.val = value

		// 返回值的拷贝
		result := make([]byte, len(value))
		copy(result, value)
		return result, nil

	} else {
		// 未能获取锁，说明其他实例正在计算
		// 使用指数退避等待并重试获取缓存值
		maxRetries := 10
		baseDelay := 10 * time.Millisecond

		for i := 0; i < maxRetries; i++ {
			// 等待一段时间
			delay := time.Duration(1<<uint(i)) * baseDelay
			if delay > time.Second {
				delay = time.Second
			}

			select {
			case <-ctx.Done():
				call.err = ctx.Err()
				return nil, ctx.Err()
			case <-time.After(delay):
			}

			// 重试获取缓存值
			if value, err := h.GetWithCtx(ctx, key); err == nil {
				call.val = value
				result := make([]byte, len(value))
				copy(result, value)
				return result, nil
			}

			// 检查锁是否还存在
			if h.redis.Exists(ctx, lockKey).Val() == 0 {
				// 锁已释放，跳出等待循环，重新尝试获取锁
				break
			}
		}

		// 等待超时或锁释放后，尝试获取锁并计算
		acquired := h.redis.SetNX(ctx, lockKey, lockValue, lockTTL).Val()
		if acquired {
			defer func() {
				script := `
					if redis.call("get", KEYS[1]) == ARGV[1] then
						return redis.call("del", KEYS[1])
					else
						return 0
					end
				`
				h.redis.Eval(ctx, script, []string{lockKey}, lockValue)
			}()

			// 再次检查缓存
			if value, err := h.GetWithCtx(ctx, key); err == nil {
				call.val = value
				result := make([]byte, len(value))
				copy(result, value)
				return result, nil
			}

			// 执行计算
			value, err := loader(ctx)
			if err != nil {
				call.err = err
				return nil, err
			}

			// 写入缓存
			if ttl <= 0 {
				h.SetWithCtx(ctx, key, value)
			} else {
				h.SetWithTTLAndCtx(ctx, key, value, ttl)
			}

			call.val = value
			result := make([]byte, len(value))
			copy(result, value)
			return result, nil
		}

		// 最后尝试从缓存获取
		if value, err := h.GetWithCtx(ctx, key); err == nil {
			call.val = value
			result := make([]byte, len(value))
			copy(result, value)
			return result, nil
		}

		// 所有重试都失败，返回错误
		call.err = errors.New("failed to compute value after max retries")
		return nil, call.err
	}
}

// Close 实现 Handler 接口的 Close 方法
func (h *RedisHandler) Close() error {
	return h.redis.Close()
}
