# 字体资源与在线库构建

当前源码内置 1 套界面字体 IBM Plex Sans SC，以及 5 套 Shell 字体：JetBrains Mono、Fira Code、Source Code Pro、IBM Plex Mono、Maple Mono CN。Maple 同时作为中文回退，IBM Plex Mono 的独立粗体属于同一字体家族。

`web/assets/fonts/` 与 `web/assets/ui-fonts/` 是随源码提交的已验证最小资源集。旧的五套 UI / 二十套 Shell 全量生成脚本已移除，防止构建时恢复已删除字体。日常编译不需要重新下载字体：

```sh
python3 build/font-licenses/verify-bundle.py
```

`library-sources.json` 保存官网 20 款界面字体、30 款 Shell 字体的上游版本、下载及输入 SHA-256、归档成员和完整 OFL 许可证。网站字体文件放在网站输出目录，不放进 `web/`，不嵌入客户端。构建需要 Python `fonttools[woff]`、`woff2_decompress`、`7z` 和 Node/Puppeteer/Chromium：

```sh
python3 build/font-licenses/build-library.py --website-output /path/to/site --cache /path/to/cache --jobs 2
node build/font-licenses/render-library.mjs /path/to/site /path/to/chrome /path/to/puppeteer-core.js
python3 build/font-licenses/package-library.py --website-output /path/to/site
```

第一步验证固定上游输入，保留完整字符集与轮廓，将非 WOFF2 格式转为 WOFF2。转换版本使用独立 Deng Library 内部家族名，版权与 OFL 保留；上游原生 WOFF2 按原样分发。第二步从完整字库实际渲染 PNG 样张，并生成官网目录与客户端内嵌目录，校验浏览器等宽效果。第三步复制字体库页面并核对资源。网站仅存一份字体；用户确认下载后，浏览器校验完整性并打包字体、OFL 与说明为 ZIP，避免在服务器重复存储字库。

客户端只能下载内嵌目录中审核过的固定官网路径，检查大小、SHA-256 和字体格式。预览下载仅为小型 PNG；应用启动与读取目录不访问字体网站。新增/更换字体文件需要重新构建并审核客户端目录；不接受服务器动态替换的未验证字库。

内置资源目前约 9.1 MiB（含目录和许可证），相对此前约 56.5 MiB 减少约 84%。这不是最终 EXE 压缩体积的测量结果。下载字体缓存在应用实际数据目录 `assets/`，与其他数据一起备份；绝不由构建脚本读写实际用户配置。

`subset-languages.py` 是显式调用的可选语言精简工具，保留原有汉字、注音、英文、通用符号及必要兼容数据。不要在发布脚本中未经视觉复核重复精简。字体变换后须重新核对目录哈希与实际字形；在线库保留完整字库，避免预览和安装副本不一致。
