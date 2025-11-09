/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-05 22:55:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-09 18:58:57
 * @FilePath: \go-cachex\ctxcache.go
 * @Description: context 的缓存封装 `CtxCache`
 * 实现之上提供对上下文取消和并发去重的支持主要功能包括：
 *  - 提供 `GetOrCompute(ctx, key, ttl, loader)` 方法：先尝试从底层缓存读取，未命中时
 *    使用内部的 singleflight（flightGroup）对并发加载进行去重，并在 loader 成功后
 *    将结果写回底层缓存（带可选 TTL）
 *  - 代理常见的缓存操作（Get/Set/SetWithTTL/Del/Close），使现有的 Handler 可无缝
 *    被升级为支持 context 的缓存层
 *
 * 使用注意：
 *  - 传入的 loader 必须尊重 ctx 取消（及时返回），以便在调用方取消时能中断加载
 *  - 写回缓存的操作是“最佳努力”的：写失败不会影响 loader 的返回结果，但可能
 *    导致后续命中率下降
 *  - 本文件内实现了一个简易的 flightGroup 以代替外部的 singleflight 依赖，适用于
 *    常见去重场景，不保证对极端高并发或长时间阻塞的优化需求
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"sync"
	"time"
)

// CtxCache 是一个基于 context 的缓存封装，包装已有的 Handler
// 它提供支持 context 取消的 GetOrCompute（带 loader）以及并发去重（singleflight）
type CtxCache struct {
    handler Handler
    group   *flightGroup
}

// NewCtxCache 用现有的 Handler 创建 CtxCache
func NewCtxCache(h Handler) *CtxCache {
    return &CtxCache{handler: h, group: newFlightGroup()}
}

// Close 关闭底层 handler（如果底层实现需要关闭）
func (c *CtxCache) Close() error {
    if c.handler != nil {
        return c.handler.Close()
    }
    return nil
}

// Get 直接从底层缓存读取
func (c *CtxCache) Get(ctx context.Context, key []byte) ([]byte, error) {
    if err := ValidateBasicOp(key, c.handler != nil, false); err != nil {
        return nil, err
    }
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
        return c.handler.Get(key)
    }
}

// Set 直接写入底层缓存（无 TTL）
func (c *CtxCache) Set(ctx context.Context, key, value []byte) error {
    if err := ValidateWriteOp(key, value, c.handler != nil, false); err != nil {
        return err
    }
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        return c.handler.Set(key, value)
    }
}

// SetWithTTL 写入缓存并设置 TTL
func (c *CtxCache) SetWithTTL(ctx context.Context, key, value []byte, ttl time.Duration) error {
    if err := ValidateWriteWithTTLOp(key, value, ttl, c.handler != nil, false); err != nil {
        return err
    }
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        return c.handler.SetWithTTL(key, value, ttl)
    }
}

// Del 删除键
func (c *CtxCache) Del(ctx context.Context, key []byte) error {
    if err := ValidateBasicOp(key, c.handler != nil, false); err != nil {
        return err
    }
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        return c.handler.Del(key)
    }
}

// GetOrCompute：先尝试从缓存读取；若未命中，则使用 singleflight 保证并发时只会执行一次 loader
// loader 应该尊重传入的 ctx（以便在 ctx 取消时尽早停止）
// - key: 缓存键（字节切片）
// - ttl: 保存到缓存的 TTL（<=0 表示不设置 TTL）
// - loader: 计算值的函数，接收同一个 ctx
func (c *CtxCache) GetOrCompute(ctx context.Context, key []byte, ttl time.Duration, loader func(context.Context) ([]byte, error)) ([]byte, error) {
    if err := ValidateBasicOp(key, c.handler != nil, false); err != nil {
        return nil, err
    }
    if err := ValidateTTL(ttl); err != nil {
        return nil, err
    }
    if loader == nil {
        return nil, ErrInvalidValue
    }

    // 先检查上下文是否已取消
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
    }

    // 先尝试命中
    if val, err := c.handler.Get(key); err == nil {
        return val, nil
    }

    // 使用自实现的 flightGroup 去重，key 用 string(key) 做唯一标识
    sfKey := string(key)
    v, err := c.group.Do(ctx, sfKey, func(ctx context.Context) (interface{}, error) {
        return loader(ctx)
    })

    if err != nil {
        return nil, err
    }

    // 计算成功，尝试写入缓存（写失败不影响返回值）
    if data, ok := v.([]byte); ok {
        if data != nil { // 只有非 nil 数据才写入缓存
            if ttl <= 0 {
                _ = c.handler.Set(key, data)
            } else {
                _ = c.handler.SetWithTTL(key, data, ttl)
            }
        }
        return data, nil
    }
    return nil, ErrNotFound
}

// --- 简易 singleflight 实现 -------------------------------------------------
type call struct {
    done chan struct{}
    val  interface{}
    err  error
}

type flightGroup struct {
    mu sync.Mutex
    m  map[string]*call
}

func newFlightGroup() *flightGroup {
    return &flightGroup{m: make(map[string]*call)}
}

// Do 调用 fn（传入 ctx）并对相同 key 的并发调用去重
// 如果已有在进行中的调用，会等待其完成或在 ctx 取消时返回 ctx.Err()
func (g *flightGroup) Do(ctx context.Context, key string, fn func(context.Context) (interface{}, error)) (interface{}, error) {
    g.mu.Lock()
    if c, ok := g.m[key]; ok {
        // 已有调用在进行，等待其完成或 ctx 取消
        done := c.done
        g.mu.Unlock()
        select {
        case <-done:
            return c.val, c.err
        case <-ctx.Done():
            return nil, ctx.Err()
        }
    }

    // 创建新的 call 并放入 map
    c := &call{done: make(chan struct{})}
    g.m[key] = c
    g.mu.Unlock()

    // 执行 fn（不应被挂起的等待者取消）
    v, err := fn(ctx)

    // 标记完成并从 map 中移除
    g.mu.Lock()
    c.val = v
    c.err = err
    close(c.done)
    delete(g.m, key)
    g.mu.Unlock()

    return v, err
}


// ctx 中存储 CtxCache 的 key 类型
type ctxCacheKey struct{}

// WithCache 把 CtxCache 写入 ctx
func WithCache(ctx context.Context, c *CtxCache) context.Context {
    return context.WithValue(ctx, ctxCacheKey{}, c)
}

// FromContext 从 ctx 中读取 CtxCache（若不存在返回 nil）
func FromContext(ctx context.Context) *CtxCache {
    if v := ctx.Value(ctxCacheKey{}); v != nil {
        if c, ok := v.(*CtxCache); ok {
            return c
        }
    }
    return nil
}
