# Cordova Android 构建

这个目录将现有 Vue 管理后台编译进 Cordova Android 应用；Go 后端仍需通过 HTTPS 独立部署。

在仓库根目录执行：

```bash
docker build -f mobile/cordova/Dockerfile \
  --build-arg VITE_API_BASE_URL=https://sub2api.example.com/api/v1 \
  -t sub2api-cordova-build .

container_id=$(docker create sub2api-cordova-build /placeholder)
docker cp "$container_id:/sub2api-release-unsigned.apk" ./mobile/cordova/sub2api-release-unsigned.apk
docker rm "$container_id"
```

产物为 `mobile/cordova/sub2api-release-unsigned.apk`。它尚未签名，仅适合测试或由 CI 后续签名。若 Docker 已安装 Buildx，也可改用 `--output type=local` 导出最终 `artifact` stage。

`VITE_API_BASE_URL` 必须是公网 HTTPS 地址，并且必须包含 `/api/v1`。后端 CORS 需要允许 Android WebView 的来源（通常为 `https://localhost`）。OAuth 与支付回跳仍需为移动端单独配置 App Link / 自定义 Scheme；不要将桌面网页回调直接复用到生产 App。

首次构建会下载前端依赖、Android SDK 镜像和 Cordova 依赖；可以为 Docker/BuildKit 配置缓存以加快后续构建。

## 从构建到安装

连接并授权一台 Android 设备后，可在仓库根目录运行：

```bash
bash mobile/cordova/build-and-install.sh https://gpt001.iotalking.top
```

脚本会自动补全 `/api/v1`、构建 APK、使用 `mobile/cordova/.dev/` 下持久化的开发证书签名，并通过 `adb install -r` 覆盖安装。已签名 APK 输出到 `mobile/cordova/build/sub2api-debug-signed.apk`。该开发证书仅适合内部测试；上架或正式发布时请使用独立且安全保管的发布证书。
