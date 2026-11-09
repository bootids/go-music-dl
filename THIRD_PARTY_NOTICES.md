# 第三方声明

本文件列出 Apple Music 整曲下载功能（`internal/applemusic`）用到或参考的第三方项目。go-music-dl 本身遵循 AGPL-3.0，见 [LICENSE](LICENSE)。

| 项目 | 许可证 | 使用方式 |
| --- | --- | --- |
| [WorldObservationLog/Temari](https://github.com/WorldObservationLog/Temari) v0.5.1 | MIT | FairPlay 样本解密算法。`internal/applemusic/temari` 是它的纯 Go 移植版，许可证全文见 [internal/applemusic/temari/LICENSE](internal/applemusic/temari/LICENSE)，并用 Temari 官方 Go binding 测试里的已知答案向量校验。 |
| [grafov/m3u8](https://github.com/grafov/m3u8) | BSD-3-Clause | Go 模块依赖，解析 HLS 主播放列表。 |
| [Eyevinn/mp4ff](https://github.com/Eyevinn/mp4ff) | MIT | Go 模块依赖，解析和改写分片 MP4（fMP4）。 |
| [WorldObservationLog/wrapper](https://github.com/WorldObservationLog/wrapper)（wrapper-lite，`lite` 分支） | MIT | 外部服务，只通过 HTTP 接口（`/status`、`/m3u8`、`/key`、`/lyrics`）调用，代码没有收进本仓库。`docker-compose.apple.yml` 直接引用它的镜像。 |
| [zhaarey/apple-music-downloader](https://github.com/zhaarey/apple-music-downloader) | — | 仅作为行为参考实现。除上面注明的 Temari 移植版（`internal/applemusic/temari` 的三个源文件取自其 `internal/temari`，文件头注明移植自 MIT Temari v0.5.1）外，没有复制它的代码。 |

Go 模块依赖的完整列表见 `go.mod`；它们各自的许可证随模块源码一起分发。
