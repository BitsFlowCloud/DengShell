# DengShell 界面字体

从 r24 开始，设置 → **界面字体与文字颜色** 提供以下 5 套内置字体。默认使用 Noto 黑体。界面字体与终端字体分别选择、保存；终端网格和终端配色不受影响。

| 显示名称 | 原始项目与版本 | 特点 | 随程序附带的许可 |
| --- | --- | --- | --- |
| Noto 黑体 | [Noto Sans CJK SC](https://github.com/notofonts/noto-cjk)，固定提交 f8d157532fbfaeda587e826d4cd5b21a49186f7c | 无衬线，默认界面字体 | [版权声明和 OFL](../../web/assets/ui-fonts/noto-sans-OFL.txt) |
| Noto 宋体 | [Noto Serif CJK SC](https://github.com/notofonts/noto-cjk)，同一固定提交 | 衬线分明，适合书面阅读 | [版权声明和 OFL](../../web/assets/ui-fonts/noto-serif-OFL.txt) |
| 更纱 UI | [Sarasa Gothic](https://github.com/be5invis/Sarasa-Gothic/releases/tag/v1.0.41)，v1.0.41，UI SC Regular | 紧凑清晰，中英文混排 | [版权声明和 OFL](../../web/assets/ui-fonts/sarasa-OFL.txt) |
| 霞鹜文楷屏幕版 | [LXGW WenKai Screen](https://github.com/lxgw/LxgwWenKai-Screen/releases/tag/v1.522)，v1.522 | 针对屏幕显示调整的楷书笔形 | [版权声明和 OFL](../../web/assets/ui-fonts/wenkai-OFL.txt) |
| Maple Mono CN | [Maple Font](https://github.com/subframe7536/maple-font/releases/tag/v7.9)，v7.9，CN Regular | 圆润等宽，中英文比例 2:1 | [版权声明和 OFL](../../web/assets/ui-fonts/maple-OFL.txt) |

这些字体具有明确的 SIL Open Font License 1.1 许可，可依照许可随软件分发、修改和商业使用；它们仍受版权保护。程序保留作者版权声明、许可全文，并提供离线查看入口。转换后的字体采用独立的 Deng UI 内部名称，避免将派生字体冒用为原始发行。软件自身的许可不会取代字体许可。

## 字形与压缩

完整字体仅转换为 WOFF2，保留原字体全部 Unicode 字符映射。每套包含英文 ASCII、GB2312 的 6,763 个汉字、Big5 的 13,061 个汉字，并覆盖本轮程序文案中的全部 735 个不同汉字。这里不表示覆盖 Unicode 所有扩展区及每一种异体字。

原始映射数量分别为 44,810、44,777、46,272、47,449 和 22,731。每个界面字体均配置内置 Noto 黑体中文后备字体；用户导入只包含英文字形的字体时，中文继续使用内置字体。字体选择卡使用单独的小型预览文件，完整字体按需加载；预览文件的裁剪不影响实际应用字体。

原文件及打包文件的 SHA-256、来源、文件大小和字形数量位于 [catalog.json](../../web/assets/ui-fonts/catalog.json)。更纱、文楷、Maple 的下载文件还与 GitHub Release 提供的 SHA-256 摘要核对一致。

## 导入与文字颜色

- 导入支持 TTF、OTF、WOFF、WOFF2，单文件最大 64 MiB。保存程序自己的副本，不修改原文件。
- 新导入的字体被标记为“重启后生效”。完整退出 DengShell 再启动后即可启用。刷新页面或打开新窗口不会绕过这一要求。
- 已在启动时登记的字体、内置字体可直接切换。删除当前自定义界面字体后恢复默认内置字体。
- 浅色与深色模式可分别设置 `#RRGGBB` 文字颜色并保存；可分别恢复默认。界面颜色设置不会覆盖终端字体、终端文字颜色、背景和曲线样式。功能状态色和主按钮的白色标签保留原有含义。

## 重新生成字体资源

使用独立 Python 虚拟环境安装 `fonttools[woff]`，同时准备 `7z`。固定来源保存在 [ui-font-sources.json](ui-font-sources.json)。运行：

```bash
python build/font-licenses/bundle-ui-fonts.py /path/to/font-download-cache
```

脚本下载缺失的固定版本源文件，检查现有目录清单中的源文件摘要，保留完整字形并转换、重命名和生成选择卡预览。不要用该脚本覆盖用户导入字体。
