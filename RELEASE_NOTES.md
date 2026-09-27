# fan-image-tr v0.0.4

固定 ubuntu-24.04；改用固定版本静态 FFmpeg 8.1.3（sha256 校验），消除 Ubuntu 26 迁移告警

## 下载
```bash
# 二进制（linux amd64 / arm64）
curl -fsSL -o fan-image-tr "https://github.com/meimolihan/fan-image-tr/releases/latest/download/fan-image-tr-linux-amd64"
chmod +x fan-image-tr
```

## 变更
- a31efad ci: 固定 ubuntu-24.04 并改用固定版本静态 FFmpeg(8.1.3, sha256 校验)，消除 Ubuntu 26 迁移告警
