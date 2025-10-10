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
- 实例级/节点级
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
- 会熔断单个实例

```shell
❯ for i in {1..5};do curl http://127.0.0.1:18080/echo -w '\n';echo $(date);sleep 0.1s;done                                                                                                                              ~

status code: 500, Fatal, My host : 10.64.44.79:62153
2025年 9月 5日 星期五 15时12分26秒 CST
status code: 500, Fatal, My host : 10.64.44.79:62153
2025年 9月 5日 星期五 15时12分26秒 CST
status code: 200, Hello, My host : 10.64.44.79:62145
2025年 9月 5日 星期五 15时12分26秒 CST
status code: 200, Hello, My host : 10.64.44.79:62145
2025年 9月 5日 星期五 15时12分27秒 CST
status code: 500, Fatal, My host : 10.64.44.79:62153
2025年 9月 5日 星期五 15时12分27秒 CST
[15:12:27] [cost 0.862s] for i in {1..5};do curl http://127.0.0.1:18080/echo -w '                                                                                                                                         
';echo $(date);sleep 0.1s;done

-- 单个错误示例被熔断

❯ for i in {1..5};do curl http://127.0.0.1:18080/echo -w '\n';echo $(date);sleep 0.1s;done                                                                                                                              ~

status code: 200, Hello, My host : 10.64.44.79:62145
2025年 9月 5日 星期五 15时12分53秒 CST
status code: 200, Hello, My host : 10.64.44.79:62145
2025年 9月 5日 星期五 15时12分53秒 CST
status code: 200, Hello, My host : 10.64.44.79:62145
2025年 9月 5日 星期五 15时12分54秒 CST
status code: 200, Hello, My host : 10.64.44.79:62145
2025年 9月 5日 星期五 15时12分54秒 CST
status code: 200, Hello, My host : 10.64.44.79:62145
2025年 9月 5日 星期五 15时12分54秒 CST
[15:12:54] [cost 0.760s] for i in {1..5};do curl http://127.0.0.1:18080/echo -w '                                                                                                                                         
';echo $(date);sleep 0.1s;done

-- 恢复 provider-b

> curl 127.0.0.1:62153/switch?openError=false

-- 半开状态，请求 provider-b成功

❯ for i in {1..5};do curl http://127.0.0.1:18080/echo -w '\n';echo $(date);sleep 0.1s;done                                                                                                                              ~

status code: 200, Hello, My host : 10.64.44.79:62145
2025年 9月 5日 星期五 15时15分55秒 CST
status code: 200, Hello, My host : 10.64.44.79:62145
2025年 9月 5日 星期五 15时15分55秒 CST
status code: 200, Hello, My host : 10.64.44.79:62153
2025年 9月 5日 星期五 15时15分55秒 CST
status code: 200, Hello, My host : 10.64.44.79:62153
2025年 9月 5日 星期五 15时15分55秒 CST
status code: 200, Hello, My host : 10.64.44.79:62153
2025年 9月 5日 星期五 15时15分56秒 CST
[15:15:56] [cost 0.791s] for i in {1..5};do curl http://127.0.0.1:18080/echo -w '                                                                                                                                         
';echo $(date);sleep 0.1s;done

-- 熔断关闭

❯ for i in {1..5};do curl http://127.0.0.1:18080/echo -w '\n';echo $(date);sleep 0.1s;done                                                                                                                              ~

status code: 200, Hello, My host : 10.64.44.79:62153
2025年 9月 5日 星期五 15时16分51秒 CST
status code: 200, Hello, My host : 10.64.44.79:62145
2025年 9月 5日 星期五 15时16分51秒 CST
status code: 200, Hello, My host : 10.64.44.79:62153
2025年 9月 5日 星期五 15时16分51秒 CST
status code: 200, Hello, My host : 10.64.44.79:62153
2025年 9月 5日 星期五 15时16分51秒 CST
status code: 200, Hello, My host : 10.64.44.79:62153
2025年 9月 5日 星期五 15时16分52秒 CST
[15:16:52] [cost 0.715s] for i in {1..5};do curl http://127.0.0.1:18080/echo -w '                                                                                                                                         
';echo $(date);sleep 0.1s;done
```
