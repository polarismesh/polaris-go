# Polaris Go

[English](README.md) | 中文

## 使用故障熔断
北极星支持熔断异常的服务、实例或者接口，降低请求失败率。

## 如何使用
进入 上一级的callee目录，启动 provider-a 和 provider-b
### 执行provider程序
- provider
```
cd provider
export POLARIS_SERVER=127.0.0.1
make run
```

### 设置熔断规则
- 服务级
  ![create_service_circuitbreaker](./image/create_circuitbreaker.png)

### 执行consumer程序
- consumer
```
cd consumer
export POLARIS_SERVER=127.0.0.1
make run
```

## 验证
### 服务级熔断
- provider-a正常，provider-b异常
- 设置熔断规则失败率大于等于50%
- 会熔断整个服务

```shell
❯ for i in {1..10};do curl http://127.0.0.1:18080/echo -w '\n';echo $(date);sleep 0.1s;done                                                                                                                       ~

status code: 200, Hello, My host : 10.64.44.79:59242
2025年 9月 5日 星期五 11时00分01秒 CST
status code: 200, Hello, My host : 10.64.44.79:59242
2025年 9月 5日 星期五 11时00分01秒 CST
status code: 200, Hello, My host : 10.64.44.79:59242
2025年 9月 5日 星期五 11时00分01秒 CST
status code: 500, Fatal, My host : 10.64.44.79:59235
2025年 9月 5日 星期五 11时00分01秒 CST
status code: 200, Hello, My host : 10.64.44.79:59242
2025年 9月 5日 星期五 11时00分01秒 CST
status code: 200, Hello, My host : 10.64.44.79:59242
2025年 9月 5日 星期五 11时00分01秒 CST
status code: 200, Hello, My host : 10.64.44.79:59242
2025年 9月 5日 星期五 11时00分01秒 CST
status code: 500, Fatal, My host : 10.64.44.79:59235
2025年 9月 5日 星期五 11时00分02秒 CST
status code: 200, Hello, My host : 10.64.44.79:59242
2025年 9月 5日 星期五 11时00分02秒 CST
status code: 500, Fatal, My host : 10.64.44.79:59235
2025年 9月 5日 星期五 11时00分02秒 CST
[11:00:02] [cost 1.396s] for i in {1..10};do curl http://127.0.0.1:18080/echo -w '                                                                                                                                  
';echo $(date);sleep 0.1s;done


❯ for i in {1..10};do curl http://127.0.0.1:18080/echo -w '\n';echo $(date);sleep 0.1s;done                                                                                                                       ~

status code: 500, Fatal, My host : 10.64.44.79:59235
2025年 9月 5日 星期五 11时00分38秒 CST
status code: 500, Fatal, My host : 10.64.44.79:59235
2025年 9月 5日 星期五 11时00分39秒 CST
status code: 500, Fatal, My host : 10.64.44.79:59235
2025年 9月 5日 星期五 11时00分39秒 CST
status code: 500, Fatal, My host : 10.64.44.79:59235
2025年 9月 5日 星期五 11时00分39秒 CST
status code: 500, Fatal, My host : 10.64.44.79:59235
2025年 9月 5日 星期五 11时00分39秒 CST
status code: 500, Fatal, My host : 10.64.44.79:59235
2025年 9月 5日 星期五 11时00分39秒 CST
status code: 200, Hello, My host : 10.64.44.79:59242
2025年 9月 5日 星期五 11时00分39秒 CST
status code: 200, Hello, My host : 10.64.44.79:59242
2025年 9月 5日 星期五 11时00分39秒 CST
status code: 200, Hello, My host : 10.64.44.79:59242
2025年 9月 5日 星期五 11时00分39秒 CST
status code: 500, Fatal, My host : 10.64.44.79:59235
2025年 9月 5日 星期五 11时00分40秒 CST
[11:00:40] [cost 1.342s] for i in {1..10};do curl http://127.0.0.1:18080/echo -w '                                                                                                                                  
';echo $(date);sleep 0.1s;done

-- 整个服务被熔断
❯ for i in {1..10};do curl http://127.0.0.1:18080/echo -w '\n';echo $(date);sleep 0.1s;done                                                                                                                       ~

status code: 500, Fatal, My host : 10.64.44.79:59235
2025年 9月 5日 星期五 11时02分47秒 CST
[error] fail : call aborted
2025年 9月 5日 星期五 11时02分47秒 CST
[error] fail : call aborted
2025年 9月 5日 星期五 11时02分48秒 CST
[error] fail : call aborted
2025年 9月 5日 星期五 11时02分48秒 CST
[error] fail : call aborted
2025年 9月 5日 星期五 11时02分48秒 CST
[error] fail : call aborted
2025年 9月 5日 星期五 11时02分48秒 CST
[error] fail : call aborted
2025年 9月 5日 星期五 11时02分48秒 CST
[error] fail : call aborted
2025年 9月 5日 星期五 11时02分48秒 CST
[error] fail : call aborted
2025年 9月 5日 星期五 11时02分48秒 CST
[error] fail : call aborted
2025年 9月 5日 星期五 11时02分48秒 CST
[11:02:49] [cost 1.362s] for i in {1..10};do curl http://127.0.0.1:18080/echo -w '                                                                                                                                  
';echo $(date);sleep 0.1s;done
```
