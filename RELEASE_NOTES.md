# fan-image-tr v0.0.8

单一流水线：合并常规检查与镜像冒烟，消除 Node.js 20 弃用警告

## 下载（二进制）
```bash
curl -fsSL -o fan-image-tr "https://github.com/meimolihan/fan-image-tr/releases/latest/download/fan-image-tr_linux_amd64"
chmod +x fan-image-tr
```

## Docker 安装
```bash
docker pull mobufan/fan-image-tr:latest
docker run -d --name fan-image-tr -p 8791:8791 -v /var/lib/fan-image-tr:/data mobufan/fan-image-tr:v0.0.8
```

```bash
docker pull ghcr.io/meimolihan/fan-image-tr:v0.0.8
```

## 二进制安装
```bash
bash -c "$(curl -sSL https://raw.githubusercontent.com/meimolihan/fan-image-tr/main/scripts/install.sh)" -p 8791
```

## 二进制卸载
```bash
bash -c "$(curl -sSL https://raw.githubusercontent.com/meimolihan/fan-image-tr/main/scripts/uninstall.sh)" -y
```

## 变更

