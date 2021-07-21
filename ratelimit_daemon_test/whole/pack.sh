#!/bin/bash

packname="polaris-ratelimit-whole"

mkdir -p ${packname}/bin
mkdir ${packname}/conf
go build -o polaris-ratelimit-v2-whole
cp polaris-ratelimit-v2-whole ./${packname}/bin

cp -r ../tool ./${packname}/
cp include ./${packname}/tool
tar -zcvf ${packname}.tar.gz ${packname}