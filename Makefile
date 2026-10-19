# Shims onto ./dev, the entry point. The commands live in scripts/project.sh.

.PHONY: build build-llama build-bitnet test lint clean install fmt vet run preflight doctor

build:
	./dev build

# Build the llama.cpp static library (one-time, ~5 minutes).
# Set LLAMA_TAG=bNNNN to pin a build tag.
build-llama:
	bash scripts/build-llama.sh

# agentvault with the embedded BitNet/llama.cpp engine -> ./agentvault-bitnet.
# internal/localllm/llm_cgo.go hardcodes third_party/llama in its #cgo lines.
build-bitnet:
	./dev build bitnet

test:
	./dev test

lint:
	./dev lint

fmt:
	./dev format

# Plain go vet for this OS only; ./dev lint also vets darwin and windows.
vet:
	go vet ./...

clean:
	./dev clean

install:
	./dev install-bin

run:
	./dev run

preflight:
	./dev preflight

doctor:
	./dev doctor
