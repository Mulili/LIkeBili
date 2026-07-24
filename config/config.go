package config

import (
	"log"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	App struct {
		AppName string
		Port    string
	}
	Database struct {
		DSN             string
		MaxIdleConns    int
		MaxOpenConns    int
		ConnMaxLifetime time.Duration
	}
}

var AppConfig *Config

func InitConfig() {
	//配置文件名
	viper.SetConfigName("config")
	//配置文件后缀
	viper.SetConfigType("yaml")
	//配置文件地址
	viper.AddConfigPath("./config")
	//读取配置文件
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("错误！无法读取到配置文件：%v", err)
	}
	AppConfig = &Config{}
	if err := viper.Unmarshal(AppConfig); err != nil {
		log.Fatalf("错误！无法反序列化配置文件：%v", err)
	}

	initDB()
}
