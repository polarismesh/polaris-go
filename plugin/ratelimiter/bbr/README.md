# Kratos - BBR
Kratos 是 bilibili 开源的一套 Go 微服务框架。BBR 是其中的一个限流组件。参考了 [TCP BBR](https://en.wikipedia.org/wiki/TCP_congestion_control#TCP_BBR) 的思想，以及 [阿里 Sentinel](https://github.com/alibaba/Sentinel/wiki/系统自适应限流) 的算法。

传统的限流思路为：超过一定负载就拦截流量进入，负载恢复就放开流量，这样做有延迟性，最终是按照果来调节因，无法取得良好效果。

BBR 的思路为：超过一定 CPU 负载后，根据应用的平均 RT、当前请求数等指标，综合判断是否应当拦截流量，即所谓"自适应"。

BBR 的源码实现可参考此解析：[yuemoxi - 从kratos分析BBR限流源码实现](https://juejin.cn/post/7004848252109455368)



# 插件设计
本插件使用了 kratos 的 BBR 限流器，将其适配成 `QuotaBucket` 接口（主要实现 `GetQuota` 判断限流方法），以及 `ServiceRateLimiter` 接口（实现 `InitQuota` 初始化方法）。

由于 CPU 使用率指标为实例单机指标，因此 CPU 限流只适用于单机限流，不适用于分布式限流，未实现分布式限流器需要实现的接口。


## 初始化 InitQuota
kratos - BBR 初始化需要三个入参：
```
CPUThreshold: CPU使用率阈值，超过该阈值时，根据应用过去能承受的负载判断是否拦截流量 
window: 窗口采样时长，控制采样多久的数据
bucket: 桶数，BBR 会把 window 分成多个 bucket，沿时间轴向前滑动。如 window=1s, bucket=10 时，整个滑动窗口用来保存最近 1s 的采样数据，每个小的桶用来保存 100ms 的采样数据。当时间流动之后，过期的桶会自动被新桶的数据覆盖掉
```
这三个入参，从 `apitraffic.Rule` 结构体中解析

分别对应结构体中的 `MaxAmount`、`ValidDuration`、`Precision` 字段


## 判断限流 GetQuota
调用了 BBR 的 `Allow()` 方法

其内部执行 `shouldDrop()` 方法，判断是否超过设定的 CPU使用率阈值，以及对比当前请求数 `inFlight` 和历史请求数 `maxInFlight`，来决定是否拦截本次请求

关键执行流程如下：

![img.jpg](img.jpg)