// Package transcode 提供视频转码的进度广播（发布-订阅）功能。
//
// 解决的问题：转码是后台异步任务，可能持续几分钟。前端想实时看到"转码 30% / 完成"，
// 但普通 HTTP 是"一问一答"（有请求才有响应），服务器没法主动把进度推给前端。
//
// 本包用"发布-订阅（Pub/Sub）"模式解决：
//   - 订阅 Subscribe：前端连接时，广播台给它分配一个专属"信箱"（Go channel）
//   - 发布 Publish：转码协程产生进度时，往所有订阅者的信箱里投递一条消息
//   - 退订 Unsubscribe：前端断开时，关闭自己的信箱，并通知广播台移除它
//
// 需要的前置知识：
//   - goroutine：`go 函数()` 开一个并发执行单元（轻量线程），不会阻塞当前代码
//   - channel：协程间传递数据的管道。`ch <- v` 送进管道，`v := <-ch` 取出。
//     无缓冲管道：发送会阻塞，直到有人接收；有缓冲管道（本文件容量 32）：
//     缓冲区没满时发送不阻塞，满了再发就会阻塞（或者走 select default 丢弃）。
//
// 订阅方的典型用法：
//
//	ch := broker.Subscribe(videoID)        // ① 领一个专属信箱
//	go func() {                            // ② 开一个协程专门等消息
//		for update := range ch {            // ③ 一直读，直到信箱被 close
//			// 把 update 通过 WebSocket/SSE 转发给前端
//		}
//	}()
//	// 请求结束时（通常配 defer）：
//	broker.Unsubscribe(videoID, ch)        // ④ 退订+关信箱 → ③ 的 range 退出 → 协程不泄漏
package transcode

import (
	"LikeBili/pkg/logger"
	"sync"

	"go.uber.org/zap"
)

// ProgressUpdate 一条进度消息（DTO），会被序列化成 JSON 发给前端。
// 字段上的 json tag 是序列化用的名字，要和前端约定一致。
type ProgressUpdate struct {
	VideoID  uint   `json:"video_id"`            // 哪个视频的进度
	Status   uint8  `json:"status"`              // 转码状态（枚举由业务约定，如 1转码中 2完成 3失败）
	Progress uint8  `json:"progress"`            // 0-100 的百分比
	Quality  string `json:"quality,omitempty"`   // 当前转出的清晰度档位（可选）
	ErrorMsg string `json:"error_msg,omitempty"` // 失败原因（可选）
}

// ProgressBroker 进度广播台（"中央调度表"）。
//
// 数据结构是两层 map：
//
//	subs[视频ID] → 该视频的所有订阅者集合
//	             → 每个订阅者一个 channel（信箱），值是 struct{} 占位
//
// 为什么内层用 map[chan]struct{} 而不是 []chan？
//   - 订阅/退订是随机的，map 增删都是 O(1)；slice 删中间元素要搬移，麻烦
//   - "这个通道还订着吗"用 map 查一下就知道，天然防重复登记
//
// 为什么值是 struct{}？
//   - struct{} 占 0 字节，纯粹"占位表示键存在"，是 Go 实现"集合(Set)"的惯用技巧：
//     我们只关心"有哪些 channel"，不关心它的值是什么
//
// 为什么每个订阅者一个独立 channel，而不是大家共用一个？
//   - 一个频道一条队列，谁慢谁自己堵自己；共用一条的话，一个慢订阅者会把所有进度都卡住
type ProgressBroker struct {
	mu   sync.RWMutex                              // 互斥锁：串行化对 subs 的读写
	subs map[uint]map[chan ProgressUpdate]struct{} // 视频ID → 订阅者集合
}

// NewProgressBroker 构造广播台。
// 必须显式初始化 subs：nil map 是只读的，往里写（p.subs[id][ch]=...）会 panic。
// 锁不用初始化：sync.RWMutex 的零值就是"未锁定"的可用状态。
// 注意：ProgressBroker 之后必须用指针（New 返回 *ProgressBroker），
// 千万别拷贝它——拷贝会把锁一起复制，两个副本共用一把锁，行为会错乱（Go 经典坑）。
func NewProgressBroker() *ProgressBroker {
	return &ProgressBroker{
		subs: make(map[uint]map[chan ProgressUpdate]struct{}),
	}
}

// Subscribe 订阅某个视频的进度，返回一个专属信箱（channel）。
//
// 返回的 channel 带缓冲（容量 32）：发布方投递时如果订阅方正忙于处理上一条，
// 消息先暂存在缓冲区，发布方不会被阻塞。
// 缓冲满了怎么办？见 Publish 的说明——丢弃新消息而不是阻塞。
func (p *ProgressBroker) Subscribe(videoID uint) chan ProgressUpdate {
	ch := make(chan ProgressUpdate, 32) // 每个订阅者独立信箱
	p.mu.Lock()                         // 要改 map，先拿锁
	defer p.mu.Unlock()

	// 首次订阅该视频时，内层集合还不存在 → 先建好（往 nil map 写会 panic）
	if p.subs[videoID] == nil {
		p.subs[videoID] = make(map[chan ProgressUpdate]struct{})
	}
	p.subs[videoID][ch] = struct{}{} // 把新信箱登记进集合
	return ch
}

// Unsubscribe 退订并关闭信箱。
// 订阅方不再需要进度时必须调用（通常配 defer），否则：
//   - 信箱永远留在 subs 里 → 内存泄漏
//   - 发布方还继续向它投递 → 白白浪费
//
// 本方法是幂等的：重复退订同一个信箱不会 panic（见下方 ok 判断）。
// 为什么不在这里清空缓冲？订阅方自己的 `for range ch` 会把缓冲里剩余的消息
// 读完再退出，close 只是发"没有新消息了"的结束信号。
func (p *ProgressBroker) Unsubscribe(videoID uint, ch chan ProgressUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 先确认这个信箱确实登记在案；不在（重复退订/从未订阅）就什么都不做，
	// 避免 close 一个已经关闭的 channel → panic
	if m := p.subs[videoID]; m != nil {
		if _, ok := m[ch]; ok {
			delete(m, ch) // 从集合里移除
			if len(m) == 0 {
				delete(p.subs, videoID) // 该视频没有订阅者了，清掉外层，省内存
			}
			close(ch) // 关信箱：订阅方的 for range 收到"结束"信号后退出，协程不泄漏
		}
	}
}

// Publish 向某个视频的所有订阅者广播一条进度。
//
// 广播策略：非阻塞投递。对每个信箱尝试 `ch <- update`，
// 如果对方缓冲已满（消费太慢），丢弃这条并记一条警告，而不是阻塞在这里。
//
// 为什么"丢弃"而不是"阻塞"？
//   - 进度是"最终状态"型数据：前端看到 30% 还是 40% 差别不大，最终要的是 100%，
//     丢几条中间进度无伤大雅
//   - 阻塞会让转码协程卡在发消息上，拖慢整个转码，因小失大
//
// 为什么锁要包住整个遍历循环？
//   - 锁外遍历 + 其他协程删元素 = "一边遍历一边改 map"，Go 检测到会直接 panic
//     （"concurrent map iteration and map write"）
func (p *ProgressBroker) Publish(update ProgressUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for ch := range p.subs[update.VideoID] { // 遍历该视频的所有订阅者信箱
		select {
		case ch <- update: // 投递成功
		default: // 缓冲已满，按策略丢弃
			logger.Warn("订阅者消费过慢，丢弃进度更新", zap.Uint("video_id", update.VideoID))
		}
	}
}
