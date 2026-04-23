GOPATH ?= $(shell go env GOPATH)

ifeq "$(GOPATH)" ""
  $(error Please set the environment variable GOPATH before running `make`)
endif

.PHONY: build
build: clean
	go build -o bin *.go

.PHONY: run
run: build
	./bin -selfNamespace=default -selfService=LaneRouterGatewayService -port=48096

.PHONY: clean
clean:
	rm -f bin
	rm -rf polaris
