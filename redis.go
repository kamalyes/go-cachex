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
	"os"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/redis/go-redis/v9"
)

// RedisConfig 是 Redis 的配置结构
type RedisConfig struct {
	Host     string
	Port     int
	DB       int
	Username []byte
	Password []byte
}

// LoadRedisConfigFromFile 从文件加载 Redis 配置
func LoadRedisConfigFromFile(configPath string) (*RedisConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	c := &RedisConfig{}
	if err := jsoniter.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return c, nil
}

// RedisHandler 是 Redis 缓存的实现
type RedisHandler struct {
	redis *redis.Client
	ctx context.Context
}

// NewRedisHandler 创建新的 RedisHandler
func NewRedisHandler(cfg *RedisConfig) (Handler, error) {
	redis := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Username: string(cfg.Username),
		Password: string(cfg.Password),
		DB:       cfg.DB,
	})

	return &RedisHandler{redis: redis, ctx: context.Background()}, nil
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

// Close 实现 Handler 接口的 Close 方法
func (h *RedisHandler) Close() error {
	return h.redis.Close()
}
