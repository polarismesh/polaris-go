GOPATH ?= $(shell go env GOPATH)

ifeq "$(GOPATH)" ""
  $(error Please set the environment variable GOPATH before running `make`)
endif

SERVER ?=

.PHONY: build
build: clean
	go build -o bin *.go

.PHONY: run
run:
	@if [ -z "$(SERVER)" ]; then \
		echo "Error: SERVER is required"; \
		echo "Usage: make run SERVER=<ip>"; \
		echo "Example: make run SERVER=114.132.192.60"; \
		exit 1; \
	fi
	bash verify.sh -s $(SERVER)

.PHONY: clean
clean:
	rm -f bin group_concurrent_test
	rm -rf polaris
