APP_NAME  := agentvault
VERSION   := $(shell cat VERSION)
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE      := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS   := -s -w \
  -X github.com/nikolareljin/agentvault/cmd.Version=$(VERSION) \
  -X github.com/nikolareljin/agentvault/cmd.Commit=$(COMMIT) \
  -X github.com/nikolareljin/agentvault/cmd.Date=$(DATE)

# Embedded llama.cpp inference engine (BitNet support)
LLAMA_DIR := $(shell pwd)/third_party/llama
# -lgomp (OpenMP via libgomp) is Linux-specific; omit on macOS where Accelerate provides parallelism.
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
  LLAMA_CGO = CGO_ENABLED=1 \
    CGO_CFLAGS="-I$(LLAMA_DIR)/include" \
    CGO_LDFLAGS="-L$(LLAMA_DIR)/lib -lllama -lggml -lggml-cpu -lc++ -lm"
else
  LLAMA_CGO = CGO_ENABLED=1 \
    CGO_CFLAGS="-I$(LLAMA_DIR)/include" \
    CGO_LDFLAGS="-L$(LLAMA_DIR)/lib -lllama -lggml -lggml-cpu -lstdc++ -lm -lgomp"
endif

.PHONY: build build-llama build-bitnet test lint clean install fmt vet run

build:
	go build -ldflags "$(LDFLAGS)" -o $(APP_NAME) .

# Build llama.cpp static library (one-time, ~5 minutes).
# Set LLAMA_TAG=bNNNN to pin to a specific build tag.
build-llama:
	bash scripts/build-llama.sh

# Build agentvault with embedded BitNet/llama.cpp inference engine.
# Skips build-llama when $(LLAMA_DIR)/lib/libllama.a already exists.
# Note: internal/localllm/llm_cgo.go hardcodes third_party/llama in its #cgo directives,
# so that directory must always be present regardless of LLAMA_DIR. Changing LLAMA_DIR
# affects only the CGO_CFLAGS/CGO_LDFLAGS env vars set here and the build-llama step.
build-bitnet:
	@{ test -f "$(LLAMA_DIR)/lib/libllama.a" && \
	   test -f "$(LLAMA_DIR)/lib/libggml.a" && \
	   test -f "$(LLAMA_DIR)/lib/libggml-cpu.a" && \
	   test -f "$(LLAMA_DIR)/include/llama.h"; } || $(MAKE) build-llama
	$(LLAMA_CGO) go build -tags localllm \
	  -ldflags "$(LDFLAGS)" -o $(APP_NAME)-bitnet .

test:
	go test ./...

lint:
	test -z "$$(gofmt -l .)" && go vet ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -f $(APP_NAME) $(APP_NAME)-bitnet

install: build
	@target_bin="$$(go env GOBIN)"; \
	if [ -z "$$target_bin" ]; then \
		gopath_bin="$$(go env GOPATH)/bin"; \
		if echo ":$$PATH:" | grep -q ":$$gopath_bin:"; then \
			target_bin="$$gopath_bin"; \
		else \
			target_bin="$$HOME/.local/bin"; \
		fi; \
	fi; \
	mkdir -p "$$target_bin"; \
	cp $(APP_NAME) "$$target_bin/$(APP_NAME)"; \
	echo "Installed $(APP_NAME) to $$target_bin/$(APP_NAME)"

run: build
	./$(APP_NAME)
