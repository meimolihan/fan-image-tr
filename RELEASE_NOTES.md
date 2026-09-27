# fan-image-tr v0.0.1

第一个测试版

## 下载
```bash
# 二进制（linux amd64 / arm64）
curl -fsSL -o fan-image-tr "https://github.com/meimolihan/fan-image-tr/releases/latest/download/fan-image-tr-linux-amd64"
chmod +x fan-image-tr
```

## 变更
- 1fd721c fix: 允许本地领先远端时发布，仅在远端领先或分叉时报错
- dceb583 chore: add build-and-push.sh release script
