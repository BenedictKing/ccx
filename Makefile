# CCX Makefile

GREEN=\033[0;32m
YELLOW=\033[0;33m
NC=\033[0m

.PHONY: help install dev run build clean frontend-dev frontend-build embed-frontend desktop-dev desktop-build container-verify generate-preset-manifest benchmark-update benchmark-update-dry benchmark-chart

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
	@echo "  make container-verify - 在 Apple Container 中执行隔离验证"
	@echo "  make frontend-build - 仅构建前端"
	@echo "  make clean          - 清理构建文件"
	@echo ""
	@echo "$(YELLOW)工具:$(NC)"
	@echo "  make generate-preset-manifest - 生成预设清单"
	@echo "  make benchmark-update         - 更新模型能力基准数据并生成多来源图表"
	@echo "  make benchmark-update-dry     - 预览基准数据变更（不写入）"
	@echo "  make benchmark-chart          - 生成能力-成本边界曲线"
	@echo ""
	@echo "$(YELLOW)Benchmark 数据源参数:$(NC)"
	@echo "  make benchmark-update ARTIFICIAL_ANALYSIS_API_KEY=xxx - 传入 Artificial Analysis API key"
	@echo "  未传 key 时自动跳过 Artificial Analysis，其余源照常更新"

install:
	@echo "$(GREEN)📦 安装前端依赖...$(NC)"
	@cd frontend && bun install
	@echo "$(GREEN)📦 安装桌面前端依赖...$(NC)"
	@cd desktop/frontend && bun install
	@echo "$(GREEN)📦 下载 Go 后端依赖...$(NC)"
	@cd backend-go && go mod download
	@echo "$(GREEN)📦 下载桌面端 Go 依赖...$(NC)"
	@cd desktop && go mod download
	@echo "$(GREEN)📦 安装 Wails 3 CLI...$(NC)"
	@bash ./scripts/install-wails3.sh
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

container-verify:
	@bash scripts/container-verify.sh

desktop-dev: build
	@echo "$(GREEN)启动桌面外壳开发模式...$(NC)"
	@cd desktop && wails3 task dev

desktop-build: build
	@echo "$(GREEN)构建桌面外壳...$(NC)"
	@cd desktop && wails3 task package

FRONTEND_SENTINEL=backend-go/frontend/dist/.build-sentinel
FRONTEND_SOURCES=$(shell find frontend/src frontend/public frontend/index.html frontend/vite.config.ts frontend/tsconfig.json frontend/tsconfig.app.json -type f 2>/dev/null)

embed-frontend: $(FRONTEND_SENTINEL)

$(FRONTEND_SENTINEL): $(FRONTEND_SOURCES)
	@bash scripts/embed-frontend.sh
	@touch $(FRONTEND_SENTINEL)

clean:
	@cd backend-go && $(MAKE) clean
	@rm -rf frontend/dist
	@rm -rf desktop/bin desktop/dist desktop/frontend/dist desktop/.task

frontend-dev:
	@cd frontend && bun run dev

frontend-build:
	@cd frontend && bun run build

generate-preset-manifest:
	@node scripts/generate-preset-manifest.mjs

benchmark-update:
	@node scripts/update-benchmark-data.mjs

benchmark-update-dry:
	@node scripts/update-benchmark-data.mjs --dry-run

benchmark-chart:
	@node scripts/generate-benchmark-chart.mjs
