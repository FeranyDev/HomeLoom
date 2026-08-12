# Matter Camera MC-6 验证清单

本文固定 HomeLoom Matter Camera 的人工联调与兼容性记录方法。参考基线是
connectedhomeip `v1.5.1.0` 的官方
[Camera Controller](https://github.com/project-chip/connectedhomeip/tree/v1.5.1.0/examples/camera-controller)
和
[Camera App](https://github.com/project-chip/connectedhomeip/tree/v1.5.1.0/examples/camera-app)。

截至 2026-07-29，本机未安装 `chip-camera-controller` 或 `chip-tool`，仓库中也没有相应
可执行文件。因此以下内容是可执行验证步骤，不是通过记录。缺少 Controller 二进制时
不得继续验证，也不得将缺少工具记为成功。

## 1. 固定环境

- Controller 必须由 connectedhomeip tag `v1.5.1.0` 构建；记录 commit、构建主机、
  Controller 可执行文件 SHA-256 和 GStreamer 版本。
- Controller 和 HomeLoom 必须位于同一局域网；记录网络接口、IPv4/IPv6、是否跨 VLAN。
- HomeLoom 使用一台已在线且确实包含音频的 Camera Provider 子设备，创建一个独立
  `matter-camera` Target；记录 Target ID、媒体 Stream ID 和源协议。
- 首轮固定 Node ID 为 `1`，Camera Endpoint 为 `1`。后续 Controller 矩阵不得复用同一
  存储目录或 Fabric，避免历史会话影响结果。
- 运行前确认 `<路径>` 是 connectedhomeip `v1.5.1.0` 构建的可执行
  `chip-camera-controller`，并记录其 SHA-256；准备完成仍不代表任何协议测试通过。

在 connectedhomeip 源码目录中，Linux x64 的官方构建命令为：

```text
cd /path/to/connectedhomeip
source scripts/activate.sh
./scripts/build/build_examples.py --target linux-x64-camera-controller build
```

官方输出路径是
`out/linux-x64-camera-controller/chip-camera-controller`。HomeLoom 不自动下载或构建
该工具。

## 2. 可重复 commissioning

1. 在 HomeLoom Matter Camera 卡片打开配网窗口，记录当次 Matter 手工配对码。不要填写
   HomeKit PIN。
2. 启动 Controller：

   ```text
   <路径>
   ```

3. 在 Controller 交互提示符执行：

   ```text
   pairing code 1 <HomeLoom 显示的 Matter 手工配对码>
   ```

   connectedhomeip 官方 README 也提供
   `pairing onnetwork 1 <passcode>`，但 HomeLoom 验证优先使用完整手工配对码，以同时
   约束 discriminator 和 passcode。
4. 验收必须同时满足：Controller 报告 commissioning 完成；HomeLoom 显示一个 Fabric；
   Target 重启后 Fabric 仍存在；Controller 能读取 Endpoint 1 的 Descriptor 和 Camera
   AV Stream Management 属性。只看到 mDNS 或二维码不能算通过。
5. 每轮结束后先执行：

   ```text
   pairing unpair 1
   ```

   再从 HomeLoom 删除对应 Fabric。下一轮重新打开配网窗口并使用新显示的配对资料。
   Controller 与 HomeLoom 任一侧未清理完成时，不开始下一轮。

## 3. AV 能力和资源分配

下列名称和参数顺序来自 connectedhomeip `v1.5.1.0` 生成的 Camera Controller 命令。
枚举值 `0` 在该版本示例中分别用于 H.264、Opus 和 JPEG；Stream Usage `3` 为
LiveView。命令末尾的 `1 1` 是 Node ID 和 Endpoint ID。

```text
cameraavstreammanagement video-stream-allocate 3 0 15 30 '{"width":320,"height":240}' '{"width":1280,"height":720}' 128000 4000000 30 1 1
cameraavstreammanagement audio-stream-allocate 3 0 1 48000 64000 16 1 1
cameraavstreammanagement snapshot-stream-allocate 0 1 '{"width":320,"height":240}' '{"width":1280,"height":720}' 80 1 1
```

保存三个响应返回的 `VideoStreamID`、`AudioStreamID` 和 `SnapshotStreamID`。验收要求：

- 分配结果进入相应 Allocated Streams 属性，ID 不重复；
- 超出能力或第二个并发编码器请求被明确拒绝，不得默默降级；
- deallocate 后属性和内部资源同步释放；相同 ID 不得继续用于新会话；
- Target 停用、删除或重启不得遗留 allocation。

清理命令为：

```text
cameraavstreammanagement video-stream-deallocate <VideoStreamID> 1 1
cameraavstreammanagement audio-stream-deallocate <AudioStreamID> 1 1
cameraavstreammanagement snapshot-stream-deallocate <SnapshotStreamID> 1 1
```

## 4. WebRTC 实时视频

官方 Camera Controller 的 `liveview start` 会先分配视频流，再走 ProvideOffer、
ProvideAnswer 和 ICE 建立 WebRTC：

```text
liveview start 1 --min-res-width 640 --min-res-height 480 --min-framerate 15 --min-bitrate 256000
```

通过标准不是“命令返回成功”，而是：

- 收到非空 SDP Answer，ICE 到达 connected，CurrentSessions 出现一项；
- 10 秒内出现 H.264 IDR，随后连续播放至少 5 分钟；
- 分辨率、帧率和码率不低于请求下限，画面不是周期性 JPEG；
- Opus SDP 使用 48000 Hz RTP clock，连续音频无时间戳倒退；
- Controller 发出 PLI 后 5 秒内再次收到 IDR；
- 停止后 CurrentSessions、RTP socket、Consumer 和 allocation 全部释放。

从 Controller 输出记录自动分配的 Video Stream ID，再执行：

```text
liveview stop 1 <VideoStreamID>
```

此外执行断网恢复、HomeLoom Target 停用、Camera Provider 重启和重复 EndSession。每种
故障都必须有有界超时且能在下一次 `liveview start` 前清理完成。

## 5. JPEG Snapshot

先按第 3 节分配 Snapshot Stream，再执行：

```text
cameraavstreammanagement capture-snapshot <SnapshotStreamID> '{"width":1280,"height":720}' 1 1
```

保存完整 `CaptureSnapshotResponse`。验收要求：

- `ImageData` 非空，以 JPEG SOI `FF D8` 开始、EOI `FF D9` 结束；
- 解码后尺寸为 1280×720，响应在 5 秒内完成；也可重新分配只包含
  640×360 的 Snapshot Stream 后验证 640×360；
- 连续请求产生可解码图像，且不是 MP4 init segment 或 H.264 Annex-B；
- 非法尺寸、未知或已释放的 Snapshot Stream ID 返回协议错误；
- 并发 Snapshot 不产生无界 FFmpeg 进程或内存增长。

Controller 默认日志是否把 octet string 保存为文件取决于构建配置。无法取得完整
`ImageData` 并独立解码时，该项记为 `BLOCKED`，不能只凭命令状态记为 `PASS`。

## 6. 结果矩阵

每个 Controller 版本单独记录：

| Controller | 平台/版本 | Commissioning | AV allocation | WebRTC 5 min | Opus | Snapshot | 重启恢复 | 24 h | 结论 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| chip-camera-controller | v1.5.1.0 / 待安装 | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | 工具缺失 |
| Android Controller | 待确定 | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | 未验证 |
| Apple Home | 当前无官方承诺 | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | 不作为发布门槛 |

状态只能使用：

- `PASS`：保留了日志、抓包/媒体证据和环境信息；
- `FAIL`：步骤已执行并出现可复现失败；
- `BLOCKED`：工具或环境存在，但无法取得必要结果；
- `NOT RUN`：未执行。

Apple Home 对 HAP Camera 的支持不能作为 Matter Camera 的证据。在 Apple 官方明确
支持 Matter Camera 且完成上述实测前，HomeLoom 继续使用独立 HomeKit Camera Target
服务 Apple Home。
