# --- 阶段 1: 准备 Bun 运行时（使用构建平台架构，避免 QEMU）---
FROM --platform=$BUILDPLATFORM oven/bun:alpine AS bun-runtime

# --- 阶段 2: 构建阶段（使用构建平台架构，Go 交叉编译目标平台）---
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG VERSION
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

RUN apk add --no-cache git make libstdc++ libgcc

COPY --from=bun-runtime /usr/local/bin/bun /usr/local/bin/bun
COPY --from=bun-runtime /usr/local/bin/bunx /usr/local/bin/bunx
ENV PATH="/usr/local/bin:${PATH}"

# 先复制依赖清单，安装依赖（源码变动不会触发重装）
COPY frontend/package.json frontend/bun.lock ./frontend/
RUN --mount=type=cache,id=bun-cache,target=/root/.bun/install/cache \
    cd frontend && bun install --frozen-lockfile

COPY backend-go/go.mod backend-go/go.sum ./backend-go/
RUN --mount=type=cache,id=go-mod-cache,target=/go/pkg/mod \
    cd backend-go && go mod download

# 复制全部源码（变动频繁，放在依赖层之后）
COPY Makefile ./
COPY frontend/ ./frontend/
COPY backend-go/ ./backend-go/
COPY VERSION ./

# 构建：交叉编译目标平台，利用 Go build cache
RUN --mount=type=cache,id=go-build-cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} CGO_ENABLED=0 \
    VERSION=${VERSION} make build

# --- 阶段 3: 运行时 ---
FROM alpine:latest AS runtime

WORKDIR /app

RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /src/dist/ccx-go /app/ccx

RUN mkdir -p /app/.config/backups /app/logs

ENV TZ=Asia/Shanghai

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:3000/health || exit 1

CMD ["/app/ccx"]
