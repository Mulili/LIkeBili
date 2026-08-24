package rabbitmq

import (
	"LikeBili/pkg/logger"
	"fmt"
	"log"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// queueTranscode 转码任务队列名（durable 持久化，broker 重启后消息不丢失）。
const (
	QueueTranscode = "video.transcode"
)

// TranscodeMessage 转码任务消息体：投递到 video.transcode 队列，消费端按 VideoID 执行转码。
type TranscodeMessage struct {
	VideoID uint `json:"video_id"`
}

// Config RabbitMQ 连接配置（由 main 装配时从环境变量读取）。
type Config struct {
	Host     string
	Port     string
	User     string
	Password string
}

// Client RabbitMQ 客户端：持有连接与 channel，负责发布消息与断线自动重连。
// 注意：amqp.Channel 非并发安全，所有对外方法（Publish/Close）与内部 connect
// 统一通过 mu 串行化，保证 conn/channel 字段的并发安全读写。
type Client struct {
	cfg     Config
	conn    *amqp.Connection
	channel *amqp.Channel
	mu      sync.Mutex    // 串行化 connect/Publish/Close，保护 conn/channel 的读写与替换
	done    chan struct{} // 关闭信号：Close 时 close(done)，通知 reconnectLoop 退出
	once    sync.Once     // 保证 Close 只执行一次，重复调用不 panic
}

// Init 建立 RabbitMQ 连接（拨号最多重试 5 次、每次间隔 2 秒）并启动断线自动重连。
func Init(cfg Config) (*Client, error) {
	c := &Client{cfg: cfg, done: make(chan struct{})}
	if err := c.connect(); err != nil {
		return nil, err
	}
	go c.reconnectLoop() // 后台 goroutine 监听连接断开 → 自动重连
	return c, nil
}

// connect 建立连接：拨号（带重试）→ 创建 channel → 声明转码队列。
// 加锁执行，保证重连时对 conn/channel 字段的覆盖替换是线程安全的。
func (c *Client) connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	dsn := fmt.Sprintf("amqp://%s:%s@%s:%s/", c.cfg.User, c.cfg.Password, c.cfg.Host, c.cfg.Port)

	// 拨号最多尝试 5 次，每次失败等 2 秒（应对 RabbitMQ 服务刚启动尚未就绪的场景）
	var conn *amqp.Connection
	var err error
	for i := 0; i < 5; i++ {
		conn, err = amqp.Dial(dsn)
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return fmt.Errorf("Method:rabbitmq.connect: dial: %w", err)
	}

	// 创建 channel（后续 Publish 都复用它）
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("Method:rabbitmq.connect: channel: %w", err)
	}
	// 声明持久化队列：durable=true 保证队列定义在 broker 重启后依然存在
	if _, err := ch.QueueDeclare(QueueTranscode, true, false, false, false, nil); err != nil {
		ch.Close()
		conn.Close()
		return fmt.Errorf("Method:rabbitmq.connect: channel: %w", err)
	}

	c.conn = conn
	c.channel = ch
	return nil
}

// reconnectLoop 断线自动重连：监听当前连接的关闭通知，断开后循环 connect 直到成功，
// 并把关闭监听重新挂到新连接上，回到监听状态。Close 时通过 done 信号退出。
func (c *Client) reconnectLoop() {
	// 注册关闭监听：amqp091-go 的 NotifyClose 把 receiver 追加到连接自身的
	// closes 列表并原样返回，连接关闭时关闭事件会推送到该 channel
	notify := make(chan *amqp.Error, 1)
	c.conn.NotifyClose(notify)

	for {
		select {
		case <-c.done:
			return // 主动 Close，退出重连循环
		case err, ok := <-notify:
			if !ok || err == nil {
				return // channel 被关闭（本地主动断开），无需重连
			}
			log.Printf("rabbitmq connection lost: %v, reconnecting...", err)
		reconnect:
			// 内层重连循环：connect 成功即跳出（用标签，避免 break 只跳出 select）
			for {
				select {
				case <-c.done:
					return
				default:
					if cerr := c.connect(); cerr == nil {
						log.Printf("rabbitmq reconnected")
						// 重连成功后把监听挂到新连接（新连接有自己的 closes 列表，无累积问题）
						c.conn.NotifyClose(notify)
						break reconnect
					}
					time.Sleep(2 * time.Second) // 重连失败等 2 秒再试
				}
			}
		}
	}
}

// Consume 消费指定队列：独立 channel + 逐条手动 ack，消息反序列化后交给 handler。
// 注意：必须用独立 channel，避免与 Publish 共用 c.channel 时互相阻塞（共用 mu）。
func (c *Client) Consume(queue string, handler func(body []byte) error) error {
	c.mu.Lock()
	ch, err := c.conn.Channel() // 从当前连接派生新 channel
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("Method:rabbitmq.Consume: %w", err)
	}
	defer ch.Close()
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		return fmt.Errorf("Method:rabbitmq.Consume: %w", err) // 与发布侧声明一致，幂等
	}
	// 一次只取一条，逐条处理
	if err := ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("Method:rabbitmq.Consume: %w", err)
	}
	// autoAck=false
	msgs, err := ch.Consume(queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("Method:rabbitmq.Consume: %w", err)
	}
	for m := range msgs {
		if err := handler(m.Body); err != nil {
			logger.Warn("转码消息处理失败", zap.Error(err)) // 不重投：失败状态已由 ProcessVideo 落库
		}
		_ = m.Ack(false) // 手动确认，防止未处理消息被重复投递
	}
	return nil // channel 关闭（断线）时返回
}

// Publish 向指定队列发布持久化消息（JSON 格式）。
// 加锁串行化 channel 的发布操作（amqp.Channel 非并发安全）；
// 连接断开时此处返回错误，由调用方决定降级策略（如丢弃或本地转码）。
func (c *Client) Publish(queue string, body []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.channel.Publish("", queue, false, false, amqp.Publishing{
		ContentType:  "application/json",
		Body:         body,
		DeliveryMode: amqp.Persistent, // 持久化消息：broker 重启不丢失
	}); err != nil {
		return fmt.Errorf("Method:rabbitmq.Publish: %w", err)
	}
	return nil
}

// Close 优雅关闭，幂等（sync.Once 保证仅执行一次）：
// 先 close(done) 通知重连循环退出，再加锁关闭 channel 与连接。
func (c *Client) Close() {
	c.once.Do(func() {
		close(c.done)
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.conn != nil {
			c.conn.Close()
		}
		if c.channel != nil {
			c.channel.Close()
		}
	})
}
