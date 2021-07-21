#!/bin/bash

packname="polaris-ratelimit-go"

mkdir -p ${packname}/bin
mkdir ${packname}/conf
cd daemon
go build -o polaris-ratelimit-go
cd ../
cp daemon/polaris-ratelimit-go ./${packname}/bin

cp -r ./tool ./${packname}/
tar -zcvf ${packname}.tar.gz ${packname}