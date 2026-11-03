BINARY := github-spyglass

.PHONY: build test vet install clean

build:
	go build -o $(BINARY) ./cmd/github-spyglass

test:
	go test ./...

vet:
	go vet ./...

install:
	go install ./cmd/github-spyglass

clean:
	rm -f $(BINARY)
