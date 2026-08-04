// Package logger 是一个基于 zap 的日志工具包。
//
// 为什么要做日志：程序运行时我们是"看不见"它内部的，出错了只能靠它自己告诉我们。
// 日志就是程序写给开发/运维看的话：什么时间、发生了什么级别的事件、具体内容是什么。
// 本包把 zap 封装成全局可用的 Logger，统一日志格式（JSON）、统一级别、自动脱敏。
package logger

import (
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger 是全局日志实例。
// 它由 Init 初始化一次（程序启动时调用），之后全项目都通过本包的
// Debug / Info / Warn / Error 四个函数打印日志。
var Logger *zap.Logger

// Init 根据传入的日志级别初始化全局 Logger，程序启动时调用一次即可。
//
// 参数 level 的取值与含义：
//   - "debug": 全部日志都输出（最详细，一般开发时用）
//   - "info" : 输出 info 及更严重的日志（默认值）
//   - "warn" : 只输出 warn 和 error
//   - "error": 只输出 error
//
// 工作原理：zap 把"怎么输出日志"拆成三个可组合的零件（见下方代码）：
//  1. 编码器（Encoder）：决定一条日志长什么样，这里用 JSON 编码器
//  2. 输出地（WriteSyncer）：决定日志写到哪，这里写到 stdout（终端）
//  3. 级别过滤（LevelEnabler）：决定哪些级别的日志允许通过
//
// 三个零件拼成 Core，再用 zap.New 包一层，就是完整的 *zap.Logger。
func Init(level string) {
	// 把字符串形式的级别翻译成 zap 认识的 Level 对象
	var lvl zapcore.Level
	switch level {
	case "debug":
		lvl = zapcore.DebugLevel
	case "info":
		lvl = zapcore.InfoLevel
	case "warn":
		lvl = zapcore.WarnLevel
	case "error":
		lvl = zapcore.ErrorLevel
	default:
		lvl = zapcore.InfoLevel // 传了奇怪的值就用 info 兜底
	}

	// 配置"日志长什么样"：生产环境默认配置 + 时间格式改成人类可读的 ISO8601
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	// 拼装三个零件 → Core → Logger
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig), // ① 编码器：输出 JSON
		zapcore.AddSync(os.Stdout),            // ② 输出地：标准输出
		lvl,                                   // ③ 级别过滤
	)

	Logger = zap.New(core)
}

// Debug 打印 debug 级别日志，用于开发期排查细节。
// 用法示例：logger.Debug("开始查询用户", zap.String("username", u))
func Debug(msg string, fields ...zap.Field) {
	Logger.Debug(sanitizeMsg(msg), sanitizeFields(fields)...)
}

// Info 打印 info 级别日志，用于记录正常流程的关键节点。
// 用法示例：logger.Info("用户注册成功", zap.Uint("userID", id))
func Info(msg string, fields ...zap.Field) {
	Logger.Info(sanitizeMsg(msg), sanitizeFields(fields)...)
}

// Warn 打印 warn 级别日志，用于记录不致命但值得注意的情况。
// 用法示例：logger.Warn("重试次数过多", zap.Int("retries", 3))
func Warn(msg string, fields ...zap.Field) {
	Logger.Warn(sanitizeMsg(msg), sanitizeFields(fields)...)
}

// Error 打印 error 级别日志，用于记录发生了错误。
// 推荐把原始错误传进来：logger.Error("注册失败", zap.Error(err))
func Error(msg string, fields ...zap.Field) {
	Logger.Error(sanitizeMsg(msg), sanitizeFields(fields)...)
}

// sensitiveFields 记录哪些字段属于敏感信息。
// 命中这些字段名的值，写日志时会替换成 [REDACTED]，防止密码、Token 等泄露到日志里。
var sensitiveFields = map[string]bool{
	"password":     true,
	"passwordhash": true,
	"token":        true,
	"jwt":          true,
	"secret":       true,
}

// sanitizeFields 逐个检查调用方传入的字段，把敏感字段的值替换成 [REDACTED]。
// 因为 logger 是全局统一出口，在这里做脱敏，就能保证全项目所有日志都不会泄露敏感值。
func sanitizeFields(fields []zap.Field) []zap.Field {
	result := make([]zap.Field, len(fields))
	for i, f := range fields {
		if sensitiveFields[f.Key] {
			// 命中敏感字段：只保留字段名，值换成占位符
			result[i] = zap.String(f.Key, "[REDACTED]")
		} else {
			// 正常字段原样保留
			result[i] = f
		}
	}
	return result
}

// sanitizeMsg 对日志消息正文做脱敏，目前针对 AMQP 连接串。
// 例如 amqp://user:pass@192.168.1.1:5672/ 会变成 amqp://[REDACTED]@192.168.1.1:5672/，
// 即只隐藏账号密码，保留主机和端口（方便定位连的是哪台机器）。
func sanitizeMsg(msg string) string {
	// 1. 找到 "amqp://" 出现的位置，没找到说明这条消息不涉敏，原样返回
	idx := strings.Index(msg, "amqp://")
	if idx < 0 {
		return msg
	}
	// 2. 在 "amqp://" 之后找 "@"，@ 之前就是 user:pass 部分
	rest := msg[idx+len("amqp://"):]
	atIdx := strings.Index(rest, "@")
	if atIdx < 0 {
		return msg // 没有 @ 说明格式不标准，直接返回
	}
	// 3. 拼接：amqp:// + [REDACTED] + @host:port/，把 user:pass 整段换成占位符
	return msg[:idx+len("amqp://")] + "[REDACTED]" + rest[atIdx:]
}
