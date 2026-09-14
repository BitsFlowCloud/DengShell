# r21 起的更新签名与发布

新版客户端在固定 HTTPS、大小、SHA-256、版本和构建防回退之外，强制验证 Ed25519 发行签名。网站不能通过添加自己的公钥或重新计算哈希使伪造更新通过。无签名、错误签名、未信任密钥、过期描述均跳过更新，不影响主界面启动。

## 兼容与首次迁移

- JSON 继续使用 schema 2，增加 `signingKeyID`、`issuedAt`、`expiresAt`、`signature`。旧 r20 会忽略新增字段，**旧客户端仍不验证签名**。
- 用户需要通过可信途径先获得 r21 或更新客户端。首次从旧无签名更新链升级，本身不能反向得到独立签名保护。
- R28 同时提供新的 Windows 与 Linux 发行包。R20 客户端仍不验证签名，给旧描述补签名不等于升级旧程序；更新到本版后才具有客户端强制验证。GitHub Release 与官网更新端点分别发布，上传 GitHub 不会自动替换官网文件。
- Windows Authenticode / SmartScreen 与这里的更新描述签名不同；本轮没有购买证书或给 PE 添加 Authenticode。

## 发行密钥

公钥位于 `internal/updatetrust/publisher.go`，SHA-256 key ID：

```text
9d4f8c976cf8c71a1ad7345dae038b60303a8f7776e775c6638dca941b7bc052
```

本机生成的私钥种子位于 `/home/bitsflow/.local/share/dengshell-publisher/update-signing-ed25519.seed`，目录 0700、文件 0600。它不在项目、share、网站或源码包中，不由客户端读取。请由发布者离线备份，不能上传 GitHub 或源站。丢失私钥会失去为已部署客户端签发后续更新的能力；不能简单重新生成后继续使用旧公钥。

这是单发行者固定公钥方案，不宣称实现完整 TUF。日常更换密钥需要通过仍可信的旧密钥签发一个内置新公钥的客户端；新客户端移除旧密钥后才拒绝旧签名。如果旧密钥疑似泄露，停止原更新渠道并通过独立可信途径分发更换信任锚的客户端。未升级的旧客户端不能远程撤销其内置公钥。

## 签名命令

先在项目目录编译发布工具到缓存目录：

```bash
TMPDIR="$HOME/.cache/cloudshell-build" GOTMPDIR="$HOME/.cache/cloudshell-build" \
  go build -buildvcs=false -o "$HOME/.cache/cloudshell-build/sign-update" ./cmd/sign-update
```

制作匹配程序的 JSON，递增 `ApplicationBuild`，计算真实大小与两个 SHA-256。然后执行：

```bash
"$HOME/.cache/cloudshell-build/sign-update" \
  -key "$HOME/.local/share/dengshell-publisher/update-signing-ed25519.seed" \
  -in /绝对路径/up.exe.json -out /绝对路径/up.exe.json -days 90
```

Linux 对 `up.deb.json`（Debian/Ubuntu）和 `up.pkg.tar.zst.json`（Arch）做同样操作。Arch 描述使用独立的 `linux-amd64-pacman` 平台身份，下载固定为 `/up.pkg.tar.zst`；平台字段在签名覆盖范围内，不能将 DEB 包改名后冒充 Arch 包。旧版 Windows/DEB 端点及 schema 2 签名字节协议保持兼容。工具拒绝与本源码公钥不匹配的私钥、公开权限的私钥文件、无效哈希和基本身份；输出使用临时文件和原子重命名。完整打包脚本新增必需的 `--signer` 与 `--signing-key` 参数，禁止无签名发布，并拒绝把私钥放到其源码/输出目录中。

默认有效期 90 天、最长 180 天。没有新程序发布时也需在到期前重新签发描述（版本、构建和程序哈希可以保持不变）；过期后客户端只停止提供更新，不停止应用。检查时允许签发时间最多比本机快五分钟，到期时间无额外宽限。用户延迟确认或下载完成后过期，安装前会再次拒绝。

## 签名字节协议

算法固定为 Ed25519，没有来自网络的算法选择或下载公钥。签名消息是 UTF-8 的 `DengShell update manifest Ed25519 v1`，后接一个零字节，再接 `internal/updatetrust.Descriptor` 固定字段顺序的 Go `encoding/json.Marshal` 紧凑输出；其中 `signature` 置为空字符串。Go JSON 默认对 HTML 字符转义，签名覆盖全部其他字段的原始值，包括 key ID、有效期和说明。描述 JSON 的缩进、字段排列不影响验证；修改任何被解释字段会使签名失败。

签名使用标准 Base64，key ID 为公钥原始 32 字节的 SHA-256 十六进制；时间为 UTC Unix 秒。不要用其他语言默认 JSON 序列化结果直接签名，除非严格匹配该协议。建议使用随源码提供的工具。
