package config

import (
	"fmt"
	"log"

	"github.com/spf13/viper"
)

type Config struct {
	ServerName string
	ServerPort string

	DBHost          string
	DBPort          string
	DBUser          string
	DBPassword      string
	DBName          string
	MaxIdleConns    int
	MaxOpenConns    int
	ConnMaxLifetime int

	RedisHost     string
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	JWTSecret    string
	TokenTTLDays int // Token 统一有效期（天），JWT 与 Redis 共用

	MinioEndpoint       string // MinIO 服务端点（内网）
	MinioPublicEndpoint string // MinIO 公网访问端点
	MinioAccessKey      string // MinIO 访问密钥
	MinioSecretKey      string // MinIO 密钥
	MinioBucket         string // MinIO 存储桶名称
	MinioUseSSL         bool   // MinIO 是否启用 SSL

	RabbitMQHost     string
	RabbitMQPort     string
	RabbitMQUser     string
	RabbitMQPassword string
}

func InitConfig() *Config {
	v := viper.New()
	//默认情况（部署环境）
	//启动项
	v.SetDefault("SERVER_NAME", "LikeBiliStation")
	v.SetDefault("SERVER_PORT", ":8080")
	//数据库Mysql
	v.SetDefault("DB_HOST", "127.0.0.1")
	v.SetDefault("DB_PORT", "0721")
	v.SetDefault("DB_USER", "root")
	v.SetDefault("DB_PASSWORD", "123456")
	v.SetDefault("DB_NAME", "likebili")
	v.SetDefault("MAX_IDLE_CONNS", 10)
	v.SetDefault("MAX_OPEN_CONNS", 100)
	v.SetDefault("CONN_MAX_LIFETIME", 3600)
	//Redis
	v.SetDefault("REDIS_HOST", "192.168.11.100")
	v.SetDefault("REDIS_ADDR", "6379")
	v.SetDefault("REDIS_PASSWORD", "123456")
	v.SetDefault("REDIS_DB", "0")
	//JWT
	v.SetDefault("JWT_SECRET", "likebili-secret-key")
	v.SetDefault("TOKEN_TTL_DAYS", 7)
	//MinIO
	v.SetDefault("MINIO_ENDPOINT", "192.168.11.100:9000")
	v.SetDefault("MINIO_PUBLIC_ENDPOINT", "192.168.11.100:9000")
	v.SetDefault("MINIO_ACCESS_KEY", "minioadmin")
	v.SetDefault("MINIO_SECRET_KEY", "minioadmin")
	v.SetDefault("MINIO_BUCKET", "likebili")
	v.SetDefault("MINIO_USE_SSL", false)
	//rabbitmq
	v.SetDefault("RABBITMQ_HOST", "192.168.11.100")
	v.SetDefault("RABBITMQ_PORT", "5672")
	v.SetDefault("RABBITMQ_USER", "rabbit")
	v.SetDefault("RABBITMQ_PASSWORD", "rabbitPassword")
	//配置文件名
	v.SetConfigName("config")
	//配置文件后缀
	v.SetConfigType("yaml")
	//配置文件地址
	v.AddConfigPath("./pkg/config")

	v.AutomaticEnv()
	//读取配置文件
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			log.Printf("Warning: config file error: %v", err)
		}
	}

	cfg := &Config{
		ServerName:          v.GetString("SERVER_NAME"),
		ServerPort:          v.GetString("SERVER_PORT"),
		DBHost:              v.GetString("DB_HOST"),
		DBPort:              v.GetString("DB_PORT"),
		DBUser:              v.GetString("DB_USER"),
		DBPassword:          v.GetString("DB_PASSWORD"),
		DBName:              v.GetString("DB_NAME"),
		MaxIdleConns:        v.GetInt("MAX_IDLE_CONNS"),
		MaxOpenConns:        v.GetInt("MAX_OPEN_CONNS"),
		ConnMaxLifetime:     v.GetInt("CONN_MAX_LIFETIME"),
		RedisHost:           v.GetString("REDIS_HOST"),
		RedisAddr:           v.GetString("REDIS_ADDR"),
		RedisPassword:       v.GetString("REDIS_PASSWORD"),
		RedisDB:             v.GetInt("REDIS_DB"),
		JWTSecret:           v.GetString("JWT_SECRET"),
		TokenTTLDays:        v.GetInt("TOKEN_TTL_DAYS"),
		MinioEndpoint:       v.GetString("MINIO_ENDPOINT"),
		MinioPublicEndpoint: v.GetString("MINIO_PUBLIC_ENDPOINT"),
		MinioAccessKey:      v.GetString("MINIO_ACCESS_KEY"),
		MinioSecretKey:      v.GetString("MINIO_SECRET_KEY"),
		MinioBucket:         v.GetString("MINIO_BUCKET"),
		MinioUseSSL:         v.GetBool("MINIO_USE_SSL"),
		RabbitMQHost:        v.GetString("RABBITMQ_HOST"),
		RabbitMQPort:        v.GetString("RABBITMQ_PORT"),
		RabbitMQUser:        v.GetString("RABBITMQ_USER"),
		RabbitMQPassword:    v.GetString("RABBITMQ_PASSWORD"),
	}

	return cfg
}
func (c *Config) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
}

func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%s",
		c.RedisHost, c.RedisAddr)
}
