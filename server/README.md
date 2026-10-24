# server

ImKit 的 Go 实时服务端。

当前是计划任务 1.1 的结果：小桃子原版 IM 服务（源提交 `33e23250` 的 `server/im-go`）原样搬入，只改了模块名（`roubao/im-server` → `example.com/imkit/server`，占位符，定名后替换）。从 1.2 起按职责拆文件，每一步都要重新通过一致性测试。

## 来源与核对

- 用 `git -c core.autocrlf=false archive` 从源仓库导出，和 `tools/conformance/oracle/` 是同一个提交、同样的字节（LF）。源仓库只读，没有被改动。
- 没有搬入 `build-all.ps1`：那是原项目的交叉编译与部署脚本，输出到它自己的 `deploy\im-server` 目录，在这里路径不成立。
- 除 `go.mod` 第一行外，每个文件都和冻结的源码相同：

  ```bash
  cd server
  sha256sum *.go go.sum | sed 's/ \*\?/ */' | diff - <(grep -v ' \*go.mod$' ../tools/conformance/oracle/SOURCE.sha256)
  ```

- 去掉构建 ID 和路径（`-trimpath -ldflags=-buildid=`）后编译，和原版的产物只差嵌入的模块路径与 VCS 标记（`go version -m` 可见）。

## 构建与验证

```bash
cd server
go build -o bin/imkit-server.exe .
go vet ./... && go test ./...

cd ../tools/conformance
./bin/conformance.exe check -bin ../../server/bin/imkit-server.exe -db imkit_new -logs logs/server
```

2026-10-24 的结果：32 个场景全部通过；用本目录的服务端重新录制的 32 份归一化对话，与 `contract/protocol/transcripts/` 里的黄金对话逐字节相同（包括标为可选的帧）。

## 原样带来的已知问题

不在 1.1 里改，改了就不是“原样”。各自的处理位置写在后面。

- **数据竞争**（F58）：`hub.go` 发起通话时给 `call.ringTimer` 赋值没有持 `call.mu`，而取消呼叫或断线会在另一个 goroutine 里调用持锁读取它的 `resolve()`。`resolve()` 赶在赋值之前执行时看不到计时器、也就停不掉它，振铃超时（默认 45 秒）到点它照常触发，因为通话已结束而什么都不做。`go test -race` 能复现（`call_test.go` 里的同款写法）；不开竞态检测时单测全部通过。计划任务 1.11 修。
- **排版**：`hub.go`、`protocol.go`、`store.go` 没有按 gofmt 排版。改排版是纯机械改动，单独做一步，做完重跑一致性测试。
- 测试里会打印 `settleCallAsync: recovered panic ... nil pointer dereference`：单测的 Hub 没有配宿主客户端，异步结算在 recover 里吞掉了这个 panic，不影响测试结果。
