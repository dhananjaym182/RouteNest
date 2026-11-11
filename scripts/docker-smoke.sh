#!/usr/bin/env bash
# ============================================================================
# Docker 持久化冒烟测试：验证「重建容器（不删除存储卷）后数据不丢」。
#
# 场景对应真实事故：容器重建后账号/配置等数据异常丢失。
# 本脚本在部署机上即可运行：
#   sh scripts/docker-smoke.sh            # 使用 docker build 构建镜像后测试
#
# 流程：构建镜像 → 带卷启动 → 写入数据 → 强制删除容器 → 同卷重建 → 校验数据仍在。
# ============================================================================
set -euo pipefail

IMAGE=${SMOKE_IMAGE:-routenest-smoke}
PORT=${SMOKE_PORT:-39999}
NAME=routenest-smoke
WORK=${SMOKE_WORKDIR:-"$(mktemp -d)"}
ADMIN_BOOTSTRAP="routenest-smoke-bootstrap-secret"
ADMIN_PASSWORD="routenest-smoke-admin-password"
COOKIE_JAR="$WORK/admin.cookies"

say()  { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
pass() { printf '\033[1;32mPASS: %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31mFAIL: %s\033[0m\n' "$*"; exit 1; }

cleanup() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

say "1/6 构建镜像 $IMAGE"
docker build -t "$IMAGE" .

say "2/6 准备数据目录 $WORK/data 并预置账号数据"
mkdir -p "$WORK/data"
cat > "$WORK/data/.cline-accounts.json" <<'EOF'
{
  "accounts": [
    {"accountId": "smoke-1", "email": "smoke@example.com", "refreshToken": "rt-smoke", "status": "active"}
  ],
  "keys": ["sk-smoke-key"]
}
EOF

# 容器监听 0.0.0.0（非回环），管理端要求显式的受信代理模式：规范 URL + 受信代理 CIDR。
# 宿主机经发布端口访问时，容器看到的对端是 docker 网桥网关，因此只信任这个单一地址（/32）。
BRIDGE_GW=$(docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}' 2>/dev/null || true)
BRIDGE_GW=${BRIDGE_GW:-172.17.0.1}
say "受信代理（docker 网桥网关）：$BRIDGE_GW/32，规范 URL：http://127.0.0.1:$PORT"
ADMIN_ENV=(
  -e ROUTENEST_ADMIN_BOOTSTRAP_PASSWORD="$ADMIN_BOOTSTRAP"
  -e ROUTENEST_ADMIN_CANONICAL_URL="http://127.0.0.1:$PORT"
  -e ROUTENEST_ADMIN_PROXY_MODE=true
  -e ROUTENEST_ADMIN_TRUSTED_PROXY_CIDRS="$BRIDGE_GW/32"
)

say "3/6 启动容器（卷挂载 $WORK/data:/app/data，端口 $PORT）"
docker run -d --name "$NAME" -p "$PORT:3457" \
  "${ADMIN_ENV[@]}" \
  -v "$WORK/data:/app/data" \
  "$IMAGE" >/dev/null

wait_ready() {
  for _ in $(seq 1 60); do
    if curl -sf -m 2 "http://127.0.0.1:$PORT/ready" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  return 1
}
wait_ready || { docker logs "$NAME"; fail "容器 1 就绪检查超时"; }

say "4/6 写入数据：通过已认证管理接口生成一个 API Key"
curl -sf -c "$COOKIE_JAR" \
  -H 'Content-Type: application/json' \
  -H "Origin: http://127.0.0.1:$PORT" \
  --data "{\"secret\":\"$ADMIN_BOOTSTRAP\",\"password\":\"$ADMIN_PASSWORD\"}" \
  "http://127.0.0.1:$PORT/admin/api/auth/bootstrap" >/dev/null
CSRF=$(awk '$6 == "routenest_admin_csrf" { print $7 }' "$COOKIE_JAR")
[ -n "$CSRF" ] || fail "管理会话缺少 CSRF 凭据"
GENERATED=$(curl -sf -b "$COOKIE_JAR" -c "$COOKIE_JAR" -X POST \
  -H 'Content-Type: application/json' \
  -H "Origin: http://127.0.0.1:$PORT" \
  -H "X-RouteNest-CSRF: $CSRF" \
  --data '{}' \
  "http://127.0.0.1:$PORT/admin/api/keys/generate")
NEW_KEY=$(printf '%s' "$GENERATED" | sed -n 's/.*"key":"\([^"]*\)".*/\1/p')
[ -n "$NEW_KEY" ] || fail "生成 API Key 失败"

say "5/6 强制删除容器（模拟重建容器，保留存储卷）"
docker rm -f "$NAME" >/dev/null
docker run -d --name "$NAME" -p "$PORT:3457" \
  "${ADMIN_ENV[@]}" \
  -v "$WORK/data:/app/data" \
  "$IMAGE" >/dev/null
wait_ready || { docker logs "$NAME"; fail "容器 2（重建后）就绪检查超时"; }

say "6/6 校验重建后的数据"
KEYS=$(curl -sf -b "$COOKIE_JAR" "http://127.0.0.1:$PORT/admin/api/keys")
echo "$KEYS" | grep -q "sk-smoke-key"  || fail "预置 Key 丢失"
echo "$KEYS" | grep -q "$NEW_KEY"      || fail "重建前生成的 Key 丢失"
pass "预置数据与重建前写入的数据在重建容器后全部保留"

docker rm -f "$NAME" >/dev/null
trap - EXIT
pass "Docker 持久化冒烟测试全部通过（数据目录: $WORK/data）"
