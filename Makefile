# morse-dsp — build, test and serve.
#
#   make            tests + CLI + WebAssembly
#   make cli        bin/morse-dsp
#   make wasm       web/main.wasm (+ wasm_exec.js matching your Go toolchain)
#   make test       go test -v ./...
#   make serve      build wasm and serve web/ on http://localhost:8080

GO        ?= go
PORT      ?= 8080
BIN_DIR   := bin
CLI       := $(BIN_DIR)/morse-dsp
WEB_DIR   := web
WASM      := $(WEB_DIR)/main.wasm
GOROOT    := $(shell $(GO) env GOROOT)
# Go >= 1.24 ships wasm_exec.js in lib/wasm, older releases in misc/wasm.
WASM_EXEC := $(firstword $(wildcard $(GOROOT)/lib/wasm/wasm_exec.js) $(wildcard $(GOROOT)/misc/wasm/wasm_exec.js))
WASM_RUN  := $(firstword $(wildcard $(GOROOT)/lib/wasm/go_js_wasm_exec) $(wildcard $(GOROOT)/misc/wasm/go_js_wasm_exec))

.PHONY: all build cli wasm wasm-exec test test-wasm bench vet fmt serve clean help

all: test cli wasm

build: cli wasm

cli:
	$(GO) build -trimpath -o $(CLI) ./cmd/morse-dsp

wasm: wasm-exec
	GOOS=js GOARCH=wasm $(GO) build -trimpath -ldflags="-s -w" -o $(WASM) ./cmd/wasm
	@ls -lh $(WASM) | awk '{print "built " $$9 " (" $$5 ")"}'

wasm-exec:
	@test -n "$(WASM_EXEC)" || { echo "wasm_exec.js not found under $(GOROOT)"; exit 1; }
	cp "$(WASM_EXEC)" $(WEB_DIR)/wasm_exec.js

test:
	$(GO) test -v ./...

# Run the core package's tests compiled to WebAssembly (needs Node.js).
# wasm_exec.js limits argv+env to 12 KB, so run with a minimal environment.
GO_BIN    := $(shell command -v $(GO))
NODE_BIN  := $(shell command -v node)
test-wasm:
	@test -n "$(WASM_RUN)" || { echo "go_js_wasm_exec not found under $(GOROOT)"; exit 1; }
	@test -n "$(NODE_BIN)" || { echo "node not found in PATH"; exit 1; }
	env -i PATH="$(dir $(GO_BIN)):$(dir $(NODE_BIN)):/usr/bin:/bin" HOME="$(HOME)" \
		GOCACHE="$(shell $(GO) env GOCACHE)" GOMODCACHE="$(shell $(GO) env GOMODCACHE)" \
		GOOS=js GOARCH=wasm $(GO_BIN) test -exec="$(WASM_RUN)" ./pkg/morse

bench:
	$(GO) test -run '^$$' -bench . ./pkg/morse

vet:
	$(GO) vet ./...
	GOOS=js GOARCH=wasm $(GO) vet ./cmd/wasm ./pkg/morse

fmt:
	gofmt -w cmd pkg

serve: wasm
	@echo "serving $(WEB_DIR)/ on http://localhost:$(PORT)"
	python3 -m http.server $(PORT) --bind 127.0.0.1 --directory $(WEB_DIR)

clean:
	rm -rf $(BIN_DIR) $(WASM)

help:
	@sed -n '1,9p' Makefile
