#!/usr/bin/env bash
# 自签 HTTPS 证书生成 —— 没有域名时的正路：自建 CA + 服务器证书 + 设备信任 CA。
#
# 为什么不直接一张自签证书完事：iOS 只能「信任一个 CA」，不能给单张裸证书开绿灯。
# 自建 CA 签出服务器证书后，把 ca.crt 装进 iPhone 并开启完全信任，URLSession/ATS
# 就按正常证书链校验放行——传输真加密、能防中间人，不需要任何跳过校验的邪门代码。
#
# Apple 对服务器证书的硬性要求（不满足直接拒连）：
#   - 必须带 SAN（Subject Alternative Name），CN 不算数 → 这里把公网 IP/本机都列进去
#   - 有效期 ≤ 825 天 → 服务器证书给 820 天；CA 自身不受此限，给 10 年
#   - ECDSA 或 RSA≥2048、SHA-256 起步 → 用 EC P-256
#
# 产物（全部在 certs/，已 gitignore——ca.key 是「根私钥」，泄露=任何人都能冒充你的服务器）：
#   ca.key / ca.crt         自建 CA（ca.crt 发给 iPhone 装；ca.key 永远只留在这台 Mac）
#   server.key / server.crt 服务器证书（这两个上传到 hermas）
#
# 用法：./scripts/gen-certs.sh   （重复执行会覆盖重签，服务器换证书后要重启服务）

set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p certs

SERVER_IP="${SERVER_IP:-101.132.191.7}"

# 1. 自建 CA
openssl ecparam -genkey -name prime256v1 -out certs/ca.key
openssl req -x509 -new -key certs/ca.key -sha256 -days 3650 \
  -subj "/CN=MenuAgent Home CA" -out certs/ca.crt

# 2. 服务器密钥 + 签发请求
openssl ecparam -genkey -name prime256v1 -out certs/server.key
openssl req -new -key certs/server.key -subj "/CN=menuagent" -out certs/server.csr

# 3. 用 CA 签服务器证书（SAN 含 公网IP/回环/localhost，本地调试同样能走 HTTPS）
cat > certs/server.ext <<EOF
basicConstraints = CA:FALSE
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = IP:${SERVER_IP}, IP:127.0.0.1, DNS:localhost
EOF
openssl x509 -req -in certs/server.csr -CA certs/ca.crt -CAkey certs/ca.key \
  -CAcreateserial -days 820 -sha256 -extfile certs/server.ext -out certs/server.crt

rm -f certs/server.csr certs/server.ext certs/ca.srl
echo ""
echo "✅ 证书已生成到 certs/："
openssl x509 -in certs/server.crt -noout -subject -enddate -ext subjectAltName
echo ""
echo "下一步："
echo "  1) server.crt/server.key 上传服务器，.env 配 TLS_CERT/TLS_KEY，重启服务"
echo "  2) ca.crt AirDrop 到 iPhone 安装，并在 设置→通用→关于本机→证书信任设置 开启完全信任"
echo "  3) ca.key 永远不要离开这台 Mac，也永远不要提交进 git"
