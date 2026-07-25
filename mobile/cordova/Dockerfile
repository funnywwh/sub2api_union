# syntax=docker/dockerfile:1.7
# Build example:
# docker build -f mobile/cordova/Dockerfile \
#   --build-arg VITE_API_BASE_URL=https://sub2api.example.com/api/v1 \
#   -t sub2api-cordova-build .

FROM node:22-bookworm AS frontend
WORKDIR /workspace/frontend

COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
# The committed lockfile predates the workspace-level security overrides.
# Let pnpm synchronize that metadata inside the disposable image layer.
RUN corepack enable && pnpm install --no-frozen-lockfile

COPY frontend/ ./
ARG VITE_API_BASE_URL
ARG SUB2API_BUILD_HASH=mobile
ARG SUB2API_BUILD_TIME
ENV VITE_API_BASE_URL=${VITE_API_BASE_URL}
ENV SUB2API_BUILD_HASH=${SUB2API_BUILD_HASH}
ENV SUB2API_BUILD_TIME=${SUB2API_BUILD_TIME}
RUN test -n "$VITE_API_BASE_URL" \
  && node replace-build-hash.mjs \
  && pnpm exec vue-tsc -b \
  && pnpm exec vite build --outDir /workspace/mobile/cordova/www

FROM ghcr.io/cirruslabs/android-sdk:35 AS android-build
USER root
RUN apt-get update \
  && apt-get install -y --no-install-recommends gradle nodejs npm \
  && rm -rf /var/lib/apt/lists/*
WORKDIR /workspace/mobile/cordova

COPY mobile/cordova/package.json mobile/cordova/config.xml ./
RUN npm install --omit=optional
COPY --from=frontend /workspace/mobile/cordova/www ./www

# Keep the unsigned APK in a stable location for Docker's local output exporter.
RUN npx cordova prepare android \
  && npx cordova build android --release -- --packageType=apk \
  && mkdir -p /artifacts \
  && cp platforms/android/app/build/outputs/apk/release/app-release-unsigned.apk /artifacts/sub2api-release-unsigned.apk

FROM scratch AS artifact
COPY --from=android-build /artifacts/ /
