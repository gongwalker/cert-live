APP        := cert-live
PORT       := 8080
PID_FILE   := .run/$(APP).pid
LOG_FILE   := logs/app.log
# Docker Hub 用户名(改成你自己的);VERSION 优先用 git tag,失败则退到 commit hash
DOCKER_USER   ?= gongwen
VERSION       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# buildx builder 名,使用 docker-container driver(支持多架构)。OrbStack/Docker Desktop 默认的 docker driver 不支持
BUILDX_BUILDER ?= cert-live-builder

.PHONY: help build run start stop restart status clean tidy reset-admin docker-image docker-push docker-buildx-init

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

docker-image: ## 本地构建镜像(单架构,不推送)。可覆盖:make docker-image DOCKER_USER=foo VERSION=v1
	docker build \
		-t $(DOCKER_USER)/$(APP):latest \
		-t $(DOCKER_USER)/$(APP):$(VERSION) \
		.

docker-buildx-init: ## 创建支持多架构的 buildx builder(docker-container driver,只需一次)
	@docker buildx inspect $(BUILDX_BUILDER) >/dev/null 2>&1 || \
		docker buildx create --name $(BUILDX_BUILDER) --driver docker-container --use
	@docker buildx use $(BUILDX_BUILDER)
	@echo "buildx builder 就绪: $(BUILDX_BUILDER) (driver=docker-container)"

docker-push: docker-buildx-init ## 多架构构建并推送到 Docker Hub(amd64 + arm64)。需先 docker login
	docker buildx build \
		--builder $(BUILDX_BUILDER) \
		--platform linux/amd64,linux/arm64 \
		-t $(DOCKER_USER)/$(APP):latest \
		-t $(DOCKER_USER)/$(APP):$(VERSION) \
		--push .