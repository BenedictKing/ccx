# CCX Makefile

GREEN=\033[0;32m
YELLOW=\033[0;33m
NC=\033[0m

.PHONY: help install dev run build clean frontend-dev frontend-build embed-frontend desktop-dev desktop-build

help:
	@echo "$(GREEN)CCX - 可用命令:$(NC)"
	@echo ""
	@echo "$(YELLOW)环境准备:$(NC)"
	@echo "  make install        - 安装所有依赖（前端 + 后端 + 桌面端）"
	@echo ""
	@echo "$(YELLOW)开发:$(NC)"
	@echo "  make dev            - Go 后端热重载开发(不含前端)"
	@echo "  make run            - 构建前端并运行 Go 后端"
	@echo "  make frontend-dev   - 前端开发服务器"
	@echo "  make desktop-dev    - 构建 CCX 核心并启动桌面外壳开发模式"
	@echo ""
	@echo "$(YELLOW)构建:$(NC)"
	@echo "  make build          - 构建前端并编译 Go 后端"
	@echo "  make desktop-build  - 构建前端、Go 后端和桌面外壳"
	@echo "  make frontend-build - 仅构建前端"
	@echo "  make clean          - 清理构建文件"
	@echo ""
	@echo "$(YELLOW)测试:$(NC)"
	@echo "  make test-all       - 运行所有测试（前端 + Go 后端）"
	@echo "  make test-frontend  - 仅运行前端测试"
	@echo "  make test-backend   - 仅运行 Go 后端测试"

install:
	@echo "$(GREEN)📦 安装前端依赖...$(NC)"
	@cd frontend && bun install
	@echo "$(GREEN)📦 安装桌面前端依赖...$(NC)"
	@cd desktop/frontend && bun install
	@echo "$(GREEN)📦 下载 Go 后端依赖...$(NC)"
	@cd backend-go && go mod download
	@echo "$(GREEN)📦 下载桌面端 Go 依赖...$(NC)"
	@cd desktop && go mod download
	@if ! command -v wails3 &> /dev/null; then \
		echo "$(GREEN)📦 安装 Wails 3 CLI...$(NC)"; \
		go install github.com/wailsapp/wails/v3/cmd/wails3@latest; \
	else \
		echo "$(GREEN)✅ Wails 3 已安装，跳过$(NC)"; \
	fi
	@if ! command -v air &> /dev/null; then \
		echo "$(GREEN)📦 安装 Air 热重载工具...$(NC)"; \
		go install github.com/air-verse/air@latest; \
	else \
		echo "$(GREEN)✅ Air 已安装，跳过$(NC)"; \
	fi
	@echo "$(GREEN)✅ 所有依赖安装完成$(NC)"

dev:
	@echo "$(GREEN)🚀 启动前后端开发模式...$(NC)"
	@cd frontend && bun run dev &
	@cd backend-go && $(MAKE) dev

run: embed-frontend
	@cd backend-go && $(MAKE) run

build: embed-frontend
	@cd backend-go && $(MAKE) build

desktop-dev: build
	@echo "$(GREEN)启动桌面外壳开发模式...$(NC)"
	@cd desktop && wails3 task dev

desktop-build: build
	@echo "$(GREEN)构建桌面外壳...$(NC)"
	@cd desktop && wails3 task package

embed-frontend:
	@echo "$(GREEN)📦 构建前端...$(NC)"
	@cd frontend && bun run build
	@echo "$(GREEN)📋 嵌入前端到 Go 后端...$(NC)"
	@rm -rf backend-go/frontend/dist
	@mkdir -p backend-go/frontend/dist
	@cp -r frontend/dist/* backend-go/frontend/dist/

clean:
	@cd backend-go && $(MAKE) clean
	@rm -rf frontend/dist
	@rm -rf desktop/bin desktop/dist desktop/frontend/dist desktop/.task

frontend-dev:
	@cd frontend && bun run dev

frontend-build:
	@cd frontend && bun run build

# ─── 测试 ──────────────────────────────────────────

test-all: test-frontend test-backend
	@echo "$(GREEN)✅ 全部测试完成$(NC)"

test-frontend:
	@echo "$(GREEN)🧪 运行前端测试...$(NC)"
	@cd frontend && npm test

test-backend:
	@echo "$(GREEN)🧪 运行 Go 后端测试...$(NC)"
	@cd backend-go && GOPROXY=https://goproxy.cn,direct go test ./... -count=1
