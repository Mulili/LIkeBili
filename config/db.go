package config

import (
	"LikeBili/global"
	"log"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func initDB() {
	dsn := AppConfig.Database.DSN
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("错误！无法找到数据库：%v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("错误！无法获取到数据库对象：%v", err)
	}
	sqlDB.SetMaxOpenConns(AppConfig.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(AppConfig.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(AppConfig.Database.ConnMaxLifetime * time.Second)

	//导入到全局
	global.Db = db

}
