/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-06 21:15:15
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-07-06 00:15:56
 * @FilePath: \go-cachex\generic_cache.go
 * @Description: 泛型本地 TTL 缓存（GenericTTLCache）
 *
 * 与 Handler（[]byte 值）互补，支持任意类型值（如 *rsa.PublicKey、bool、struct 指针等），
 * 避免序列化开销，适合鉴权热路径等高性能场景
 *
 * 设计要点：
 *   - 基于 sync.Map，无锁读路径，适合读多写少场景
 *   - 惰性过期：读取时检查过期时间，过期则删除并视为未命中
 *   - 不主动清理过期项（避免后台 goroutine 开销）；过期项占用极小内存，
 *     下次读取时被惰性删除，或在主动 Delete 时被清理
 *   - 分布式一致性：通过短 TTL 容忍秒级不一致，配合主动 Delete 失效；
 *     启用 PubSub（WithPubSub 选项）后，Delete/Clear 事件广播到所有节点，
 *     收到事件的节点删除本地缓存，实现跨节点即时失效
 *
 * KeyCodec 解决 Go 泛型键序列化问题：
 *   Go 泛型无法在运行时从 string 还原为类型参数 K（comparable 约束不足以支撑反序列化），
 *   kv_cache.go 中 any(ks).(K) 仅对 string 生效，对 int 等类型静默失效
 *   通过 KeyCodec[K] 接口显式提供 Encode/Decode，使 PubSub 跨节点传输时能正确还原键，
 *   同时覆盖 string/int/int64 等常见类型，自定义键类型实现接口即可
 */
package cachex

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/mathx"
)

// ============================================================================
// KeyCodec —— 泛型键编解码器
// ============================================================================

// KeyCodec 键编解码器
// 用于 PubSub 跨节点传输时将泛型键 K 序列化为字符串，以及将字符串反序列化回 K
// 解决 Go 泛型无法在运行时从 string 还原为 K 的限制（comparable 约束不足以支撑反序列化）
type KeyCodec[K comparable] interface {
	// Encode 将键序列化为字符串（用于 PubSub 传输）
	Encode(key K) string
	// Decode 将字符串反序列化为键
	Decode(data string) (K, error)
}

// StringKeyCodec string 键编解码器（恒等变换，零开销）
type StringKeyCodec struct{}

func (StringKeyCodec) Encode(key string) string           { return key }
func (StringKeyCodec) Decode(data string) (string, error) { return data, nil }

// IntKeyCodec int 键编解码器
type IntKeyCodec struct{}

func (IntKeyCodec) Encode(key int) string { return strconv.Itoa(key) }
func (IntKeyCodec) Decode(data string) (int, error) {
	return strconv.Atoi(data)
}

// Int64KeyCodec int64 键编解码器
type Int64KeyCodec struct{}

func (Int64KeyCodec) Encode(key int64) string { return strconv.FormatInt(key, 10) }
func (Int64KeyCodec) Decode(data string) (int64, error) {
	return strconv.ParseInt(data, 10, 64)
}

// Uint64KeyCodec uint64 键编解码器
type Uint64KeyCodec struct{}

func (Uint64KeyCodec) Encode(key uint64) string { return strconv.FormatUint(key, 10) }
func (Uint64KeyCodec) Decode(data string) (uint64, error) {
	return strconv.ParseUint(data, 10, 64)
}

// ============================================================================
// 泛型缓存条目
// ============================================================================

// genericEntry 泛型缓存条目，带过期时间
type genericEntry[V any] struct {
	value     V
	expiresAt time.Time
}

func (e genericEntry[V]) expired() bool {
	return time.Now().After(e.expiresAt)
}

// genericInvalidateMsg 跨节点失效广播消息
type genericInvalidateMsg struct {
	Sender string `json:"sender"`        // 发送方实例 ID，避免处理自己发的消息
	Op     string `json:"op"`            // 操作类型：delete / clear
	Key    string `json:"key,omitempty"` // 编码后的键（clear 时为空）
}

// ============================================================================
// GenericTTLCache
// ============================================================================

// GenericTTLCache 基于 sync.Map + TTL 的泛型本地缓存
// 与 Handler（[]byte 值）互补：Handler 适合缓存可序列化数据，GenericTTLCache 适合缓存内存对象
//
// 启用跨节点失效（可选）：
//
//	pubsub := cachex.NewPubSub(redisClient)
//	cache := cachex.NewGenericTTLCache[string, *rsa.PublicKey](
//	    cachex.WithPubSub[string, *rsa.PublicKey](pubsub, "ac:jwks:invalidate", cachex.StringKeyCodec{}),
//	)
//	defer cache.Stop()
type GenericTTLCache[K comparable, V any] struct {
	m sync.Map

	// PubSub 跨节点失效（可选，通过 WithPubSub 启用）
	pubsub     *PubSub
	codec      KeyCodec[K]
	channel    string // PubSub 频道名
	instanceID string // 本节点唯一标识，避免处理自己发的消息
	subscriber *Subscriber
	logger     logger.ILogger
	stopOnce   sync.Once
}

// GenericCacheOption 泛型缓存配置选项
type GenericCacheOption[K comparable, V any] func(*GenericTTLCache[K, V])

// WithPubSub 启用 PubSub 跨节点失效
// 同一逻辑缓存的所有节点必须使用相同 channel 和兼容的 codec
// 构造时自动订阅失效频道，Delete/Clear 会广播到其他节点
func WithPubSub[K comparable, V any](pubsub *PubSub, channel string, codec KeyCodec[K]) GenericCacheOption[K, V] {
	return func(c *GenericTTLCache[K, V]) {
		c.pubsub = pubsub
		c.channel = channel
		c.codec = codec
		c.instanceID = fmt.Sprintf("%d-%d", time.Now().UnixNano(), randomID())
	}
}

// WithLogger 配置日志记录器（不设置则使用默认 logger）
func WithLogger[K comparable, V any](log logger.ILogger) GenericCacheOption[K, V] {
	return func(c *GenericTTLCache[K, V]) {
		if log != nil {
			c.logger = log
		}
	}
}

// NewGenericTTLCache 创建泛型本地 TTL 缓存
// 不传选项时为纯本地缓存；通过 WithPubSub 启用跨节点失效后，构造时自动订阅失效频道
func NewGenericTTLCache[K comparable, V any](opts ...GenericCacheOption[K, V]) *GenericTTLCache[K, V] {
	c := &GenericTTLCache[K, V]{
		logger: mathx.IfEmpty(globalLogger, NewDefaultCachexLogger()),
	}
	for _, opt := range opts {
		opt(c)
	}
	// 启用 PubSub 时自动启动失效订阅
	if c.pubsub != nil && c.codec != nil {
		c.startInvalidationSubscriber()
	}
	return c
}

// Load 加载缓存项，未命中或已过期返回零值与 false
func (c *GenericTTLCache[K, V]) Load(key K) (V, bool) {
	v, ok := c.m.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	entry, ok := v.(genericEntry[V])
	if !ok {
		var zero V
		return zero, false
	}
	if entry.expired() {
		c.m.Delete(key) // 惰性删除，避免过期项长期驻留
		var zero V
		return zero, false
	}
	return entry.value, true
}

// Store 存储缓存项，ttl 为存活时长
func (c *GenericTTLCache[K, V]) Store(key K, value V, ttl time.Duration) {
	c.m.Store(key, genericEntry[V]{
		value:     value,
		expiresAt: time.Now().Add(ttl),
	})
}

// Delete 删除缓存项 启用 PubSub 时会广播失效事件到其他节点
func (c *GenericTTLCache[K, V]) Delete(key K) {
	c.m.Delete(key)
	c.publishInvalidation("delete", key)
}

// Invalidate 广播失效事件到其他节点，不删除本地缓存
// 用于本节点写入新值后，让其他节点删除旧值，下次读取走回源拿最新值
// 典型场景：拉黑 jti 后本节点 Store(true)，调用 Invalidate 让其他节点删除旧值后回源感知拉黑
func (c *GenericTTLCache[K, V]) Invalidate(key K) {
	c.publishInvalidation("delete", key)
}

// DeleteLocal 仅删除本地缓存项，不广播失效事件
// 用于本地缓存疑似脏数据的自愈场景，不影响其他节点
func (c *GenericTTLCache[K, V]) DeleteLocal(key K) {
	c.m.Delete(key)
}

// Clear 清空本地缓存并广播 clear 事件（启用 PubSub 时其他节点同步清空）
func (c *GenericTTLCache[K, V]) Clear() {
	c.m.Range(func(k, _ any) bool {
		c.m.Delete(k)
		return true
	})
	var zero K
	c.publishInvalidation("clear", zero)
}

// publishInvalidation 广播失效消息给其他节点
// op: "delete"（单键失效）或 "clear"（全量清空）
func (c *GenericTTLCache[K, V]) publishInvalidation(op string, key K) {
	if c.pubsub == nil || c.codec == nil {
		return
	}
	msg := genericInvalidateMsg{
		Sender: c.instanceID,
		Op:     op,
	}
	if op == "delete" {
		msg.Key = c.codec.Encode(key)
	}
	data, err := json.Marshal(msg)
	if err != nil {
		c.logger.Warnf("GenericTTLCache marshal invalidation msg failed: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.pubsub.Publish(ctx, c.channel, string(data)); err != nil {
		c.logger.Warnf("GenericTTLCache publish invalidation failed: %v", err)
	}
}

// startInvalidationSubscriber 启动失效广播订阅
// 收到其他节点的 Delete/Clear 事件后，删除本地对应缓存项（不再广播，避免循环）
func (c *GenericTTLCache[K, V]) startInvalidationSubscriber() {
	sub, err := c.pubsub.Subscribe([]string{c.channel}, func(ctx context.Context, channel, message string) error {
		var msg genericInvalidateMsg
		if err := json.Unmarshal([]byte(message), &msg); err != nil {
			c.logger.Warnf("GenericTTLCache invalidation message unmarshal failed: %v", err)
			return err
		}
		// 忽略自己发的消息（本地缓存在写操作时已更新）
		if msg.Sender == c.instanceID {
			return nil
		}
		switch msg.Op {
		case "clear":
			c.m.Range(func(k, _ any) bool {
				c.m.Delete(k)
				return true
			})
			c.logger.Debugf("GenericTTLCache received clear from %s", msg.Sender)
		default: // delete
			key, err := c.codec.Decode(msg.Key)
			if err != nil {
				c.logger.Warnf("GenericTTLCache decode key failed: %v", err)
				return err
			}
			c.m.Delete(key) // 直接删除本地缓存，不再广播
			c.logger.Debugf("GenericTTLCache received delete: key=%s", msg.Key)
		}
		return nil
	})
	if err != nil {
		c.logger.Warnf("GenericTTLCache subscribe invalidation failed: %v", err)
		return
	}
	c.subscriber = sub
}

// Stop 停止 PubSub 订阅（如果已启用）
// 未启用 PubSub 的实例无需调用；调用后不再接收跨节点失效事件，本地读写不受影响
func (c *GenericTTLCache[K, V]) Stop() {
	c.stopOnce.Do(func() {
		if c.subscriber != nil {
			c.subscriber.Unsubscribe()
			c.subscriber = nil
		}
	})
}
