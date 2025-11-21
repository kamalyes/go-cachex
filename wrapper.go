/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 23:50:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-21 13:55:08
 * @FilePath: \go-cachex\wrapper.go
 * @Description: 缓存包装器实现
 *
 * 缓存包装器(CacheWrapper)是一个高阶函数，用于为任意数据加载函数添加Redis缓存功能。
 *
 * 核心特性：
 * - 泛型支持：支持任意类型的数据缓存
 * - 数据压缩：使用Zlib算法压缩缓存数据，节省Redis内存
 * - 延迟双删：实现延迟双删策略，保证缓存一致性
 * - 错误处理：优雅处理Redis连接错误和数据序列化错误
 * - 并发安全：支持高并发访问
 *
 * 使用示例：
 *   loader := CacheWrapper(redisClient, "cache_key", dataLoaderFunc, time.Hour)
 *   result, err := loader(ctx)
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package cachex

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kamalyes/go-toolbox/pkg/zipx"
	"github.com/redis/go-redis/v9"
)

// CacheFunc 是一个函数类型，用于表示返回数据和错误的数据加载函数。
//
// 泛型参数:
//
//	T: 数据加载函数返回的数据类型，可以是任意可序列化为JSON的类型
//
// 参数:
//
//	ctx: 上下文，用于控制超时和取消
//
// 返回值:
//
//	T: 加载的数据
//	error: 加载过程中可能出现的错误
type CacheFunc[T any] func(ctx context.Context) (T, error)

// CacheOptions 缓存选项配置
type CacheOptions struct {
	ForceRefresh bool           // 是否强制刷新缓存（清除缓存重新获取）
	TTLOverride  *time.Duration // 覆盖默认 TTL（为 nil 时使用默认值）
	SkipCompress bool           // 跳过压缩（用于小数据或已压缩的数据）
	UseAsync     bool           // 使用异步更新缓存（适用于非关键数据）
	RetryOnError bool           // Redis 错误时重试（默认不重试）
	RetryTimes   int            // 重试次数（默认 0）
}

// CacheOption 缓存选项函数类型
type CacheOption func(*CacheOptions)

// WithForceRefresh 设置是否强制刷新缓存
//
// 参数:
//
//	force: true 表示强制从数据源加载，false 表示优先使用缓存
//
// 使用场景:
//   - 管理员操作需要最新数据
//   - 定时任务刷新缓存
//   - 数据变更后立即更新缓存
func WithForceRefresh(force bool) CacheOption {
	return func(opts *CacheOptions) {
		opts.ForceRefresh = force
	}
}

// WithTTL 覆盖默认的缓存过期时间
//
// 参数:
//
//	ttl: 自定义的缓存过期时间
//
// 使用场景:
//   - 动态调整缓存时长
//   - 不同条件下使用不同的 TTL
//   - VIP 用户使用更长的缓存时间
func WithTTL(ttl time.Duration) CacheOption {
	return func(opts *CacheOptions) {
		opts.TTLOverride = &ttl
	}
}

// WithoutCompression 跳过数据压缩
//
// 使用场景:
//   - 数据量很小（如布尔值、小字符串）
//   - 数据已经压缩过（如图片、视频链接）
//   - 需要极致性能，压缩开销大于收益
func WithoutCompression() CacheOption {
	return func(opts *CacheOptions) {
		opts.SkipCompress = true
	}
}

// WithAsyncUpdate 使用异步方式更新缓存
//
// 使用场景:
//   - 非关键数据，允许短暂的数据延迟
//   - 高并发场景，减少阻塞
//   - 缓存更新耗时较长的情况
func WithAsyncUpdate() CacheOption {
	return func(opts *CacheOptions) {
		opts.UseAsync = true
	}
}

// WithRetry 设置 Redis 操作失败时重试
//
// 参数:
//
//	times: 重试次数（建议不超过 3 次）
//
// 使用场景:
//   - Redis 网络不稳定
//   - 关键数据必须缓存成功
//   - 提高缓存可靠性
func WithRetry(times int) CacheOption {
	return func(opts *CacheOptions) {
		opts.RetryOnError = true
		if times <= 0 {
			times = 1
		}
		opts.RetryTimes = times
	}
}

// When 条件选项构建器 - 当条件为 true 时应用选项
//
// 参数:
//
//	condition: 条件表达式
//	opt: 当条件为 true 时应用的选项
//
// 使用场景:
//   - 简化条件选项的构建代码
//   - 提高代码可读性
//   - 函数式编程风格
//
// 示例:
//
//	When(isVIP, WithTTL(time.Hour * 24))
//	When(needRefresh, WithForceRefresh(true))
func When(condition bool, opt CacheOption) CacheOption {
	return func(opts *CacheOptions) {
		if condition {
			opt(opts)
		}
	}
}

// WhenThen 条件选项构建器 - 根据条件选择不同的选项
//
// 参数:
//
//	condition: 条件表达式
//	thenOpt: 当条件为 true 时应用的选项
//	elseOpt: 当条件为 false 时应用的选项
//
// 使用场景:
//   - 二选一的选项场景
//   - 简化 if-else 逻辑
//   - 函数式编程风格
//
// 示例:
//
//	WhenThen(isVIP, WithTTL(time.Hour * 24), WithTTL(time.Hour))
func WhenThen(condition bool, thenOpt, elseOpt CacheOption) CacheOption {
	return func(opts *CacheOptions) {
		if condition {
			thenOpt(opts)
		} else {
			elseOpt(opts)
		}
	}
}

// Match 多条件匹配选项构建器 - 类似 switch-case
//
// 参数:
//
//	cases: 条件-选项对的切片
//	defaultOpt: 所有条件都不满足时的默认选项（可选）
//
// 使用场景:
//   - 多个互斥条件的选项选择
//   - 替代复杂的 if-else-if 链
//   - 提高代码可读性
//
// 示例:
//
//	Match([]Case{
//	    {Condition: level == "VIP", Opt: WithTTL(time.Hour * 24)},
//	    {Condition: level == "Premium", Opt: WithTTL(time.Hour * 12)},
//	}, WithTTL(time.Hour))
func Match(cases []Case, defaultOpt ...CacheOption) CacheOption {
	return func(opts *CacheOptions) {
		for _, c := range cases {
			if c.Condition {
				c.Opt(opts)
				return
			}
		}
		// 应用默认选项
		for _, opt := range defaultOpt {
			opt(opts)
		}
	}
}

// Case 条件-选项对
type Case struct {
	Condition bool        // 条件
	Opt       CacheOption // 选项
}

// NewCase 创建条件-选项对
func NewCase(condition bool, opt CacheOption) Case {
	return Case{Condition: condition, Opt: opt}
}

// Combine 组合多个选项为一个选项
//
// 参数:
//
//	opts: 要组合的选项列表
//
// 使用场景:
//   - 将多个相关选项组合成一个预设
//   - 创建可重用的选项组合
//   - 简化选项传递
//
// 示例:
//
//	vipPreset := Combine(
//	    WithTTL(time.Hour * 24),
//	    WithRetry(3),
//	    WithAsyncUpdate(),
//	)
func Combine(opts ...CacheOption) CacheOption {
	return func(options *CacheOptions) {
		for _, opt := range opts {
			opt(options)
		}
	}
}

// CacheWrapper 是一个高阶函数，用于为数据加载函数添加Redis缓存功能。
//
// 该函数实现了以下核心特性：
//  1. 缓存查询：首先尝试从Redis获取缓存数据
//  2. 数据压缩：使用Zlib算法压缩缓存数据，减少内存使用
//  3. 延迟双删：实现延迟双删策略，防止并发写入导致的缓存不一致
//  4. 错误处理：区分Redis连接错误和键不存在错误，优雅降级
//
// 缓存流程：
//  1. 尝试从Redis获取缓存数据
//  2. 如果缓存存在且有效，解压缩并反序列化返回
//  3. 如果缓存不存在或无效，执行原始数据加载函数
//  4. 将加载的数据序列化、压缩后存储到Redis
//  5. 执行延迟双删策略确保缓存一致性
//
// 延迟双删策略：
//   - 第一次删除：在设置新缓存前删除旧缓存
//   - 设置缓存：存储新的缓存数据
//   - 延迟删除：100ms后再次删除缓存并重新设置，防止并发问题
//
// 泛型参数:
//
//	T: 缓存数据的类型，必须支持JSON序列化
//
// 参数:
//
//	client: Redis客户端实例
//	key: 缓存键名，建议使用有意义的命名规范
//	cacheFunc: 数据加载函数，当缓存不存在时调用
//	expiration: 缓存过期时间
//
// 返回值:
//
//	返回一个缓存包装后的数据加载函数，该函数具有相同的签名但增加了缓存功能
//
// 使用示例:
//
//	创建用户数据加载器
//	userLoader := func(ctx context.Context) (*User, error) {
//	    return getUserFromDB(ctx, userID)
//	}
//
//	包装为缓存加载器
//	cachedLoader := CacheWrapper(client, "user:123", userLoader, time.Hour)
//
//	使用缓存加载器
//	user, err := cachedLoader(ctx)
//
// 注意事项:
//   - 确保数据类型T支持JSON序列化
//   - 缓存键名应该具有唯一性和可读性
//   - 合理设置过期时间以平衡性能和数据一致性
//   - 大对象缓存会消耗更多内存，即使有压缩
//   - 可通过 WithForceRefresh(true) 强制刷新缓存
func CacheWrapper[T any](client *redis.Client, key string, cacheFunc CacheFunc[T], expiration time.Duration, opts ...CacheOption) CacheFunc[T] {
	return func(ctx context.Context) (T, error) {
		var result T
		var err error
		var cachedData string
		var decompressedData []byte

		// 应用选项配置
		options := &CacheOptions{
			RetryTimes: 0, // 默认不重试
		}
		for _, opt := range opts {
			opt(options)
		}

		// 应用 TTL 覆盖选项
		if options.TTLOverride != nil {
			expiration = *options.TTLOverride
		}

		// 如果设置了强制刷新，直接跳转到数据加载逻辑
		if options.ForceRefresh {
			// 删除旧缓存
			client.Del(ctx, key)
			goto executeFunc
		}

		// 第一步：尝试从Redis缓存中获取数据
		// 这是最快的路径，如果缓存命中可以避免执行原始数据加载函数
		cachedData, err = client.Get(ctx, key).Result()

		// 错误处理：区分真正的Redis连接错误和键不存在的正常情况
		// redis.Nil 表示键不存在，这是正常情况，应该继续执行数据加载
		// 其他错误(如网络错误、权限错误等)需要返回给调用者
		if err != nil && err != redis.Nil {
			return result, err // 返回真正的错误（非键不存在的错误）
		}

		// 第二步：处理缓存命中的情况
		if err == nil && cachedData != "" {
			// 根据压缩选项处理数据
			if options.SkipCompress {
				// 未压缩数据，直接反序列化
				decompressedData = []byte(cachedData)
			} else {
				// 解压缩缓存数据
				// 使用Zlib算法解压缩，如果解压缩失败可能是数据损坏或版本不兼容
				decompressedData, err = zipx.ZlibDecompress([]byte(cachedData))
				if err != nil {
					// 解压缩失败，可能是旧版本数据或数据损坏
					// 跳转到数据加载逻辑，重新获取并缓存新数据
					goto executeFunc
				}
			}

			// JSON反序列化
			// 将解压缩后的JSON数据反序列化为目标类型
			err = json.Unmarshal(decompressedData, &result)
			if err != nil {
				// 反序列化失败，可能是数据结构变更或数据损坏
				// 跳转到数据加载逻辑，重新获取数据
				goto executeFunc
			}

			// 缓存命中，返回反序列化后的数据
			return result, nil
		}

	executeFunc:
		// 第三步：执行原始数据加载函数
		// 当缓存未命中、解压缩失败或反序列化失败时执行此逻辑
		result, err = cacheFunc(ctx)
		if err != nil {
			// 数据加载失败，直接返回错误，不进行缓存操作
			return result, err
		}

		// 第四步：准备缓存数据
		// 将加载的数据序列化为JSON格式
		data, err := json.Marshal(result)
		if err != nil {
			// 序列化失败，返回数据但不缓存
			// 这通常发生在数据类型不支持JSON序列化时
			return result, err
		}

		// 根据压缩选项处理数据
		var cacheData string
		if options.SkipCompress {
			// 跳过压缩，直接存储原始 JSON 数据
			cacheData = string(data)
		} else {
			// 压缩序列化后的数据
			// 使用Zlib压缩减少Redis内存使用，特别是对大数据有效
			compressedData, err := zipx.ZlibCompress(data)
			if err != nil {
				// 压缩失败，返回数据但不缓存
				return result, err
			}
			cacheData = string(compressedData)
		}

		// 第五步：执行延迟双删策略
		// 延迟双删是为了解决分布式环境下的缓存一致性问题

		// 如果启用了异步更新，则在后台更新缓存
		if options.UseAsync {
			go updateCache(client, key, cacheData, expiration, options)
			return result, nil
		}

		// 同步更新缓存
		updateCache(client, key, cacheData, expiration, options)

		// 返回加载的数据
		return result, nil
	}
}

// updateCache 更新缓存的内部辅助函数
// 实现延迟双删策略和重试机制
func updateCache(client *redis.Client, key string, cacheData string, expiration time.Duration, options *CacheOptions) {
	ctx := context.Background()

	// 第一次删除：清除可能存在的旧缓存数据
	// 这确保在设置新缓存之前没有过时的数据
	client.Del(ctx, key)

	// 设置新的缓存数据（带重试机制）
	var err error
	retryCount := 0
	maxRetries := options.RetryTimes

	for {
		err = client.Set(ctx, key, cacheData, expiration).Err()
		if err == nil {
			break // 设置成功
		}

		// 如果不需要重试或已达到最大重试次数，则退出
		if !options.RetryOnError || retryCount >= maxRetries {
			break
		}

		// 重试前等待一小段时间（指数退避）
		retryCount++
		waitTime := time.Duration(retryCount*retryCount*50) * time.Millisecond
		time.Sleep(waitTime)
	}

	// 如果设置成功，执行延迟双删
	if err == nil {
		// 第二次删除（延迟执行）：防止并发写入导致的缓存不一致
		// 启动异步goroutine执行延迟删除和重新设置
		go func() {
			// 延迟100ms等待可能的并发操作完成
			time.Sleep(100 * time.Millisecond)

			// 再次删除缓存，清除可能由并发操作产生的不一致数据
			client.Del(context.Background(), key)

			// 重新设置最新的缓存数据，确保缓存的最终一致性
			client.Set(context.Background(), key, cacheData, expiration)
		}()
	}
}
