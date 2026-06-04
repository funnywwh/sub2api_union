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
	@tmp_tar="$$(mktemp .sub2api-image.XXXXXX.tar)" && \
	tmp_gz="$$(mktemp .sub2api-image.XXXXXX.tgz)" && \
	trap 'rm -f "$$tmp_tar" "$$tmp_gz"' EXIT && \
	docker save -o "$$tmp_tar" $(DOCKER_IMAGE) && \
	gzip -c "$$tmp_tar" > "$$tmp_gz" && \
	mv "$$tmp_gz" $(DOCKER_ARCHIVE)
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
