/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-25 09:55:02
 * @FilePath: \go-cachex\id_generator.go
 * @Description: 通用ID生成器 - 基于Redis实现分布式序列号
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */

package cachex

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// IDGenerator 通用ID生成器
type IDGenerator struct {
	redisClient redis.UniversalClient // 支持单机和集群
	keyPrefix   string // Redis key前缀
	timeFormat  string // 时间格式
	seqLength   int    // 序列号长度
	expire      time.Duration // 序列号过期时间
	seqStart    int64  // 序列号起始值
	idPrefix    string // ID前缀
	idSuffix    string // ID后缀
	separator   string // 分隔符
	OnSequenceInit func() int64 // key不存在时动态获取起始值
}

// Option 是配置IDGenerator的函数类型
type Option func(*IDGenerator)

// NewIDGenerator 创建ID生成器实例
func NewIDGenerator(redisClient redis.UniversalClient, options ...Option) *IDGenerator {
	gen := &IDGenerator{
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
	for _, option := range options {
		option(gen)
	}
	return gen
}

// WithKeyPrefix 设置Redis键前缀
func WithKeyPrefix(prefix string) Option {
	return func(g *IDGenerator) {
		g.keyPrefix = prefix
	}
}

// WithTimeFormat 设置时间格式
func WithTimeFormat(format string) Option {
	return func(g *IDGenerator) {
		g.timeFormat = format
	}
}

// WithSequenceLength 设置序列号长度
func WithSequenceLength(length int) Option {
	return func(g *IDGenerator) {
		g.seqLength = length
	}
}

// WithExpire 设置序列号过期时间
func WithExpire(d time.Duration) Option {
	return func(g *IDGenerator) {
		g.expire = d
	}
}

// WithSequenceStart 设置序列号起始值
func WithSequenceStart(start int64) Option {
	return func(g *IDGenerator) {
		g.seqStart = start
	}
}

// WithIDPrefix 设置ID前缀
func WithIDPrefix(prefix string) Option {
	return func(g *IDGenerator) {
		g.idPrefix = prefix
	}
}

// WithIDSuffix 设置ID后缀
func WithIDSuffix(suffix string) Option {
	return func(g *IDGenerator) {
		g.idSuffix = suffix
	}
}

// WithSeparator 设置分隔符
func WithSeparator(sep string) Option {
	return func(g *IDGenerator) {
		g.separator = sep
	}
}

func WithSequenceInitCallback(cb func() int64) Option {
    return func(g *IDGenerator) {
        g.OnSequenceInit = cb
    }
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
	// 首次设置起始值（优先用回调）
	if exists, err := g.redisClient.Exists(ctx, redisKey).Result(); err == nil && exists == 0 {
		start := g.seqStart
		if g.OnSequenceInit != nil {
			start = g.OnSequenceInit()
		}
		if start > 1 {
			if err := g.redisClient.Set(ctx, redisKey, start, g.expire).Err(); err != nil {
				return "", fmt.Errorf("failed to set sequence start: %w", err)
			}
		}
	}
	seq, err := g.redisClient.Incr(ctx, redisKey).Result()
	if err != nil {
		return "", fmt.Errorf("failed to get sequence from redis: %w", err)
	}
	_ = g.redisClient.Expire(ctx, redisKey, g.expire)
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

// ResetSequence 重置指定时间的序列号（仅用于测试或特殊场景）
func (g *IDGenerator) ResetSequence(ctx context.Context, timePrefix string) error {
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
func (g *IDGenerator) GetIDPrefix() string    { return g.idPrefix }
func (g *IDGenerator) GetIDSuffix() string    { return g.idSuffix }
func (g *IDGenerator) GetSeparator() string   { return g.separator }