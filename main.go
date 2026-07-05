package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"cert-live/internal/api"
	"cert-live/internal/auth"
	"cert-live/internal/config"
	"cert-live/internal/store"

	"golang.org/x/term"
)

//go:embed templates static
var assetsFS embed.FS

const version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		runServer()
		return
	}
	switch args[0] {
	case "serve":
		runServer()
	case "reset-admin":
		runResetAdmin(args[1:])
	case "version", "-v", "--version":
		fmt.Println("cert-live", version)
	case "help", "-h", "--help":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", args[0])
		printHelp()
		os.Exit(2)
	}
}

func printHelp() {
	fmt.Println("cert-live —— SSL 证书 + HTTP 健康监控")
	fmt.Println()
	fmt.Println("用法:")
	fmt.Println("  cert-live                 启动服务（同 serve）")
	fmt.Println("  cert-live serve           启动 HTTP 服务")
	fmt.Println("  cert-live reset-admin     重置登录账号 / 密码")
	fmt.Println("  cert-live version         打印版本号")
	fmt.Println("  cert-live help            显示本帮助")
}

// runServer 正常启动服务（原 main 逻辑）。
func runServer() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	auth.Configure(cfg.SessionKey)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.EnsureSchema(); err != nil {
		log.Fatalf("ensure schema: %v", err)
	}
	if err := st.EnsureLogin(cfg.AdminUser, cfg.AdminPass); err != nil {
		log.Fatalf("ensure login: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	srv := api.New(cfg, st, assetsFS)
	srv.StartScheduler(ctx)

	// 起一个 goroutine 等 Ctrl+C / kill 信号:ctx cancel 后调用 Shutdown,
	// 让 srv.Run 里的 ListenAndServe 主动返回,主 goroutine 才能退出。
	// 不然 signal.NotifyContext 接管了信号,默认杀进程行为被覆盖,程序卡死。
	go func() {
		<-ctx.Done()
		log.Printf("收到退出信号,正在关闭 HTTP 服务...")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			log.Printf("shutdown 错误: %v", err)
		}
	}()

	log.Printf("cert-live 启动于 http://localhost:%s  模式=%s  账号: %s",
		cfg.AppPort, cfg.GinMode, cfg.AdminUser)
	if err := srv.Run(":" + cfg.AppPort); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	log.Printf("已退出")
}

// runResetAdmin 子命令：重置登录账号和密码。
//
//	./cert-live reset-admin                 # 交互式：提示输入
//	./cert-live reset-admin <user> <pass>   # 非交互式：直接传
func runResetAdmin(args []string) {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.EnsureSchema(); err != nil {
		log.Fatalf("ensure schema: %v", err)
	}

	var user, pass string
	switch len(args) {
	case 0:
		// 交互式：密码不回显（term.ReadPassword 内部处理，不动 raw 模式）
		fmt.Print("新账号: ")
		fmt.Scanln(&user)
		if strings.TrimSpace(user) == "" {
			log.Fatalf("账号不能为空")
		}
		p1, err := readPasswordSilent("新密码: ")
		if err != nil {
			log.Fatalf("读取密码失败: %v", err)
		}
		p2, err := readPasswordSilent("确认密码: ")
		if err != nil {
			log.Fatalf("读取密码失败: %v", err)
		}
		if p1 != p2 {
			log.Fatalf("两次输入的密码不一致")
		}
		user = strings.TrimSpace(user)
		pass = p1
	case 2:
		user = strings.TrimSpace(args[0])
		pass = args[1]
	default:
		fmt.Fprintln(os.Stderr, "用法:")
		fmt.Fprintln(os.Stderr, "  交互式:    ./cert-live reset-admin")
		fmt.Fprintln(os.Stderr, "  非交互式:  ./cert-live reset-admin <user> <pass>")
		os.Exit(2)
	}
	if user == "" || pass == "" {
		log.Fatalf("账号和密码都不能为空")
	}
	if err := validateCredentials(user, pass); err != nil {
		log.Fatalf("校验失败: %v", err)
	}

	hash, err := auth.HashPassword(pass)
	if err != nil {
		log.Fatalf("哈希失败: %v", err)
	}
	if err := st.SetSetting("login_user", user); err != nil {
		log.Fatalf("写入账号失败: %v", err)
	}
	if err := st.SetSetting("login_password", hash); err != nil {
		log.Fatalf("写入密码失败: %v", err)
	}
	fmt.Printf("已重置: 账号=%s（bcrypt 哈希已存）\n", user)
}

// validateCredentials 校验账号密码复杂度：
//   - 账号 ≥ 5 字符；密码 ≥ 6 字符
//   - 只允许可打印 ASCII（0x20~0x7E），拒绝汉字、emoji、控制字符
func validateCredentials(user, pass string) error {
	if len([]rune(user)) < 5 {
		return fmt.Errorf("账号长度不能少于 5 个字符")
	}
	if len([]rune(pass)) < 6 {
		return fmt.Errorf("密码长度不能少于 6 个字符")
	}
	if !isPrintableASCII(user) {
		return fmt.Errorf("账号只能包含英文字母、数字、英文符号（不能有汉字 / emoji）")
	}
	if !isPrintableASCII(pass) {
		return fmt.Errorf("密码只能包含英文字母、数字、英文符号（不能有汉字 / emoji）")
	}
	return nil
}

// isPrintableASCII 检查字符串是否全部落在可打印 ASCII 范围 (0x20 ~ 0x7E)。
func isPrintableASCII(s string) bool {
	for _, r := range s {
		if r < 0x20 || r > 0x7E {
			return false
		}
	}
	return true
}

// readPasswordSilent 提示并静默读取密码（不回显）。
// 用 term.ReadPassword，不进 raw 模式，终端永远不可能卡死。
// 非 TTY（管道 / 重定向）退化为按行读。
func readPasswordSilent(prompt string) (string, error) {
	fmt.Print(prompt)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		// 非 TTY：按行读
		var s string
		_, err := fmt.Scanln(&s)
		return s, err
	}
	b, err := term.ReadPassword(fd)
	fmt.Println()
	return string(b), err
}