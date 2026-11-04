BINARY := github-spyglass
APP_NAME := GitHub Spyglass
APP := build/$(APP_NAME).app

.PHONY: build test vet install clean app install-app run-app

build:
	go build -o $(BINARY) ./cmd/github-spyglass

test:
	go test ./...

vet:
	go vet ./...

install:
	go install ./cmd/github-spyglass

app:
	mkdir -p "$(APP)/Contents/MacOS"
	go build -o "$(APP)/Contents/MacOS/github-spyglass-app" ./cmd/github-spyglass-app
	cp packaging/Info.plist "$(APP)/Contents/Info.plist"
	codesign --force -s - "$(APP)"

install-app: app
	mkdir -p "$(HOME)/Applications"
	rm -rf "$(HOME)/Applications/$(APP_NAME).app"
	cp -R "$(APP)" "$(HOME)/Applications/"

run-app: install-app
	open "$(HOME)/Applications/$(APP_NAME).app"

clean:
	rm -f $(BINARY)
	rm -rf build
