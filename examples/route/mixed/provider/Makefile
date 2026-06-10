GOPATH ?= $(shell go env GOPATH)

ifeq "$(GOPATH)" ""
  $(error Please set the environment variable GOPATH before running `make`)
endif

.PHONY: build
build: clean
	go build -o bin *.go

.PHONY: run
run: build
	./bin -namespace=default -service=MixedRouteEchoServer -port=28181 -metadata=env=dev

.PHONY: clean
clean:
	rm -f bin
	rm -rf polaris
