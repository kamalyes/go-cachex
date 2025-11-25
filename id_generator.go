/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-26 22:55:27
 * @FilePath: \go-cachex\id_generator.go
 * @Description: 通用ID生成器 - 基于Redis实现分布式序列号
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */

package cachex

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"time"
)

// IDGenerator 通用ID生成器
type IDGenerator struct {
	redisClient    redis.UniversalClient // 支持单机和集群
	keyPrefix      string                // Redis key前缀
	timeFormat     string                // 时间格式
	seqLength      int                   // 序列号长度
	expire         time.Duration         // 序列号过期时间
	seqStart       int64                 // 序列号起始值
	idPrefix       string                // ID前缀
	idSuffix       string                // ID后缀
	separator      string                // 分隔符
	OnSequenceInit func() int64          // key不存在时动态获取起始值
}

// Option 是配置IDGenerator的函数类型 - 已废弃，改为链式调用
// type Option func(*IDGenerator)

// NewIDGenerator 创建ID生成器实例
func NewIDGenerator(redisClient redis.UniversalClient) *IDGenerator {
	return &IDGenerator{
		redisClient: redisClient,
		keyPrefix:   "default:key:prefix:",
		timeFormat:  "2006010215",
		seqLength:   6,
		expire:      2 * time.Hour,
		seqStart:    1,
		idPrefix:    "",
		idSuffix:    "",
		separator:   "",
	}
}

// WithKeyPrefix 设置Redis键前缀
func (g *IDGenerator) WithKeyPrefix(prefix string) *IDGenerator {
	g.keyPrefix = prefix
	return g
}

// WithTimeFormat 设置时间格式
func (g *IDGenerator) WithTimeFormat(format string) *IDGenerator {
	g.timeFormat = format
	return g
}

// WithSequenceLength 设置序列号长度
func (g *IDGenerator) WithSequenceLength(length int) *IDGenerator {
	g.seqLength = length
	return g
}

// WithExpire 设置序列号过期时间
func (g *IDGenerator) WithExpire(d time.Duration) *IDGenerator {
	g.expire = d
	return g
}

// WithSequenceStart 设置序列号起始值
func (g *IDGenerator) WithSequenceStart(start int64) *IDGenerator {
	g.seqStart = start
	return g
}

// WithIDPrefix 设置ID前缀
func (g *IDGenerator) WithIDPrefix(prefix string) *IDGenerator {
	g.idPrefix = prefix
	return g
}

// WithIDSuffix 设置ID后缀
func (g *IDGenerator) WithIDSuffix(suffix string) *IDGenerator {
	g.idSuffix = suffix
	return g
}

// WithSeparator 设置分隔符
func (g *IDGenerator) WithSeparator(sep string) *IDGenerator {
	g.separator = sep
	return g
}

// WithSequenceInitCallback 设置key不存在时的序列号初始化回调
func (g *IDGenerator) WithSequenceInitCallback(cb func() int64) *IDGenerator {
	g.OnSequenceInit = cb
	return g
}

// getTimePrefix 获取当前时间前缀
func (g *IDGenerator) getTimePrefix() string {
	return time.Now().Format(g.timeFormat)
}

// getRedisKey 生成Redis键
func (g *IDGenerator) getRedisKey(timePrefix string) string {
	return fmt.Sprintf("%s%s", g.keyPrefix, timePrefix)
}

// GenerateID 生成一个ID
func (g *IDGenerator) GenerateID(ctx context.Context) (string, error) {
	if g.redisClient == nil {
		return "", fmt.Errorf("redis client is not available")
	}
	timePrefix := g.getTimePrefix()
	redisKey := g.getRedisKey(timePrefix)

	// 使用 Lua 脚本保证原子性：检查 key 是否存在，不存在则初始化，然后 INCR
	luaScript := `
		local key = KEYS[1]
		local expire = tonumber(ARGV[1])
		local seqStart = tonumber(ARGV[2])
		local exists = redis.call('EXISTS', key)
		if exists == 0 and seqStart > 1 then
			redis.call('SET', key, seqStart-1, 'EX', expire)
		end
		local seq = redis.call('INCR', key)
		if exists == 0 and seqStart <= 1 then
			redis.call('EXPIRE', key, expire)
		end
		return seq
	`

	start := g.seqStart
	if g.OnSequenceInit != nil {
		start = g.OnSequenceInit()
	}
	expireSec := int64(g.expire.Seconds())
	args := []interface{}{expireSec, start}

	res, err := g.redisClient.Eval(ctx, luaScript, []string{redisKey}, args...).Result()
	if err != nil {
		return "", fmt.Errorf("failed to generate sequence: %w", err)
	}

	seq, ok := res.(int64)
	if !ok {
		return "", fmt.Errorf("unexpected sequence type: %T", res)
	}

	id := g.formatID(timePrefix, seq)
	return id, nil
}

// formatID 格式化ID
func (g *IDGenerator) formatID(timePrefix string, seq int64) string {
	seqStr := fmt.Sprintf("%0*d", g.seqLength, seq)
	var id string
	if g.separator != "" {
		id = fmt.Sprintf("%s%s%s", timePrefix, g.separator, seqStr)
	} else {
		id = timePrefix + seqStr
	}
	if g.idPrefix != "" {
		id = g.idPrefix + id
	}
	if g.idSuffix != "" {
		id = id + g.idSuffix
	}
	return id
}

// GenerateIDs 批量生成ID（Lua脚本实现，原子高效）
func (g *IDGenerator) GenerateIDs(ctx context.Context, n int) ([]string, error) {
	if g.redisClient == nil {
		return nil, fmt.Errorf("redis client is not available")
	}
	if n <= 0 {
		return nil, fmt.Errorf("n must be positive")
	}
	timePrefix := g.getTimePrefix()
	redisKey := g.getRedisKey(timePrefix)
	// Lua脚本：批量INCRBY并返回所有ID
	luaScript := `
		local key = KEYS[1]
		local expire = tonumber(ARGV[1])
		local n = tonumber(ARGV[2])
		local seqStart = tonumber(ARGV[3])
		local exists = redis.call('EXISTS', key)
		if exists == 0 and seqStart > 1 then
			redis.call('SET', key, seqStart-1, 'EX', expire)
		end
		local seq = redis.call('INCRBY', key, n)
		local ids = {}
		for i=seq-n+1,seq do
			table.insert(ids, i)
		end
		return ids
	`
	start := g.seqStart
	if g.OnSequenceInit != nil {
		start = g.OnSequenceInit()
	}
	expireSec := int64(g.expire.Seconds())
	args := []interface{}{expireSec, n, start}
	res, err := g.redisClient.Eval(ctx, luaScript, []string{redisKey}, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to batch generate sequence: %w", err)
	}
	seqs, ok := res.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected lua result type")
	}
	ids := make([]string, 0, n)
	for _, s := range seqs {
		seqInt, ok := s.(int64)
		if !ok {
			// redis返回可能是float64
			switch v := s.(type) {
			case float64:
				seqInt = int64(v)
			case string:
				// 兼容string类型
				var tmp int64
				_, err := fmt.Sscanf(v, "%d", &tmp)
				if err == nil {
					seqInt = tmp
				} else {
					return nil, fmt.Errorf("invalid sequence value: %v", v)
				}
			default:
				return nil, fmt.Errorf("invalid sequence type: %T", s)
			}
		}
		ids = append(ids, g.formatID(timePrefix, seqInt))
	}
	return ids, nil
}

// ResetSequence 仅在 key 不存在时初始化序列号，已存在则不做任何操作
// 这样可以避免在分布式环境下误删已有序列号导致ID重复
func (g *IDGenerator) ResetSequence(ctx context.Context, timePrefix string) error {
	if g.redisClient == nil {
		return fmt.Errorf("redis client is not available")
	}
	redisKey := g.getRedisKey(timePrefix)
	exists, err := g.redisClient.Exists(ctx, redisKey).Result()
	if err != nil {
		return fmt.Errorf("failed to check key existence: %w", err)
	}
	if exists == 0 {
		// key 不存在时才初始化
		start := g.seqStart
		if g.OnSequenceInit != nil {
			start = g.OnSequenceInit()
		}
		if start > 1 {
			// 设置为 start-1，这样第一次 INCR 会得到 start
			return g.redisClient.Set(ctx, redisKey, start-1, g.expire).Err()
		}
		// start <= 1 时不需要设置，让 INCR 从 0 开始
	}
	// key 已存在则不做任何操作，保护已有序列号
	return nil
}

// ForceResetSequence 强制重置指定时间的序列号(仅用于测试或特殊场景)
// 警告：此方法会删除已有序列号，可能导致ID重复，生产环境慎用
func (g *IDGenerator) ForceResetSequence(ctx context.Context, timePrefix string) error {
	if g.redisClient == nil {
		return fmt.Errorf("redis client is not available")
	}
	return g.redisClient.Del(ctx, g.getRedisKey(timePrefix)).Err()
}

// GetCurrentSequence 获取当前时间的序列号（不增加）
func (g *IDGenerator) GetCurrentSequence(ctx context.Context) (int64, error) {
	if g.redisClient == nil {
		return 0, fmt.Errorf("redis client is not available")
	}
	return g.GetSequenceByTime(ctx, g.getTimePrefix())
}

// GetSequenceByTime 获取指定时间段的序列号（不增加）
func (g *IDGenerator) GetSequenceByTime(ctx context.Context, timePrefix string) (int64, error) {
	if g.redisClient == nil {
		return 0, fmt.Errorf("redis client is not available")
	}
	redisKey := g.getRedisKey(timePrefix)
	seq, err := g.redisClient.Get(ctx, redisKey).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get sequence from redis: %w", err)
	}
	return seq, nil
}

// Getter方法，便于测试
func (g *IDGenerator) GetIDPrefix() string  { return g.idPrefix }
func (g *IDGenerator) GetIDSuffix() string  { return g.idSuffix }
func (g *IDGenerator) GetSeparator() string { return g.separator }
