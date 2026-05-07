GOPATH ?= $(shell go env GOPATH)

ifeq "$(GOPATH)" ""
  $(error Please set the environment variable GOPATH before running `make`)
endif

# 启动 3 个不同泳道的 provider:
#   lane-gray     : 携带 lane=gray 元数据，代表灰度泳道实例
#   lane-stable   : 携带 lane=stable 元数据，代表稳定泳道实例
#   lane-baseline : 不携带 lane 元数据，代表基线实例
.PHONY: build
build: clean
	go build -o bin *.go
	mkdir -p lane-gray lane-stable lane-baseline
	cp bin polaris.yaml ./lane-gray/
	cp bin polaris.yaml ./lane-stable/
	cp bin polaris.yaml ./lane-baseline/

.PHONY: run-gray
run-gray: build
	cd lane-gray && ./bin -lane=gray -port=18081

.PHONY: run-stable
run-stable: build
	cd lane-stable && ./bin -lane=stable -port=18082

.PHONY: run-baseline
run-baseline: build
	cd lane-baseline && ./bin -lane= -port=18083

.PHONY: clean
clean:
	rm -f bin
	rm -rf polaris lane-gray lane-stable lane-baseline
