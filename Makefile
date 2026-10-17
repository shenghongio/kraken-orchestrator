SHELL := /bin/bash

# ============================================================
#  基础变量
# ============================================================
APP        := kraken-orchestrator
MODULE     := github.com/shenghongio/kraken-orchestrator

BIN_DIR    := bin

# 三个可执行入口（按 AGENTS.md 约定）
CMDS := kraken-scheduler kraken-agent krakenctl

# Go 工具
GO         := go
GOFMT      := gofmt
GOIMPORTS  := goimports

# 编译参数
GOOS       ?= $(shell go env GOOS)
GOARCH     ?= $(shell go env GOARCH)
CGO_ENABLED ?= 0

# 版本/构建信息
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS    := -s -w \
              -X main.version=$(VERSION) \
              -X main.commit=$(COMMIT) \
              -X main.buildTime=$(BUILD_TIME)

# ============================================================
#  带时间戳的日志函数
#    $(call log, message)
#    $(call log_err, message)
# ============================================================
log      = @printf "[%s] %s\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$(1)"
log_err  = @printf "[%s] \033[31m[ERROR]\033[0m %s\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$(1)" >&2

# ============================================================
#  默认目标
# ============================================================
.DEFAULT_GOAL := help

.PHONY: all
all: fmt vet check test build

# ============================================================
#  代码格式化
# ============================================================
.PHONY: fmt
fmt:
	$(call log,运行 gofmt ...)
	@output=$$($(GOFMT) -l -w . 2>&1); \
	if [ -n "$$output" ]; then \
		printf "[%s] 已格式化以下文件:\n" "$$(date '+%Y-%m-%d %H:%M:%S')"; \
		echo "$$output" | sed 's/^/  /'; \
	else \
		printf "[%s] 所有文件格式正确\n" "$$(date '+%Y-%m-%d %H:%M:%S')"; \
	fi

# ============================================================
#  静态检查 (go vet)
# ============================================================
.PHONY: vet
vet:
	$(call log,运行 go vet ...)
	@$(GO) vet ./... 2>&1 | while read -r line; do \
		printf "[%s] %s\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$$line"; \
	done; \
	exit $${PIPESTATUS[0]}

# ============================================================
#  综合检查：格式 + vet + 编译检查
# ============================================================
.PHONY: check
check:
	$(call log,开始综合检查 (fmt + vet + build check))
	@$(MAKE) --no-print-directory fmt
	@$(MAKE) --no-print-directory vet
	$(call log,运行 go build ./... 检查编译)
	@$(GO) build ./... 2>&1 | while read -r line; do \
		printf "[%s] %s\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$$line"; \
	done; \
	status=$${PIPESTATUS[0]}; \
	if [ $$status -eq 0 ]; then \
		printf "[%s] \033[32m[PASS]\033[0m 全部检查通过\n" "$$(date '+%Y-%m-%d %H:%M:%S')"; \
	else \
		printf "[%s] \033[31m[FAIL]\033[0m 检查失败\n" "$$(date '+%Y-%m-%d %H:%M:%S')" >&2; \
		exit $$status; \
	fi

# ============================================================
#  测试
# ============================================================
.PHONY: test
test:
	$(call log,运行 go test ./...)
	@$(GO) test -race -count=1 -v ./... 2>&1 | while read -r line; do \
		printf "[%s] %s\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$$line"; \
	done; \
	exit $${PIPESTATUS[0]}

.PHONY: test-coverage
test-coverage:
	$(call log,运行带覆盖率的 go test ...)
	@$(GO) test -race -count=1 -coverprofile=coverage.out ./... 2>&1 | while read -r line; do \
		printf "[%s] %s\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$$line"; \
	done; \
	exit $${PIPESTATUS[0]}

# ============================================================
#  本地构建
# ============================================================
.PHONY: build
build:
	$(call log,开始本地构建 ($(GOOS)/$(GOARCH)))
	@mkdir -p $(BIN_DIR)
	@for cmd in $(CMDS); do \
		if [ -d "cmd/$$cmd" ]; then \
			printf "[%s]  构建 %-18s -> %s/$$cmd\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$$cmd" "$(BIN_DIR)"; \
			$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$$cmd ./cmd/$$cmd; \
		else \
			printf "[%s]  跳过 %-18s (cmd/%s 目录不存在)\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$$cmd" "$$cmd"; \
		fi; \
	done
	$(call log,本地构建完成)

# ============================================================
#  ARM64 详细编译
#    - build-arm64     : linux/arm64 （服务器部署常用）
#    - build-arm64-mac : darwin/arm64（Apple Silicon 本机）
#    - build-arm64-all : 两者都构建
#  使用 -v -x 输出详细编译过程，所有日志带时间戳
# ============================================================
.PHONY: build-arm64
build-arm64:
	@$(MAKE) --no-print-directory _build-arm64 GOOS=linux GOARCH=arm64 LABEL="linux/arm64"

.PHONY: build-arm64-mac
build-arm64-mac:
	@$(MAKE) --no-print-directory _build-arm64 GOOS=darwin GOARCH=arm64 LABEL="darwin/arm64"

.PHONY: build-arm64-all
build-arm64-all: build-arm64 build-arm64-mac

.PHONY: _build-arm64
_build-arm64:
	$(call log,===== ARM64 详细编译开始 [$(LABEL)] =====)
	$(call log,GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED))
	$(call log,LDFLAGS=$(LDFLAGS))
	@mkdir -p $(BIN_DIR)/$(GOOS)-$(GOARCH)
	@export GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED); \
	for cmd in $(CMDS); do \
		if [ -d "cmd/$$cmd" ]; then \
			printf "[%s] ---- 开始编译 %-18s ----\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$$cmd"; \
			$(GO) build -v -x -trimpath -ldflags '$(LDFLAGS)' \
				-o $(BIN_DIR)/$(GOOS)-$(GOARCH)/$$cmd ./cmd/$$cmd 2>&1 \
			| while read -r line; do \
				printf "[%s]   %s\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$$line"; \
			done; \
			if [ $${PIPESTATUS[0]} -eq 0 ]; then \
				printf "[%s] \033[32m[OK]\033[0m  %-18s -> %s/$(GOOS)-$(GOARCH)/%s\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$$cmd" "$(BIN_DIR)" "$$cmd"; \
				file $(BIN_DIR)/$(GOOS)-$(GOARCH)/$$cmd 2>/dev/null | sed 's/^/    /'; \
			else \
				printf "[%s] \033[31m[FAIL]\033[0m 编译 %s 失败\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$$cmd" >&2; \
				exit 1; \
			fi; \
		else \
			printf "[%s] 跳过 %-18s (cmd/%s 目录不存在)\n" "$$(date '+%Y-%m-%d %H:%M:%S')" "$$cmd" "$$cmd"; \
		fi; \
	done
	$(call log,===== ARM64 详细编译完成 [$(LABEL)] =====)

# ============================================================
#  清理
# ============================================================
.PHONY: clean
clean:
	$(call log,清理构建产物 ...)
	@rm -rf $(BIN_DIR) coverage.out
	$(call log,清理完成)

# ============================================================
#  帮助
# ============================================================
.PHONY: help
help:
	@echo "可用目标:"
	@echo "  fmt              格式化 Go 代码"
	@echo "  vet              运行 go vet 静态检查"
	@echo "  check            综合检查 (fmt + vet + build)"
	@echo "  test             运行单元测试 (-race -count=1 -v)"
	@echo "  test-coverage    运行测试并生成 coverage.out"
	@echo "  build            本地构建所有二进制 (默认 $(GOOS)/$(GOARCH))"
	@echo "  build-arm64      详细编译 linux/arm64 (服务器部署)"
	@echo "  build-arm64-mac  详细编译 darwin/arm64 (Apple Silicon)"
	@echo "  build-arm64-all  同时编译 linux/arm64 和 darwin/arm64"
	@echo "  clean            清理构建产物"
	@echo "  help             显示此帮助信息"
	@echo ""
	@echo "所有输出均带 [YYYY-MM-DD HH:MM:SS] 时间戳"