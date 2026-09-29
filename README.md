# RouterLite

为已解锁 SSH 的路由器提供轻量的全屋代理管理：**导入订阅，选择节点，开启代理。**

通过本地网页操作，保留原厂 Wi-Fi 和路由功能，无需刷入完整 OpenWrt 或扩大分区。代理覆盖连接到这台路由器主 Wi-Fi 和 LAN 的设备。

> **开发预览**：目前只在小米 AX1800 原厂固件上完成首轮测试，尚未发布正式安装包。解锁 SSH 不代表所有路由器都能安装；其他型号、整机重启和长期稳定性仍待验证。

## 功能

- **导入订阅**：支持 Clash / Mihomo YAML 链接或粘贴配置，在路由器本地解析。
- **选择节点**：一次使用一个节点，支持 AnyTLS、Shadowsocks（无插件）、Trojan、VLESS、VMess；支持范围以导入提示为准。
- **切换策略**：智能分流（国内规则与局域网直连）、全局代理、全部直连。
- **浏览器管理**：中文页面适配手机和电脑，使用独立管理口令，保存节点与策略。

RouterLite 负责网页、配置与网络接管，代理协议由精简的 **sing-box** 内核处理。运行时不需要 Node.js、Python、数据库或 Docker，也不加载外部网页脚本。

## 体积与兼容性

当前 ARMv7 完整开发包约 **8.83 MiB**，包含管理程序、代理内核、证书、分流规则和许可文件。这是文件体积，**不是内存占用**。

| 项目 | 当前要求 |
| --- | --- |
| 已测试设备 | 小米 AX1800，原厂 MiWiFi 1.0.394；首轮测试通过 |
| 系统环境 | Linux ARMv7、root、BusyBox、procd、iptables |
| 网络能力 | TUN、IPv4 转发与策略路由；独立 LAN/WAN，LAN 接口为 `br-lan` |
| 持久存储 | `/data` 或 `/overlay`，安装文件之外至少预留 2 MiB |
| 安装内存 | 至少 64 MiB **可用** RAM；运行需求随连接数和负载变化 |

当前只接管 IPv4；检测到 LAN 全局或 ULA IPv6 地址时拒绝开启。ARM64、MIPS 和其他固件尚未完成适配验收。完整说明见 [兼容性与验证范围](docs/COMPATIBILITY.md)。

## 开始使用

目前可从源码构建开发包，尚无公开的一键下载入口。自动安装向导通过了隔离测试，仍需干净设备验收。

1. 自行解锁 SSH，确认路由器可以正常上网。
2. 将可信、匹配设备的完整开发包上传至路由器，在包目录执行：

   ```sh
   sh scripts/setup.sh --check
   sh scripts/setup.sh
   ```

3. 安装器检查环境与空间，完成后显示管理网址和随机口令，并设置管理服务开机启动。
4. 手机或电脑连接这台路由器，打开显示的网址，登录后导入订阅、选择节点和策略、开启代理。

安装完成时代理默认关闭。日常操作在网页完成，电脑无需一直开机。上传方式、安装失败处理和维护说明见 [安装指南](docs/INSTALL.md)。

## 使用边界

- 管理页仅绑定 LAN，使用 HTTP，适合可信局域网。
- 策略或节点切换会重启代理内核，已有连接可能短暂中断；低内存时先停止旧内核再检查新配置。
- 国内规则不能保证覆盖所有服务；订阅下载需要直连提供商，或手动粘贴配置。
- 当前没有自动订阅刷新、自动升级、节点测速、负载均衡和设备级策略。
- 接在上级路由器后面时，只处理本机 LAN 下的流量，不会自动接管上级路由器的其他终端。

## 开发与文档

使用 Go 1.25+。在已安装 Go 并加入 PATH 的 Windows 电脑上预览页面：

```powershell
./tools/build.ps1 -Target demo -Go go
./dist/routerlite-demo.exe
```

打开 `http://127.0.0.1:8787`，演示口令为 `routerlite-demo`。演示模式不启动代理或修改网络。

- [安装指南](docs/INSTALL.md)
- [兼容性与验证范围](docs/COMPATIBILITY.md) · [实机测试清单](docs/HARDWARE-TEST.md)
- [构建与资源来源](docs/PROVENANCE.md) · [开发验证记录](docs/VALIDATION.md)
- [发布清单](docs/RELEASE.md) · [第三方组件](THIRD_PARTY.md)

## 许可

RouterLite 原创代码采用 [GPL-3.0-or-later](LICENSE)。第三方组件保留各自许可与署名，详见 [THIRD_PARTY.md](THIRD_PARTY.md)。本项目与 sing-box / SagerNet、小米等没有官方关联。
