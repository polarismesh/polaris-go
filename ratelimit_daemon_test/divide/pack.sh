#!/bin/bash

packname="polaris-ratelimit-divide"

mkdir -p ${packname}/bin
mkdir ${packname}/conf
go build -o polaris-ratelimit-v2-divide
cp polaris-ratelimit-v2-divide ./${packname}/bin

cp -r ../tool ./${packname}/
cp include ./${packname}/tool
tar -zcvf ${packname}.tar.gz ${packname}