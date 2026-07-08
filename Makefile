.PHONY: build build-backend build-frontend build-datamanagementd docker-export test test-backend test-frontend test-frontend-critical test-datamanagementd secret-scan

DOCKER_IMAGE ?= weishaw/sub2api:latest
DOCKER_ARCHIVE ?= sub2api.tgz
GOPROXY ?= https://goproxy.cn,direct
GOSUMDB ?= sum.golang.org

FRONTEND_CRITICAL_VITEST := \
	src/views/auth/__tests__/LinuxDoCallbackView.spec.ts \
	src/views/auth/__tests__/WechatCallbackView.spec.ts \
	src/views/user/__tests__/PaymentView.spec.ts \
	src/views/user/__tests__/PaymentResultView.spec.ts \
	src/components/user/profile/__tests__/ProfileInfoCard.spec.ts \
	src/views/admin/__tests__/SettingsView.spec.ts

# 一键编译前后端
build: build-backend build-frontend

# 编译后端（复用 backend/Makefile）
build-backend:
	@$(MAKE) -C backend build

# 编译前端（需要已安装依赖）
build-frontend:
	@pnpm --dir frontend run build

# 编译 datamanagementd（宿主机数据管理进程）
build-datamanagementd:
	@cd datamanagement && go build -o datamanagementd ./cmd/datamanagementd

# 构建并导出 Docker 镜像
docker-export:
	@echo "构建 Docker 镜像: $(DOCKER_IMAGE)"
	@docker build . -t $(DOCKER_IMAGE) \
		--build-arg GOPROXY=$(GOPROXY) \
		--build-arg GOSUMDB=$(GOSUMDB)
	@echo "导出 Docker 镜像: $(DOCKER_ARCHIVE)"
	@tmp_dir="$$(mktemp -d .sub2api-image.XXXXXX)" && \
	tmp_tgz="$$(mktemp .sub2api-image.XXXXXX.tgz)" && \
	trap 'rm -rf "$$tmp_dir" "$$tmp_tgz"' EXIT && \
	docker save -o "$$tmp_dir/sub2api.tar" $(DOCKER_IMAGE) && \
	tar -C "$$tmp_dir" -zcvf "$$tmp_tgz" sub2api.tar && \
	mv "$$tmp_tgz" $(DOCKER_ARCHIVE)
	@echo "完成: $(DOCKER_ARCHIVE)"

# 运行测试（后端 + 前端）
test: test-backend test-frontend

test-backend:
	@$(MAKE) -C backend test

test-frontend:
	@pnpm --dir frontend run lint:check
	@pnpm --dir frontend run typecheck
	@$(MAKE) test-frontend-critical

test-frontend-critical:
	@pnpm --dir frontend exec vitest run $(FRONTEND_CRITICAL_VITEST)

test-datamanagementd:
	@cd datamanagement && go test ./...

secret-scan:
	@python3 tools/secret_scan.py
