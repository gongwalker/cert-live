package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// Config 全局配置，全部来自环境变量（可由 .env 注入）
type Config struct {
	AppPort    string // 服务端口
	GinMode    string // gin 模式: debug / release
	SessionKey string // 登录 cookie 签名密钥（HMAC-SHA256）

	AdminUser string // 首次启动写入 DB 的管理员账号
	AdminPass string // 首次启动写入 DB 的管理员密码

	DBPath string // SQLite 单文件路径
}

// Load 读取环境变量；缺失项使用默认值
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		AppPort:    getenv("APP_PORT", "8080"),
		GinMode:    getenv("GIN_MODE", "debug"),
		SessionKey: getenv("SESSION_SECRET", ""),
		AdminUser:  getenv("ADMIN_USER", "admin"),
		AdminPass:  getenv("ADMIN_PASS", ""),
		DBPath:     getenv("DB_PATH", "./data/certlive.db"),
	}
	if cfg.SessionKey == "" {
		return nil, fmt.Errorf("SESSION_SECRET 必须设置（参考 .env.example）")
	}
	if cfg.AdminUser == "" || cfg.AdminPass == "" {
		return nil, fmt.Errorf("ADMIN_USER 和 ADMIN_PASS 必须设置")
	}
	return cfg, nil
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}