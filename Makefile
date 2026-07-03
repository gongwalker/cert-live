APP      := cert-live
PORT     := 8080
PID_FILE := .run/$(APP).pid
LOG_FILE := logs/app.log

.PHONY: help build run start stop restart status clean tidy reset-admin

help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

build: ## 编译二进制
	go build -o $(APP) .

run: build ## 前台运行（Ctrl+C 退出，日志直接输出）
	./$(APP)

start: build ## 后台启动（带 PID 和日志）
	@mkdir -p .run logs
	@if [ -f $(PID_FILE) ] && kill -0 $$(cat $(PID_FILE)) 2>/dev/null; then \
		echo "已在运行, pid=$$(cat $(PID_FILE))"; else \
		nohup ./$(APP) > $(LOG_FILE) 2>&1 & \
		echo $$! > $(PID_FILE); \
		echo "已启动, pid=$$(cat $(PID_FILE)), 日志=$(LOG_FILE)"; \
		sleep 0.5; \
		awk '/启动于/' $(LOG_FILE) | tail -1; \
	fi

stop: ## 停止后台进程
	@if [ -f $(PID_FILE) ]; then \
		pid=$$(cat $(PID_FILE)); \
		if kill -0 $$pid 2>/dev/null; then \
			kill $$pid && echo "已停止, pid=$$pid"; \
		else \
			echo "进程不存在($$pid), 清理 PID 文件"; \
		fi; \
		rm -f $(PID_FILE); \
	else \
		echo "未运行(无 PID 文件)"; \
	fi

restart: stop start ## 重启

status: ## 查看运行状态
	@if [ -f $(PID_FILE) ] && kill -0 $$(cat $(PID_FILE)) 2>/dev/null; then \
		echo "运行中, pid=$$(cat $(PID_FILE)), 端口=$(PORT)"; \
	else \
		echo "未运行"; \
	fi

clean: ## 清理编译产物和运行时文件
	rm -f $(APP)
	rm -rf .run logs

tidy: ## 整理依赖
	go mod tidy

reset-admin: ## 重置登录账号 / 密码（交互式；非交互式用 make reset-admin A="user pass"）
	@if [ -x $(APP) ]; then \
		./$(APP) reset-admin $(A); \
	else \
		echo "二进制 $(APP) 不存在，请先 make build"; \
		exit 1; \
	fi