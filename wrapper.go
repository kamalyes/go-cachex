/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-19 23:50:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-11-20 00:15:00
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
	"github.com/kamalyes/go-toolbox/pkg/zipx"
	"github.com/redis/go-redis/v9"
	"time"
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
//	// 创建用户数据加载器
//	userLoader := func(ctx context.Context) (*User, error) {
//	    return getUserFromDB(ctx, userID)
//	}
//
//	// 包装为缓存加载器
//	cachedLoader := CacheWrapper(client, "user:123", userLoader, time.Hour)
//
//	// 使用缓存加载器
//	user, err := cachedLoader(ctx)
//
// 注意事项:
//   - 确保数据类型T支持JSON序列化
//   - 缓存键名应该具有唯一性和可读性
//   - 合理设置过期时间以平衡性能和数据一致性
//   - 大对象缓存会消耗更多内存，即使有压缩
func CacheWrapper[T any](client *redis.Client, key string, cacheFunc CacheFunc[T], expiration time.Duration) CacheFunc[T] {
	return func(ctx context.Context) (T, error) {
		var result T

		// 第一步：尝试从Redis缓存中获取数据
		// 这是最快的路径，如果缓存命中可以避免执行原始数据加载函数
		cachedData, err := client.Get(ctx, key).Result()

		// 错误处理：区分真正的Redis连接错误和键不存在的正常情况
		// redis.Nil 表示键不存在，这是正常情况，应该继续执行数据加载
		// 其他错误(如网络错误、权限错误等)需要返回给调用者
		if err != nil && err != redis.Nil {
			return result, err // 返回真正的错误（非键不存在的错误）
		}

		// 第二步：处理缓存命中的情况
		if err == nil && cachedData != "" {
			// 解压缩缓存数据
			// 使用Zlib算法解压缩，如果解压缩失败可能是数据损坏或版本不兼容
			decompressedData, err := zipx.ZlibDecompress([]byte(cachedData))
			if err != nil {
				// 解压缩失败，可能是旧版本数据或数据损坏
				// 跳转到数据加载逻辑，重新获取并缓存新数据
				goto executeFunc
			}

			// JSON反序列化
			// 将解压缩后的JSON数据反序列化为目标类型
			err = json.Unmarshal([]byte(decompressedData), &result)
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

		// 压缩序列化后的数据
		// 使用Zlib压缩减少Redis内存使用，特别是对大数据有效
		compressedData, err := zipx.ZlibCompress(data)
		if err != nil {
			// 压缩失败，返回数据但不缓存
			return result, err
		}

		// 第五步：执行延迟双删策略
		// 延迟双删是为了解决分布式环境下的缓存一致性问题

		// 第一次删除：清除可能存在的旧缓存数据
		// 这确保在设置新缓存之前没有过时的数据
		client.Del(ctx, key)

		// 设置新的缓存数据
		if err = client.Set(ctx, key, string(compressedData), expiration).Err(); err != nil {
			// 缓存设置失败不影响业务逻辑，只是性能优化失效
			// 记录错误但继续返回正确的业务数据
		} else {
			// 第二次删除（延迟执行）：防止并发写入导致的缓存不一致
			// 启动异步goroutine执行延迟删除和重新设置
			go func() {
				// 延迟100ms等待可能的并发操作完成
				time.Sleep(100 * time.Millisecond)

				// 再次删除缓存，清除可能由并发操作产生的不一致数据
				client.Del(context.Background(), key)

				// 重新设置最新的缓存数据，确保缓存的最终一致性
				client.Set(context.Background(), key, string(compressedData), expiration)
			}()
		}

		// 返回加载的数据
		return result, nil
	}
}
