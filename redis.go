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
	redis *redis.Client
	ctx context.Context
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
		ctx: ctx,
	}
}

// Get 实现 Handler 接口的 Get 方法
func (h *RedisHandler) Get(k []byte) ([]byte, error) {
    if err := ValidateBasicOp(k, h.redis != nil, false); err != nil {
        return nil, err
    }

    sCmd := h.redis.Get(h.ctx, string(k))
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

// GetTTL 实现 Handler 接口的 GetTTL 方法
func (h *RedisHandler) GetTTL(k []byte) (time.Duration, error) {
    if err := ValidateBasicOp(k, h.redis != nil, false); err != nil {
        return 0, err
    }

    dCmd := h.redis.TTL(h.ctx, string(k))
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

// Set 实现 Handler 接口的 Set 方法
func (h *RedisHandler) Set(k, v []byte) error {
    return h.SetWithTTL(k, v, redis.KeepTTL)
}

// SetWithTTL 实现 Handler 接口的 SetWithTTL 方法
func (h *RedisHandler) SetWithTTL(k, v []byte, ttl time.Duration) error {
    if err := ValidateWriteWithTTLOp(k, v, ttl, h.redis != nil, false); err != nil {
        return err
    }

    sCmd := h.redis.Set(h.ctx, string(k), v, ttl)
    if err := sCmd.Err(); err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            return ErrTimeout
        }
        return ErrUnavailable
    }
    return nil
}

// Del 实现 Handler 接口的 Del 方法
func (h *RedisHandler) Del(k []byte) error {
    if err := ValidateBasicOp(k, h.redis != nil, false); err != nil {
        return err
    }

    sCmd := h.redis.Del(h.ctx, string(k))
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

// BatchGet 批量获取多个键的值
func (h *RedisHandler) BatchGet(keys [][]byte) ([][]byte, []error) {
	if len(keys) == 0 {
		return nil, nil
	}

	results := make([][]byte, len(keys))
	errors := make([]error, len(keys))

	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}

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

// GetOrCompute 获取缓存值，如果不存在则计算并设置
func (h *RedisHandler) GetOrCompute(key []byte, ttl time.Duration, loader func() ([]byte, error)) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrInvalidKey
	}

	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	strKey := string(key)

	// 首先尝试获取
	if value, err := h.Get(key); err == nil {
		return value, nil
	}

	// 使用Redis分布式锁防止重复计算
	lockKey := strKey + ":lock"
	lockValue := fmt.Sprintf("%d", time.Now().UnixNano()) // 使用时间戳作为锁值
	lockTTL := 30 * time.Second // 锁的超时时间
	
	// 尝试获取分布式锁
	acquired := h.redis.SetNX(ctx, lockKey, lockValue, lockTTL).Val()
	
	if acquired {
		// 成功获取锁，执行计算
		defer func() {
			// 使用Lua脚本安全释放锁（只有持有锁的实例才能释放）
			script := `
				if redis.call("get", KEYS[1]) == ARGV[1] then
					return redis.call("del", KEYS[1])
				else
					return 0
				end
			`
			h.redis.Eval(ctx, script, []string{lockKey}, lockValue)
		}()
		
		// 再次检查缓存（可能在等待锁的过程中其他实例已经计算并设置了）
		if value, err := h.Get(key); err == nil {
			return value, nil
		}
		
		// 执行实际的计算
		value, err := loader()
		if err != nil {
			return nil, err
		}
		
		// 将结果写入缓存
		if ttl <= 0 {
			h.Set(key, value)
		} else {
			h.SetWithTTL(key, value, ttl)
		}
		
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
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			
			// 重试获取缓存值
			if value, err := h.Get(key); err == nil {
				return value, nil
			}
			
			// 检查锁是否还存在
			if h.redis.Exists(ctx, lockKey).Val() == 0 {
				// 锁已释放，跳出等待循环，重新尝试获取锁
				break
			}
		}
		
		// 如果等待超时，降级为不加锁的直接计算
		// 这种情况下可能有重复计算，但确保不会无限等待
		value, err := loader()
		if err != nil {
			return nil, err
		}
		
		// 尝试写入缓存（可能会被其他实例覆盖，但没关系）
		if ttl <= 0 {
			h.Set(key, value)
		} else {
			h.SetWithTTL(key, value, ttl)
		}
		
		result := make([]byte, len(value))
		copy(result, value)
		return result, nil
	}
}

// Close 实现 Handler 接口的 Close 方法
func (h *RedisHandler) Close() error {
	return h.redis.Close()
}
