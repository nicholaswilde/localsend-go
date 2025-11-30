# 项目名
PROJECT_NAME := localsend_go

# 源代码目录
SRC_DIR := .

# 输出目录
OUT_DIR := ./bin

# Go 编译器
GO := go

# 目标平台
PLATFORMS := linux/amd64 linux/arm64 linux/riscv64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64 linux/arm/7 linux/arm/6

# 默认目标
.PHONY: all
all: clean build

# 清理
.PHONY: clean
clean:
	rm -rf $(OUT_DIR)

# 创建输出目录
.PHONY: create-out-dir
create-out-dir:
	mkdir -p $(OUT_DIR)

# 构建
.PHONY: build
build: create-out-dir $(PLATFORMS)

# 针对每个平台编译
$(PLATFORMS):
	GOOS=$(word 1, $(subst /, ,$@)) GOARCH=$(word 2, $(subst /, ,$@)) GOARM=$(word 3, $(subst /, ,$@)) CGO_ENABLED=0 \
	$(GO) build -o $(OUT_DIR)/$(PROJECT_NAME)-$(word 1, $(subst /, ,$@))-$(word 2, $(subst /, ,$@))$(if $(word 3, $(subst /, ,$@)),v$(word 3, $(subst /, ,$@)))$(if $(findstring windows,$@),.exe) $(SRC_DIR)

# 测试
.PHONY: test
test:
	$(GO) test ./...

# 安装依赖
.PHONY: deps
deps:
	$(GO) mod tidy

.PHONY:format
format:
	go fmt ./...

# 构建 deb 包
.PHONY: deb
deb: build
	chmod +x build_dpkg.sh
	./build_dpkg.sh

# 使用方法
.PHONY: help
help:

	@echo "Usage:"
	@echo "  make            - 编译所有平台的可执行文件"
	@echo "  make clean      - 清理输出目录"
	@echo "  make build      - 编译所有平台的可执行文件"
	@echo "  make deb        - 构建 deb 包 (需要 Linux 环境)"
	@echo "  make test       - 运行测试"
	@echo "  make deps       - 安装依赖"
	@echo "  make help       - 显示此帮助信息"
