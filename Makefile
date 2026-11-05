VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PORT    ?= 8090

.PHONY: build test run install uninstall clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o admin .

test:
	go vet ./...
	go test ./...

run: build
	./admin run -config ./config.json

# sudo make install PORT=9000
install: build
	./admin install -port $(PORT)

uninstall:
	/usr/local/bin/admin uninstall

clean:
	rm -f admin
