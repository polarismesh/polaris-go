## Polaris Go API方法使用

### 配置API

#### 配置初始化

```
//使用默认配置对象来初始化配置，连接默认的北极星埋点域名
cfg := api.NewConfiguration()
```

#### 修改北极星后端集群名

```
//先初始化配置
cfg := api.NewConfiguration()
//修改发现server集群名
cfg.GetGlobal().GetSystem().GetDiscoverCluster().SetService("polaris-server")
//修改心跳server集群名
cfg.GetGlobal().GetSystem().GetHealthCheckCluster().SetService("healthcheck")
//修改监控server集群名
cfg.GetGlobal().GetSystem().GetHealthCheckCluster().SetService("polaris.monitor")
```

#### 修改北极星日志路径
 
假如需要修改北极星的日志打印目录，可以按照以下方式进行修改
```
if err := api.SetLoggersDir("/tmp/polaris/log"); nil != err {
   //do error handle
}
```

#### 修改北极星日志级别

假如需要修改北极星的日志打印级别，可以按照以下方式进行修改
```
if err := api.SetLoggersLevel(api.InfoLog); nil != err {
    //do error handle
}
```
日志级别支持NoneLog, TraceLog, DebugLog, InfoLog, WarnLog, ErrorLog, FatalLog，设置成NoneLog，则不打印任何日志

#### 同时修改北极星日志路径及日志级别

polaris-go启动后，默认会在程序运行的当前目录创建polaris/log目录，用于存放运行过程中的日志。因此用户需要保证当前目录有写权限
假如需要修改北极星的日志打印目录以及日志级别，可以按照以下方式进行修改
```
if err := api.ConfigLoggers("/tmp/polaris/log", api.InfoLog); nil != err {
    //do error handle
}
```


