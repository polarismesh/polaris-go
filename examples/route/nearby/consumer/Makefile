GOPATH ?= $(shell go env GOPATH)

# Ensure GOPATH is set before running build process.
ifeq "$(GOPATH)" ""
  $(error Please set the environment variable GOPATH before running `make`)
endif

.PHONY: build
build: clean
	go build -o consumer main.go

.PHONY: run
run: build
	./consumer

.PHONY: clean
clean:
	rm -f consumer
	rm -rf polaris

