GOPATH ?= $(shell go env GOPATH)

ifeq "$(GOPATH)" ""
  $(error Please set the environment variable GOPATH before running `make`)
endif

.PHONY: build
build: clean
	go build -o bin *.go

.PHONY: run
run: build
	./bin -namespace=default -service=MixedRouteEchoServer -selfService=MixedRouteEchoClient -port=18180

.PHONY: clean
clean:
	rm -f bin
	rm -rf polaris
