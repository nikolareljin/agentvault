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
	cp $(APP_NAME) $(GOPATH)/bin/$(APP_NAME)

run: build
	./$(APP_NAME)
