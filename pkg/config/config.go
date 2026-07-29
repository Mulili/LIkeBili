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
	RedisTimeout  int

	JWTSecret string
	JWTExpire int
}

func InitConfig() *Config {
	v := viper.New()
	//默认情况（部署环境）
	//启动项
	v.SetDefault("SERVER_NAME", "LikeBiliStation")
	v.SetDefault("SERVER_PORT", "8080")
	//数据库Mysql
	v.SetDefault("DB_HOST", "127.0.0.1")
	v.SetDefault("DB_PORT", "721")
	v.SetDefault("DB_USER", "root")
	v.SetDefault("DB_PASSWORD", "123456")
	v.SetDefault("DB_NAME", "likebili")
	v.SetDefault("MAX_IDLE_CONNS", 10)
	v.SetDefault("MAX_OPEN_CONNS", 100)
	v.SetDefault("CONN_MAX_LIFETIME", 3600)
	//Redis
	v.SetDefault("REDIS_HOST", "localhost")
	v.SetDefault("REDIS_ADDR", "6379")
	v.SetDefault("REDIS_PASSWORD", "")
	v.SetDefault("REDIS_DB", "0")
	v.SetDefault("REDIS_TIMEOUT", 7)
	//JWT
	v.SetDefault("JWT_SECRET", "likebili-secret-key")
	v.SetDefault("JWT_EXPIRE", 72)
	//配置文件名
	v.SetConfigName("config")
	//配置文件后缀
	v.SetConfigType("yaml")
	//配置文件地址
	v.AddConfigPath("./config")

	v.AutomaticEnv()
	//读取配置文件
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			log.Printf("Warning: config file error: %v", err)
		}
	}

	cfg := &Config{
		ServerName:      v.GetString("SERVER_NAME"),
		ServerPort:      v.GetString("SERVER_PORT"),
		DBHost:          v.GetString("DB_HOST"),
		DBPort:          v.GetString("DB_PORT"),
		DBUser:          v.GetString("DB_USER"),
		DBPassword:      v.GetString("DB_PASSWORD"),
		DBName:          v.GetString("DB_NAME"),
		MaxIdleConns:    v.GetInt("MAX_IDLE_CONNS"),
		MaxOpenConns:    v.GetInt("MAX_OPEN_CONNS"),
		ConnMaxLifetime: v.GetInt("CONN_MAX_LIFETIME"),
		RedisHost:       v.GetString("REDIS_HOST"),
		RedisAddr:       v.GetString("REDIS_ADDR"),
		RedisPassword:   v.GetString("REDIS_PASSWORD"),
		RedisDB:         v.GetInt("REDIS_DB"),
		JWTSecret:       v.GetString("JWT_SECRET"),
		JWTExpire:       v.GetInt("JWT_EXPIRE"),
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
