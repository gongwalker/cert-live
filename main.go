package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"cert-live/internal/api"
	"cert-live/internal/auth"
	"cert-live/internal/config"
	"cert-live/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// 注入 cookie 签名密钥
	auth.Configure(cfg.SessionKey)

	// 打开数据库、初始化 schema、首次启动写入管理员
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.EnsureSchema(); err != nil {
		log.Fatalf("ensure schema: %v", err)
	}
	if err := st.EnsureAdmin(cfg.AdminUser, cfg.AdminPass); err != nil {
		log.Fatalf("ensure admin: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	srv := api.New(cfg, st)
	srv.StartScheduler(ctx)
	srv.StartNotifyScheduler(ctx)

	log.Printf("cert-live 启动于 http://localhost:%s  模式=%s  账号: %s",
		cfg.AppPort, cfg.GinMode, cfg.AdminUser)
	if err := srv.Run(":" + cfg.AppPort); err != nil {
		log.Fatal(err)
	}
}