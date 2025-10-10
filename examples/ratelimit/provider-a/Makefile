GOPATH ?= $(shell go env GOPATH)

# Ensure GOPATH is set before running build process.
ifeq "$(GOPATH)" ""
  $(error Please set the environment variable GOPATH before running `make`)
endif

.PHONY: build
build: clean
	go build -o bin main.go

.PHONY: run
run: build
	./bin

.PHONY: clean
clean:
	rm -f bin
	rm -rf polaris

