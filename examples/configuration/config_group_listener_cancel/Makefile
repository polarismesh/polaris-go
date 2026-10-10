GO ?= go
SERVER ?= $(POLARIS_SERVER)
ARGS ?=
CLEAN_ARGS ?=

.PHONY: build build-x86 test e2e run clean

build:
	mkdir -p .build
	$(GO) build -o .build/config-group-listener-cancel .

build-x86:
	mkdir -p .build
	GOOS=linux GOARCH=amd64 $(GO) build -o .build/config-group-listener-cancel-linux-amd64 .

test:
	$(GO) test -race -count=1 -v ./...

e2e run:
	GO="$(GO)" bash verify.sh -server "$(SERVER)" $(ARGS)

clean:
	bash cleanup.sh -f $(CLEAN_ARGS)
