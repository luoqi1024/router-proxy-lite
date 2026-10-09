# 低内存开发模式

这是 Alpha 2 开发构建的可选离线安装方式，尚未包含在公开 Alpha 1 安装包中。当前只在一台小米 AX3000（RA81）原厂系统上测试，不代表其他设备也能运行。

## 原理与取舍

普通包使用 UPX 压缩内核，文件小，但启动时解压需要额外内存。紧凑模式使用 `native-small` 内核：只保留上游的运行、配置检查、版本查询及其启动辅助命令，不编译无关的命令行管理工具；关闭编译器内联以减小可执行文件，不使用 UPX。代理协议实现、TLS、REALITY 和证书校验保持不变。

该构建的完整运行包逻辑大小约 19.9 MiB，比普通包大。文件映射允许系统按需加载代码，而不是预先解压整份内核。压缩文件系统的实际占用可能较小，但安装器仍检查当前分区余量并保留 2 MiB，不能按其他设备的压缩率估算。

紧凑模式的安装门槛是总内存至少 128 MiB、可用内存至少 32 MiB，并要求专用 `native-small` 内核；普通模式仍要求 64 MiB 可用内存。管理程序采用 `GOMEMLIMIT=8MiB`，内核采用 `12MiB`，单线程及更频繁的垃圾回收。**这些值约束 Go 运行时管理的内存，不是进程总内存上限。** 不内联和频繁回收可能影响性能，吞吐与长期负载尚未测量。

## 构建与离线安装

```powershell
./tools/build.ps1 -Target armv7
./tools/build-core.ps1 -Target armv7 -Profile native-small
./tools/bundle.ps1 -Assets .local/release-assets -BundleName routerlite-armv7-compact -Core dist/routerlite-core-native-small-armv7
```

使用经过校验的完整包，按离线安装方式上传到合适的持久分区，例如 `/data/routerlite.stage`。只有该分区确实存在、可写且空间足够时才使用下列路径：

```sh
sh /data/routerlite.stage/scripts/setup.sh --check --root /data/routerlite --memory-profile compact
sh /data/routerlite.stage/scripts/setup.sh --root /data/routerlite --memory-profile compact
```

已有安装会被拒绝覆盖。紧凑模式仅支持离线包，不改变现有在线下载入口。首次安装后代理关闭，在 LAN 管理页设置密码、导入订阅再开启。内存不足时停止，不修改系统内存保留参数或关闭原厂服务来强装。

对应源码使用实际的原始二进制生成：

```powershell
python tools/source-bundle.py --core dist/routerlite-core-native-small-armv7.raw --module-cache .local/gopath/pkg/mod --rule-inputs .local/rule-inputs --go-overlay .local/go-compat/overlay.json --output dist/routerlite-compact-source.tar.gz
```

完整硬件测试范围见 [实机清单](HARDWARE-TEST.md)。构建和短时联网成功不等同长期稳定性、满负载或全部客户端应用验证。
