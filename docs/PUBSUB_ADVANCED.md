# 发布订阅(PubSub)组件高级使用指南

## 🏗️ 架构设计

### 核心组件架构

```
                      PubSub Manager
                           ↓
        ┌─────────────┬─────────────┬─────────────┐
        │  Publisher  │ Subscriber  │  Pattern    │
        │             │             │ Subscriber  │
        └─────────────┴─────────────┴─────────────┘
                ↓           ↓           ↓
    ┌───────────────────────────────────────────────┐
    │            Redis PubSub                       │
    │    PUBLISH    SUBSCRIBE    PSUBSCRIBE        │
    └───────────────────────────────────────────────┘
                           ↓
        ┌─────────────────────────────────────────┐
        │         消息处理管道                      │
        │   重试机制 → 超时控制 → 错误处理          │
        └─────────────────────────────────────────┘
```

### 消息流转架构

```
Publisher                 Redis                 Subscriber
    │                      │                       │
    │──── PUBLISH ────────→│                       │
    │                      │────── 消息分发 ──────→│
    │                      │                       │
    │                      │←───── ACK ────────────│
```

### 特性对比

| 功能特性 | 支持度 | 性能 | 可靠性 | 适用场景 |
|----------|--------|------|--------|----------|
| 点对点消息 | ✅ | 高 | 中 | 实时通知 |
| 广播消息 | ✅ | 高 | 中 | 系统公告 |
| 模式匹配 | ✅ | 中 | 中 | 动态订阅 |
| 请求响应 | ✅ | 中 | 高 | RPC调用 |
| JSON消息 | ✅ | 中 | 高 | 结构化数据 |

## ✅ 推荐使用模式

### 1. 基础配置 - 推荐写法

```go
// ✅ 推荐：完整的配置参数
config := PubSubConfig{
    Namespace:     "myapp",           // 命名空间隔离
    MaxRetries:    3,                 // 合理的重试次数
    RetryDelay:    time.Second,       // 渐进式重试延迟
    BufferSize:    100,               // 适中的缓冲区大小
    EnableLogging: true,              // 开启日志便于调试
    PingInterval:  time.Second * 30,  // 保持连接活跃
}

pubsub := NewPubSub(client, config)
defer pubsub.Close() // ✅ 确保资源清理
```

### 2. 实时通知系统 - 推荐模式

```go
// ✅ 推荐：事件驱动的通知系统
type NotificationService struct {
    pubsub *PubSub
}

func (ns *NotificationService) SendUserNotification(userID string, message string) error {
    channel := fmt.Sprintf("user:%s:notifications", userID)
    
    notification := Notification{
        ID:        generateID(),
        UserID:    userID,
        Message:   message,
        Timestamp: time.Now(),
        Type:      "user_notification",
    }
    
    return PublishJSON[Notification](ns.pubsub, ctx, channel, notification)
}

// ✅ 推荐：结构化的消息处理
func (ns *NotificationService) StartNotificationProcessor(userID string) error {
    channel := fmt.Sprintf("user:%s:notifications", userID)
    
    handler := func(ctx context.Context, channel string, notification Notification) error {
        // ✅ 推荐：幂等性检查
        if ns.isProcessed(notification.ID) {
            return nil
        }
        
        // ✅ 推荐：错误恢复机制
        if err := ns.processNotification(notification); err != nil {
            return fmt.Errorf("failed to process notification %s: %w", notification.ID, err)
        }
        
        ns.markProcessed(notification.ID)
        return nil
    }
    
    subscriber, err := SubscribeJSON[Notification](ns.pubsub, []string{channel}, handler)
    if err != nil {
        return err
    }
    
    // ✅ 推荐：优雅关闭
    go func() {
        <-ns.shutdownChan
        subscriber.Stop()
    }()
    
    return nil
}
```

### 3. 微服务间通信 - 推荐模式

```go
// ✅ 推荐：请求响应模式用于RPC
type ServiceA struct {
    pubsub *PubSub
}

func (s *ServiceA) CallServiceB(request ServiceBRequest) (*ServiceBResponse, error) {
    ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
    defer cancel()
    
    // ✅ 推荐：结构化请求
    requestData := RPCRequest{
        ID:        generateRequestID(),
        ServiceA:  "service-a",
        Method:    "processData",
        Data:      request,
        Timestamp: time.Now(),
    }
    
    responseChannel := fmt.Sprintf("rpc:response:%s", requestData.ID)
    
    // 启动响应监听
    var response ServiceBResponse
    responseChan := make(chan ServiceBResponse, 1)
    
    handler := func(ctx context.Context, channel string, resp ServiceBResponse) error {
        select {
        case responseChan <- resp:
        default:
        }
        return nil
    }
    
    subscriber, err := SubscribeJSON[ServiceBResponse](s.pubsub, []string{responseChannel}, handler)
    if err != nil {
        return nil, err
    }
    defer subscriber.Stop()
    
    // 发送请求
    if err := PublishJSON[RPCRequest](s.pubsub, ctx, "service-b:rpc:requests", requestData); err != nil {
        return nil, err
    }
    
    // 等待响应
    select {
    case response = <-responseChan:
        return &response, nil
    case <-ctx.Done():
        return nil, fmt.Errorf("RPC call timeout")
    }
}
```

### 4. 事件总线模式 - 推荐架构

```go
// ✅ 推荐：领域事件总线
type EventBus struct {
    pubsub   *PubSub
    handlers map[string][]EventHandler
    mu       sync.RWMutex
}

type DomainEvent struct {
    Type        string                 `json:"type"`
    AggregateID string                 `json:"aggregate_id"`
    Version     int64                  `json:"version"`
    Data        map[string]interface{} `json:"data"`
    Metadata    EventMetadata          `json:"metadata"`
}

type EventMetadata struct {
    EventID     string    `json:"event_id"`
    Timestamp   time.Time `json:"timestamp"`
    Source      string    `json:"source"`
    CorrelationID string  `json:"correlation_id"`
}

// ✅ 推荐：事件发布
func (eb *EventBus) PublishEvent(event DomainEvent) error {
    event.Metadata.EventID = generateEventID()
    event.Metadata.Timestamp = time.Now()
    
    channel := fmt.Sprintf("events:%s", event.Type)
    return PublishJSON[DomainEvent](eb.pubsub, ctx, channel, event)
}

// ✅ 推荐：类型安全的事件订阅
func (eb *EventBus) SubscribeToEvent(eventType string, handler EventHandler) error {
    channel := fmt.Sprintf("events:%s", eventType)
    
    messageHandler := func(ctx context.Context, channel string, event DomainEvent) error {
        // ✅ 推荐：事件验证
        if err := eb.validateEvent(event); err != nil {
            return fmt.Errorf("invalid event: %w", err)
        }
        
        // ✅ 推荐：异步处理避免阻塞
        go func() {
            defer func() {
                if r := recover(); r != nil {
                    log.Printf("Event handler panic for %s: %v", eventType, r)
                }
            }()
            
            if err := handler.Handle(ctx, event); err != nil {
                log.Printf("Event handler error for %s: %v", eventType, err)
                // ✅ 推荐：发布错误事件用于监控
                eb.publishErrorEvent(event, err)
            }
        }()
        
        return nil
    }
    
    _, err := SubscribeJSON[DomainEvent](eb.pubsub, []string{channel}, messageHandler)
    return err
}
```

### 5. 聊天系统 - 推荐实现

```go
// ✅ 推荐：实时聊天系统
type ChatService struct {
    pubsub *PubSub
}

type ChatMessage struct {
    ID        string    `json:"id"`
    RoomID    string    `json:"room_id"`
    UserID    string    `json:"user_id"`
    Username  string    `json:"username"`
    Content   string    `json:"content"`
    Timestamp time.Time `json:"timestamp"`
    Type      string    `json:"type"` // text, image, file, system
}

// ✅ 推荐：房间级别的消息分发
func (cs *ChatService) SendMessage(roomID, userID, username, content string) error {
    message := ChatMessage{
        ID:        generateMessageID(),
        RoomID:    roomID,
        UserID:    userID,
        Username:  username,
        Content:   content,
        Timestamp: time.Now(),
        Type:      "text",
    }
    
    // ✅ 推荐：消息持久化与分发分离
    if err := cs.saveMessage(message); err != nil {
        return err
    }
    
    channel := fmt.Sprintf("chat:room:%s", roomID)
    return PublishJSON[ChatMessage](cs.pubsub, ctx, channel, message)
}

// ✅ 推荐：用户加入聊天室
func (cs *ChatService) JoinRoom(userID, roomID string, msgHandler func(ChatMessage)) error {
    channel := fmt.Sprintf("chat:room:%s", roomID)
    
    handler := func(ctx context.Context, channel string, message ChatMessage) error {
        // ✅ 推荐：过滤自己发送的消息（可选）
        if message.UserID == userID {
            return nil
        }
        
        // ✅ 推荐：非阻塞消息处理
        go msgHandler(message)
        return nil
    }
    
    return cs.subscribeToRoom(roomID, handler)
}
```

## ❌ 不推荐使用模式

### 1. 阻塞操作反模式

```go
// ❌ 不推荐：在消息处理器中执行阻塞操作
handler := func(ctx context.Context, channel string, message string) error {
    // ❌ 这会阻塞消息循环
    time.Sleep(time.Second * 10)
    
    // ❌ 网络调用没有超时控制
    resp, err := http.Get("http://slow-service.com/api")
    
    // ❌ 数据库操作没有超时
    db.Query("SELECT * FROM large_table WHERE condition")
    
    return nil
}
```

### 2. 资源泄露反模式

```go
// ❌ 不推荐：忘记关闭订阅者
func BadSubscribeExample() {
    pubsub := NewPubSub(client)
    
    subscriber, _ := pubsub.Subscribe([]string{"test"}, handler)
    // ❌ 忘记调用subscriber.Stop()和pubsub.Close()
}

// ❌ 不推荐：无限创建订阅者
for userID := range users {
    // ❌ 每次循环创建新的订阅者而不清理
    pubsub.Subscribe([]string{fmt.Sprintf("user:%s", userID)}, handler)
}
```

### 3. 错误处理反模式

```go
// ❌ 不推荐：忽略错误
pubsub.Publish(ctx, "channel", message) // 忽略错误

// ❌ 不推荐：在处理器中panic
handler := func(ctx context.Context, channel string, message string) error {
    data := parseMessage(message) // 可能panic
    processData(data)             // 可能panic
    return nil
}

// ❌ 不推荐：无限重试
handler := func(ctx context.Context, channel string, message string) error {
    for {
        if err := process(message); err != nil {
            continue // 无限循环
        }
        break
    }
    return nil
}
```

### 4. 性能反模式

```go
// ❌ 不推荐：频繁创建PubSub实例
func publishMessage(message string) {
    pubsub := NewPubSub(client) // 每次都创建新实例
    defer pubsub.Close()
    pubsub.Publish(ctx, "channel", message)
}

// ❌ 不推荐：订阅过多频道
channels := make([]string, 1000)
for i := 0; i < 1000; i++ {
    channels[i] = fmt.Sprintf("channel:%d", i)
}
// 单个订阅者订阅太多频道会影响性能
pubsub.Subscribe(channels, handler)
```

## 🛠️ 最佳实践

### 1. 连接管理

```go
// ✅ 推荐：连接池模式
type PubSubManager struct {
    publishers  []*PubSub
    subscribers []*PubSub
    currentPub  int32
    currentSub  int32
}

func NewPubSubManager(client *redis.Client, pubCount, subCount int) *PubSubManager {
    manager := &PubSubManager{
        publishers:  make([]*PubSub, pubCount),
        subscribers: make([]*PubSub, subCount),
    }
    
    // 创建发布者池
    for i := 0; i < pubCount; i++ {
        manager.publishers[i] = NewPubSub(client, PubSubConfig{
            Namespace:     "pool",
            EnableLogging: false, // 发布者关闭日志
        })
    }
    
    // 创建订阅者池
    for i := 0; i < subCount; i++ {
        manager.subscribers[i] = NewPubSub(client, PubSubConfig{
            Namespace:     "pool",
            EnableLogging: true,
        })
    }
    
    return manager
}

func (pm *PubSubManager) GetPublisher() *PubSub {
    index := atomic.AddInt32(&pm.currentPub, 1) % int32(len(pm.publishers))
    return pm.publishers[index]
}
```

### 2. 消息去重

```go
// ✅ 推荐：基于消息ID的去重机制
type MessageProcessor struct {
    processed sync.Map // 已处理消息ID
    ttl       time.Duration
}

func (mp *MessageProcessor) ProcessMessage(message MessageWithID) error {
    // 检查是否已处理
    if _, exists := mp.processed.LoadOrStore(message.ID, time.Now()); exists {
        return nil // 已处理，忽略
    }
    
    // 处理消息
    err := mp.handleMessage(message)
    
    // 定期清理过期的处理记录
    go mp.cleanupExpired()
    
    return err
}
```

### 3. 健康检查

```go
// ✅ 推荐：健康检查机制
func (pubsub *PubSub) HealthCheck() error {
    ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
    defer cancel()
    
    // 发布测试消息
    testChannel := "health:check:" + generateID()
    testMessage := "ping"
    
    received := make(chan bool, 1)
    
    handler := func(ctx context.Context, channel string, message string) error {
        if message == testMessage {
            select {
            case received <- true:
            default:
            }
        }
        return nil
    }
    
    subscriber, err := pubsub.Subscribe([]string{testChannel}, handler)
    if err != nil {
        return err
    }
    defer subscriber.Stop()
    
    if err := pubsub.Publish(ctx, testChannel, testMessage); err != nil {
        return err
    }
    
    select {
    case <-received:
        return nil // 健康
    case <-time.After(time.Second * 3):
        return fmt.Errorf("health check timeout")
    }
}
```

### 4. 监控指标

```go
// ✅ 推荐：集成监控指标
type MetricsCollector struct {
    publishCount    int64
    subscribeCount  int64
    errorCount      int64
    processTime     time.Duration
}

func (mc *MetricsCollector) WrapHandler(handler MessageHandler) MessageHandler {
    return func(ctx context.Context, channel string, message string) error {
        start := time.Now()
        atomic.AddInt64(&mc.subscribeCount, 1)
        
        err := handler(ctx, channel, message)
        
        if err != nil {
            atomic.AddInt64(&mc.errorCount, 1)
        }
        
        atomic.AddInt64((*int64)(&mc.processTime), int64(time.Since(start)))
        
        return err
    }
}

func (mc *MetricsCollector) GetMetrics() map[string]interface{} {
    return map[string]interface{}{
        "publish_count":    atomic.LoadInt64(&mc.publishCount),
        "subscribe_count":  atomic.LoadInt64(&mc.subscribeCount),
        "error_count":      atomic.LoadInt64(&mc.errorCount),
        "avg_process_time": time.Duration(atomic.LoadInt64((*int64)(&mc.processTime))) / time.Duration(atomic.LoadInt64(&mc.subscribeCount)),
    }
}
```

## 🔧 性能调优

### 1. 缓冲区配置

```go
// 高吞吐量场景
config := PubSubConfig{
    BufferSize:    1000,              // 大缓冲区
    MaxRetries:    1,                 // 减少重试
    EnableLogging: false,             // 关闭日志
    PingInterval:  time.Minute,       // 减少心跳频率
}

// 可靠性优先场景
config := PubSubConfig{
    BufferSize:    50,                // 小缓冲区，快速失败
    MaxRetries:    5,                 // 增加重试
    EnableLogging: true,              // 详细日志
    PingInterval:  time.Second * 15,  // 频繁心跳
}
```

### 2. 批量操作

```go
// ✅ 推荐：批量发布消息
func BatchPublish(pubsub *PubSub, messages []Message) error {
    pipe := pubsub.client.Pipeline()
    
    for _, msg := range messages {
        data, _ := json.Marshal(msg)
        pipe.Publish(ctx, msg.Channel, data)
    }
    
    _, err := pipe.Exec(ctx)
    return err
}
```

## 📊 性能基准

| 操作类型 | 吞吐量 | 延迟 | 内存使用 |
|----------|--------|------|----------|
| 简单发布 | ~80K ops/s | <0.5ms | 低 |
| JSON发布 | ~50K ops/s | <1ms | 中 |
| 模式匹配 | ~30K ops/s | <2ms | 中 |
| 请求响应 | ~10K ops/s | <10ms | 高 |

### 架构扩展建议

1. **水平扩展**：使用Redis Cluster进行分片
2. **高可用**：配置Redis哨兵模式
3. **消息持久化**：结合Redis Streams实现可靠消息传递
4. **跨语言支持**：标准化JSON消息格式
