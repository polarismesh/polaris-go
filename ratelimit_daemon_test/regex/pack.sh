#!/bin/bash

packname="polaris-ratelimit-regex"

mkdir -p ${packname}/bin
mkdir ${packname}/conf
go build -o polaris-ratelimit-v2-regex
cp polaris-ratelimit-v2-regex ./${packname}/bin

cp -r ../tool ./${packname}/
cp include ./${packname}/tool
tar -zcvf ${packname}.tar.gz ${packname}