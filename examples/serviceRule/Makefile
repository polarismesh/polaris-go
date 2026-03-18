GOPATH ?= $(shell go env GOPATH)

# Ensure GOPATH is set before running build process.
ifeq "$(GOPATH)" ""
  $(error Please set the environment variable GOPATH before running `make`)
endif

.PHONY: build
build: clean
	go build -o bin *.go

.PHONY: build-linux
build-linux: clean
	GOOS=linux GOARCH=amd64 go build -o x86-bin *.go

.PHONY: run
run: build
	./bin

.PHONY: clean
clean:
	rm -f bin x86-bin
	rm -rf polaris

