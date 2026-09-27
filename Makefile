# fan-image-tr —— Go + FFmpeg 图片处理服务
#
# 常用：
#   make help            查看全部目标
#   make build           编译到 bin/fan-image-tr
#   make test            go vet + 单元测试
#   make race            竞态检测（较慢）
#   make check           检查本机 FFmpeg 能力
#   make run             本地启动（默认 8791）

BINARY      := fan-image-tr
PKG         := github.com/meimolihan/fan-image-tr
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X $(PKG)/internal/version.Version=$(VERSION)
DIST        := dist
GOFILES     := $(shell find . -name '*.go' -not -path './dist/*')

export PATH := $(PATH):/usr/local/go/bin

.PHONY: help
help: ## 显示帮助
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: all
all: check build test ## 依次执行环境检查、编译与测试

.PHONY: build
build: ## 编译二进制到 bin/
	@mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .
	@echo "已生成 bin/$(BINARY) ($(VERSION))"

.PHONY: build-all
build-all: ## 交叉编译 linux/darwin/windows amd64+arm64 到 dist/
	@mkdir -p $(DIST)
	@for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
		os=$${target%/*}; arch=$${target#*/}; ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "编译 $$target"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(DIST)/$(BINARY)-$$os-$$arch$$ext . || exit 1; \
	done

.PHONY: test
test: ## go vet + 单元测试
	go vet ./...
	go test -timeout 120s ./...

.PHONY: race
race: ## 竞态检测
	go test -race -timeout 300s -count=2 ./...

.PHONY: cover
cover: ## 生成覆盖率报告 coverage.html
	go test -timeout 180s -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "已生成 coverage.html"

.PHONY: fmt
fmt: ## 格式化 Go 代码
	gofmt -l -w $(GOFILES)

.PHONY: tidy
tidy: ## 整理依赖
	go mod tidy

.PHONY: check
check: build ## 检查本机 FFmpeg 能力
	./bin/$(BINARY) check

.PHONY: run
run: build ## 本地启动服务（-media 可指定浏览根目录）
	./bin/$(BINARY) -data ./data -media $(or $(MEDIA),.) -port $(or $(PORT),8791)

.PHONY: docker
docker: ## 构建 Docker 镜像
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY):$(VERSION) .

.PHONY: clean
clean: ## 清理构建产物
	rm -rf bin $(DIST) coverage.out coverage.html
