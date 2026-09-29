# 构建、资源与对应源码

构建在电脑上完成。路由器只安装管理程序、精简内核、证书、两份规则和必要许可文件，不安装 Go、Python、UPX 或源码缓存。

## 构建程序

当前复现工具链为 Go 1.27.1、UPX 5.2.1、Python 3.12+。Windows 构建脚本默认从 `.local/tools` 查找 Go 和 UPX；工具应从各自官方渠道取得。管理程序的 `tools/build.ps1` 可通过 `-Go`、`-Upx` 指定路径。内核脚本固定 sing-box 1.14.2 的源码哈希，并用 `tools/core-profile.py` 精简协议注册。

```powershell
./tools/build.ps1 -Target armv7
./tools/build-core.ps1 -Target armv7
./tools/build-core.ps1 -Target check-windows
```

构建脚本使用 `-trimpath -buildvcs=false`，保留 `.raw` 原始二进制后执行 UPX。每次编译到新文件，避免 Go build ID 导致复用已压缩的产物。

## 生成规则

`rules.lock.json` 固定两份原始数据的公开地址、字节数、SHA256 和署名。IP 输入是 DB-IP Lite 的固定镜像快照，采用 CC BY 4.0；域名输入是固定提交的 domain-list-community，采用 MIT。规则转换工具核对输入后才运行；不会在路由器上下载大数据库。

```powershell
python tools/build-rules.py --fetch --output .local/rules-build --core dist/routerlite-core-check.exe
```

输出目录必须尚不存在。输出含人可读 JSON、编译后的 SRS 和输出哈希。此转换器只接受当前固定快照实际使用的域名语法；遇到未来未支持的语法会报错，避免悄悄遗漏规则。它支持递归包含、属性筛选、精确域名、后缀、关键词和正则。

将两个 `.srs` 和锁文件所指的 Mozilla CA 包放入同一资源目录，再验证、打包：

```powershell
python tools/assets.py verify --output .local/release-assets
./tools/bundle.ps1 -Assets .local/release-assets -BundleName routerlite-armv7-candidate
python tools/package-install.py --bundle dist/routerlite-armv7-candidate --output dist/install-candidate --version 0.1.0-candidate
```

CA 下载地址和哈希见 `assets.lock.json`。资源目录里的三个文件必须全部符合该锁文件。当前锁文件内 SRS 地址是计划中的首个 Release 资产地址，**尚未发布，不能使用 `assets.py fetch` 下载它们**；维护者现阶段按上述源码转换流程生成。二进制发布后可用 `assets.py fetch` 获取已核验的完整资源集。

不接受校验不符的本地替换，不自动覆盖旧构建目录。更新数据时须审核新来源、转换结果与分流行为，再修改锁文件；用户设备不会因此自动更新。

## 完整对应源码

先确认所有新源文件已提交，再从实际原始二进制生成对应源码包：

```powershell
python tools/source-bundle.py --module-cache .local/gopath/pkg/mod --rule-inputs .local/rule-inputs --output dist/routerlite-source.tar.gz
```

可多次指定 `--module-cache`。工具按二进制真实 Go 模块信息收集依赖，逐 ZIP 计算 `h1` 并比对；归档包含模块代理、原始内核、项目源码、构建脚本、规则原始输入、清单和哈希。未提交的工作区会标记 dirty，不得作为已提交版本的发布源码。

模块变化时，从候选包重新收集许可文本，并复核后更新仓库中的 `licenses/go-dependencies.txt`：

```powershell
python tools/source-notices.py dist/routerlite-source.tar.gz --output .local/go-dependencies-new.txt
```

解压对应源码包至新目录，使用相同的已安装 Go 工具链验证：

```powershell
python tools/verify-source.py --source .local/source-extracted --work .local/rebuild-check --go .local/tools/go/bin/go.exe
```

该命令先校验源码清单，创建空的 Go 缓存，设置本地文件模块代理与 `GOTOOLCHAIN=local`，重新编译管理程序和内核并比对原始二进制哈希。不会下载 Go 或联网获取模块。压缩文件可用相同 UPX 的 `--best --lzma` 从原始文件重建。规则可用对应源码中的 `rule-inputs` 离线重建，但需要与宿主系统匹配的 sing-box 编译工具。

## 已验证与待验证

2026-09-29：旧候选对应源码中的 56 个依赖可独立离线重建，管理程序与内核原始二进制逐字节一致。新规则已通过真实内核的二进制编译和国内/国外匹配抽查；生成器、文件校验、隔离安装和网络事务测试通过。

当前候选的实机全新安装、网页首次设置密码、新规则联网及整机重启仍待执行。完整发布要求见 [发布清单](RELEASE.md)，这里的构建成功不代替硬件验收。
