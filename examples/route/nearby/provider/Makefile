GOPATH ?= $(shell go env GOPATH)

# Ensure GOPATH is set before running build process.
ifeq "$(GOPATH)" ""
  $(error Please set the environment variable GOPATH before running `make`)
endif

.PHONY: build
build:
	go build -o provider main.go
	
.PHONY: run
run: build
	./provider
	
.PHONY: clean
clean:
	rm -f provider *.log
	rm -rf polaris
