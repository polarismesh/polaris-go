for ((i=1; i<=3; i ++))
do
    # 设置地域信息
    export REGION=china
    export ZONE=ap-guangzhou
    export CAMPUS=ap-guangzhou-${i}
    
    # linux/mac运行命令
    ./provider > provider-20000-${i}.log 2>&1 &
done

