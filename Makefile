# fan-image-tr —— Go + FFmpeg 图片处理服务
#
# 常用：
#   make help            查看全部目标
#   make build           编译到 bin/fan-image-tr
#   make build-all       交叉编译 linux amd64+arm64 到 dist/
#   make test            go vet + 单元测试
#   make race            竞态检测（较慢）
#   make check           检查本机 FFmpeg 能力
#   make run             本地启动（默认 8791）
#   make release         本地构建发布产物（dist/，见 scripts/build-release.sh）
#   make push            发版并触发 GitHub Actions（make push TAG=v0.0.1）

SHELL       := /bin/bash
GO          ?= go
BINARY      := fan-image-tr
PKG         := github.com/meimolihan/fan-image-tr
VERSION     := $(shell sed -n 's/^var Version = "\([0-9.]*\)"/\1/p' internal/version/version.go | head -1)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE        := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT) -X $(PKG)/internal/version.BuildTime=$(DATE)
DIST        := dist
DOCKER_IMAGE ?= mobufan/fan-image-tr
DOCKER_TAG   ?= $(VERSION)
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
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .
	@echo "已生成 bin/$(BINARY) (v$(VERSION))"

.PHONY: build-all
build-all: ## 交叉编译 linux amd64+arm64 到 dist/
	@mkdir -p $(DIST)
	@for target in linux/amd64 linux/arm64; do \
		os=$${target%/*}; arch=$${target#*/}; \
		echo "编译 $$target"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			$(GO) build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(DIST)/$(BINARY)_$${os}_$${arch} . || exit 1; \
	done

.PHONY: test
test: ## go vet + 单元测试
	$(GO) vet ./...
	$(GO) test -timeout 120s ./...

.PHONY: race
race: ## 竞态检测
	$(GO) test -race -timeout 300s -count=2 ./...

.PHONY: cover
cover: ## 生成覆盖率报告 coverage.html
	$(GO) test -timeout 180s -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "已生成 coverage.html"

.PHONY: fmt
fmt: ## 格式化 Go 代码
	gofmt -l -w $(GOFILES)

.PHONY: tidy
tidy: ## 整理依赖
	$(GO) mod tidy

.PHONY: check
check: build ## 检查本机 FFmpeg 能力
	./bin/$(BINARY) check

.PHONY: run
run: build ## 本地启动服务（-media 可指定浏览根目录）
	./bin/$(BINARY) -data ./data -media $(or $(MEDIA),.) -port $(or $(PORT),8791)

.PHONY: release
release: ## 本地构建发布产物（dist/，见 scripts/build-release.sh）
	bash scripts/build-release.sh

.PHONY: push
push: ## 发版并触发 GitHub Actions（TAG=v0.0.1 [MSG=备注]）
	@[ -n "$(TAG)" ] || { echo "用法: make push TAG=v0.0.1 [MSG=备注]"; exit 1; }
	bash scripts/build-and-push.sh $(TAG) --yes $(if $(MSG),-m "$(MSG)",)

.PHONY: docker
docker: ## 构建 Docker 镜像
	docker build --build-arg VERSION=$(VERSION) -t $(DOCKER_IMAGE):$(DOCKER_TAG) -t $(DOCKER_IMAGE):latest .

.PHONY: docker-push
docker-push: ## 构建并推送 multi-arch Docker 镜像
	docker buildx build --platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) \
		-t $(DOCKER_IMAGE):$(DOCKER_TAG) -t $(DOCKER_IMAGE):latest --push .

.PHONY: install
install: ## 安装为系统服务（scripts/install.sh）
	bash scripts/install.sh

.PHONY: uninstall
uninstall: ## 卸载系统服务（scripts/uninstall.sh）
	bash scripts/uninstall.sh

.PHONY: clean
clean: ## 清理构建产物
	rm -rf bin $(DIST) coverage.out coverage.html
