# layer 233 Windows 实机验收

2026-11-16，通过 macOS Windows App 连接 ROG Windows 实机操作。结论仅覆盖下列用例，不代表所有业务或正式加壳产物验收通过。

## 产物来源

| 项目 | 身份 |
| --- | --- |
| TDLib | 1.8.70 / layer 233，`0964206624ce50eb669672918ef2fce381f1422d` |
| 原生 CI | [43754884489](https://github.com/tqrj/tdPatch/actions/runs/43754884489)，Windows、macOS 构建与 `test-patches` 均成功 |
| Windows 原生压缩包 SHA-256 | `8069b19ffc70dbf188ad3ba57cf98d9ff25633f5693c136e7fbf533c279f15c4` |
| Go bindings 代码 | `298717dd2187dcd8839f12057901a455e4a13fef`，`TDLIB_VERSION` 对齐上述原生提交 |
| TGDirector 代码 | `7072ebdd6a1b1279cc0e93805c9ca557939ee12d` |
| 会话转换依赖 | `4a7037b3e8b462bd40ecfb5b88a036a312e2a9aa` |
| 实测 EXE SHA-256 | `fc0dede9c3e9f999a4d58d9f83d4a1e16bda51f23fc9b8ec7ce448ed4ddf1a1c` |
| EXE 大小 | 170073600 字节 |
| 构建标签 | `production,tdlib,tgnative`，**未加壳内部验收版**，需要配套 `VMProtectSDK64.dll` |

应用在独立工作树构建。只为配套联调增加临时依赖替换，不修改 TGDirector 业务代码或生产依赖锁定：

```go
replace github.com/tqrj/go-tdlib => ../go-tdlib
```

前端从该工作树执行 `npm ci && npm run build`。资源生成使用本机已有 Wails CLI beta.33，仅生成 `.syso`，没有重新生成 Wails bindings。它不是标准发布 CI 的正式应用产物。

## 运行环境

Windows NT 10.0.26200 / AMD64，约 32 GiB RAM；Go 1.27.1；MSYS2 MINGW64 GCC 15.2.0 Rev8；链接本机 OpenSSL 3.6.0。

静态 `libcrypto.a` SHA-256 为 `85702694872989d016f63ff197db248d95c31d8ec5c1cf660dfe6a03ea553f5b`。EXE 导入表没有 TDLib、OpenSSL 或 MinGW runtime DLL，保留 VMProtect SDK 导入。这不构成加壳证明。

## 账号隔离

只读复制原应用 SQLite，只保留编号 3、4 两个获授权账号及其 TDLib 目录，另设进程级 `APPDATA`。清空测试副本中的任务、客服监听和自动回复等业务表，再通过界面明确接入两个账号。

`ribtc`、`ritrump` 及同前缀账号均不在测试账号表与 TDLib 目录中，未用于登录、收发或其他 Telegram 操作。原安装文件未替换，原数据目录不作为验收版的写入目标。隔离辅助工具用合成 SQLite 和目录测试过白名单、拒绝保护账号、源库不变及只复制选中会话。

## 已验证

| 用例 | 实测结果 |
| --- | --- |
| Windows Go bindings | 全套测试通过，`TestLinkedTDLibMatchesSchema` 实际调用原生库核对版本与提交号 |
| Windows TDLib 适配层 | `go test -count=1 -tags tdlib,tgnative ./internal/tg/tdlib` 通过，含恢复过程中关闭的离线事故回归 |
| 实际 EXE 身份 | 字节扫描命中原生 `096420662…`、`1.8.70` 及验收版版本字符串；未命中旧原生 `401e262ed…` 或中间提交 `50b63e599…` |
| 启动 | 界面手动验证已有授权，加载预设后进入主窗 |
| 会话恢复 | 两个账号恢复上线，客服列表与真实会话加载成功 |
| 文字发送 | 编号 3 向自己的收藏夹投递一条 UTF-8 中文标记；发送统计 1/1，客服页读回服务端消息并显示已发送勾选 |
| 图片 | 向同一收藏夹发送合成图片，显示发送完成；从气泡执行另存为，得到有效 640×320 JPEG，10337 字节 |
| 图片导出 SHA-256 | `009e153c53e8391df5316e17c7fdb726521d69e24e388cf3751b2a1963bc3f49` |
| 轻负载观察 | 127.62 秒内 CPU 增量 1.15625 秒，约单核 0.906% / 整机 0.028%；应用私有内存约 239 MiB，持续有响应。该时段包含图片另存为，不是严格空闲基准 |
| 空闲观察 | 正常重启后不再操作界面，99.19 秒内 CPU 增量 0.953125 秒，约单核 0.961% / 整机 0.030%；私有内存约 230 MiB，持续有响应，两个旧探针进程均不存在 |
| 正常退出 | 界面关闭后进程退出码 0，没有剩余同路径应用进程 |
| 正常重启 | 同一 SHA-256 的 EXE 再启动，两账号恢复上线，收藏夹文字与图片均读回且未重复 |

图片的另存为可能使用 TDLib 本地缓存，不单独宣称已证明一次全新的远程 CDN 下载。

## 未通过的用例与环境干扰

### 陌生私信

编号 3 首次向另一个测试账号私信时，Telegram 返回 `PEER_FLOOD`。应用显示账号受限，本轮已停止，没有再启动陌生私信轮次。该路径不记为发送成功，也不据此判定协议升级回归。后续成功发送限定在自己的收藏夹。

### 辅助进程耗尽内存

早期打开收藏夹时 WebView2 显示 `Out of Memory`。现场两个旧 PowerShell 辅助进程分别占用约 31.6 GB、27.5 GB 私有提交内存，系统记录资源耗尽事件 2004；当时应用此前的工作集约 237 MB。

旧进程退出后空闲物理内存恢复到约 28 GiB。避开旧探针、只返回基本类型后，未修改业务代码、未重建 EXE，重新打开同一会话、发送图片、另存为及正常重启均成功。旧探针的深层对象序列化是增长原因的主要嫌疑，但未用再次耗尽内存的方式做最终归因。该轮受污染结果保留，不记为应用验收通过，也不据此认定需要重装 WebView2。

## 验收边界

- PFS 强制开关、临时密钥寿命与 `dropTempAuthKeys`、RawConnection 随机 padding 强制开关、外层 Session 调度及成功联网后的缺失 `my_id` 初始化重放，仍按原生仓库 [PATCHES.md](https://github.com/tqrj/tdPatch/blob/0964206624ce50eb669672918ef2fce381f1422d/PATCHES.md) 区分源码审计与行为测试。上述实机操作不替代这些边界的专门证明。
- 不刻意触发真实 `FLOOD_WAIT` / `SLOWMODE_WAIT`，不跑群发压力测试，也未取得陌生私信或跨账号媒体收发成功结论。
- 本次没有验证 VMProtect 最终保护覆盖，没有生成正式受保护分发包，没有发布标签或 Release。
- TGDirector 的生产 `go.mod` 与 `TDPATCH_TAG` 未切换。正式升级仍需配套发布并验收正式应用，不能直接分发本次内部 EXE。

## 收尾

两次界面正常关闭均得到退出码 0。临时账号副本、原始运行日志、构建目录和桌面输入文件已清理。收藏夹保留一条验收文字与一张合成图片，没有向真实群发送消息。

Windows 仅保留不含账号、会话或卡密的内部产物 `acceptance-app.zip` 及概要材料。ZIP 为 112276754 字节，SHA-256 为 `c3808159bf24411db079967251c5cc49e52671369e5f4662f5f3d4a2684e7097`。包含 EXE、SDK DLL、说明与隔离启动脚本；需要复查时使用 `run-isolated.cmd`，不能作为正式分发包。

本地证据包括原生 CI 日志、构建与库身份结果、脱敏运行指标，以及 `03-self-text-sent.png`、`04-webview-failure.png`、`06-self-history-after-restart.png`、`07-self-image-sent.png`、`09-text-image-normal-restart.png`。账号数据、原始 TDLib 日志、卡密和会话文件不随本记录提交。
