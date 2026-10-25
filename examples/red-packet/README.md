# 示例：红包卡（宿主自定义消息类型）

演示业务功能怎样搭在 ImKit 上而不进 SDK：红包的一切（谁能领、每人领多少、钱）都归宿主，ImKit 只负责两件事——发出那条显示卡片的消息，以及把卡片的新状态实时推给在线的群成员。

## 流程

1. 发送者调宿主 `POST /api/v1/red-packets`（`topic`、`total`、`count`），宿主核对他是群成员后建红包，返回红包 id 和下一步要发的消息。
2. 发送者的客户端在群里发一条普通消息，内容类型是宿主自定的 `red_packet`：

   ```json
   {"action": "pub", "topic": "grp_…", "contentType": "red_packet", "content": "[Red packet]", "extra": {"redPacketId": "…"}}
   ```

   不认识 `red_packet` 的客户端照常显示 `content` 的文字。
3. 成员调宿主 `POST /api/v1/red-packets/:id/claim`。宿主用 `topicMembersResolve` 向 IM 服务端核对成员身份，记下这次领取，再用 `customBroadcast({ topic }, "red_packet", 卡片状态)` 推送；在线的成员收到

   ```json
   {"action": "custom_update", "topic": "grp_…", "what": "red_packet", "extra": {"id": "…", "claimed": 2, "remaining": 1, …}}
   ```

   客户端按 `what` 找到对应的卡片并刷新；不认识的 `what` 必须忽略。
4. `custom_update` 不进离线队列，所以客户端打开卡片时调 `GET /api/v1/red-packets/:id` 拉当前状态。

推送给全群的状态不含任何人的金额；每个人只在自己的应答里看到自己领了多少（`myAmount`）。

## 运行

示例通过 `file:` 依赖引用 `host-sdk-node` 的构建产物，先构建 SDK：

```bash
cd host-sdk-node && npm install && npm run build
cd ../examples/red-packet && npm install
npm run typecheck
npm test
```

账本在内存里，只为让示例短。真实的宿主在自己的数据库里扣发送者、给领取者入账，与“记下领取”放在同一个事务里，提交之后再推送。
