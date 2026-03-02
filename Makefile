APP_NAME := agentvault
VERSION  := $(shell cat VERSION)
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE     := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS  := -s -w \
  -X github.com/nikolareljin/agentvault/cmd.Version=$(VERSION) \
  -X github.com/nikolareljin/agentvault/cmd.Commit=$(COMMIT) \
  -X github.com/nikolareljin/agentvault/cmd.Date=$(DATE)

.PHONY: build test lint clean install fmt vet run

build:
	go build -ldflags "$(LDFLAGS)" -o $(APP_NAME) .

test:
	go test ./...

lint:
	test -z "$$(gofmt -l .)" && go vet ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -f $(APP_NAME)

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
