GOPATH ?= $(shell go env GOPATH)

# Ensure GOPATH is set before running build process.
ifeq "$(GOPATH)" ""
  $(error Please set the environment variable GOPATH before running `make`)
endif

.PHONY: build
build: clean
	go build -o bin *.go
	cp bin ./provider-a/
	cp bin ./provider-b/

.PHONY: run
run: build
	./bin

.PHONY: clean
clean:
	rm -f bin provider-a/bin provider-b/bin
	rm -rf polaris provider-a/polaris provider-b/polaris
