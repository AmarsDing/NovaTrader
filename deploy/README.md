# NovaTrader 部署包

对标 HotCraftFlow `deploy/`：在共享测试机（如 192.168.4.44）与 twinserver / HCF / aranea 共存。

## 最短路径

```bash
cd deploy
cp env/env.example env/.env
# 编辑 .env：PG_PASSWORD=$(openssl rand -hex 16)
bash scripts/deploy.sh
```

手册见 `doc/开发文档/部署/44-部署手册.md`。

## 端口（宿主）

| 服务 | 宿主端口 |
|------|----------|
| admin HTTP / WS | 22001 / 22003 |
| web | 9302 |
| postgres / redis / nats | 5435 / 6381 / 24222+28222 |

## 注意

- dingsheng **无 docker 组**：`echo dingsheng | sudo -S docker ...`
- 不改 `/etc/docker/daemon.json`、不重启 docker
- 网络 `nt-net` = `10.181.0.0/24`
- 无 discovery / broker-gw / 通达信；模拟盘 + AkShare/Baostock
