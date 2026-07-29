package database

import (
	"LikeBili/pkg/config"
	"context"
	"log"

	"github.com/redis/go-redis/v9"
)

func InitRedis(cfg *config.Config) *redis.Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr(),
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	//用于验证连接可用性
	c := context.Background()
	if err := rdb.Ping(c).Err(); err != nil {
		log.Fatalf("连接至Redis失败: %v", err)
	}

	return rdb
}
