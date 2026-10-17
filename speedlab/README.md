# speedlab — XDL 下载引擎性能基准

用于定位 `internal/engine` 传输层的吞吐瓶颈。所有实验都跑真实引擎代码，不修改生产逻辑。

## 组件

| 组件 | 作用 |
|---|---|
| `benchsrv/` | Range 服务的基准服务器。支持全局带宽上限（`-cap`）与单连接上限（`-perconn`），用于模拟真实宽带 |
| `bench/` | 用真实 `engine.HTTPDownloader` 下载，报告吞吐、t25/t50/t90 达成时间、逐 250ms 速度曲线 |
| `probe/` | 分阶段隔离：`raw`（纯网络）/ `rawbuf`（加大缓冲）/ `file`（加磁盘写） |
| `stage/` | 引擎 vs 手写等价实现的严格对照，隔离 ramp-up 与磁盘写入的影响 |
| `abtest/` | 多次重复取中位数，抵消单次抖动 |

## 用法

```bash
# 1) 启动无限速服务器（测客户端天花板）
go build -o benchsrv.exe ./speedlab/benchsrv
./benchsrv.exe -addr 127.0.0.1:18099 -bufsz 8388608

# 2) 跑引擎基准
go build -o bench.exe ./speedlab/bench
./bench.exe -url http://127.0.0.1:18099/file -size 1073741824 -conns 16

# 3) 分阶段定位瓶颈
go build -o probe.exe ./speedlab/probe
./probe.exe -mode raw    -size 2147483648 -conns 8   # 只走网络
./probe.exe -mode rawbuf -size 2147483648 -conns 8   # 128KiB 缓冲
./probe.exe -mode file   -size 2147483648 -conns 8   # 加磁盘写

# 4) 引擎 vs 手写对照
go build -o stage.exe ./speedlab/stage
./stage.exe -url http://127.0.0.1:18099/file -size 1073741824 -conns 16
```

> Windows 上若环境存在 `HTTP_PROXY`，请先 `set NO_PROXY=*` 或加 `--noproxy '*'`，
> 否则 curl 等工具会把 loopback 请求也送进代理。

## 注意事项

- `benchsrv` 的限速基于 `time.Sleep`，高并发下会失真，**不适合做精确带宽整形**；
  需要精确上限时请改用外部工具（如 `clumsy`、`tc netem`、Docker `--limit`）。
- 磁盘基准在这类机器上噪声极大，单次结果不可信，必须多次取中位数。
