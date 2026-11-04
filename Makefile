BINARY := github-spyglass
APP_NAME := GitHub Spyglass
APP := build/$(APP_NAME).app

.PHONY: build test vet check install clean app install-app run-app

build:
	go build -o $(BINARY) ./cmd/github-spyglass

test:
	go test ./...

vet:
	go vet ./...

check: vet test

install:
	go install ./cmd/github-spyglass

ICONSET := build/AppIcon.iconset
ICON := cmd/github-spyglass-app/icon.png

app:
	mkdir -p "$(APP)/Contents/MacOS" "$(APP)/Contents/Resources"
	go build -o "$(APP)/Contents/MacOS/github-spyglass-app" ./cmd/github-spyglass-app
	cp packaging/Info.plist "$(APP)/Contents/Info.plist"
	rm -rf $(ICONSET) && mkdir -p $(ICONSET)
	sips -z 1024 1024 $(ICON) --out build/icon-1024.png >/dev/null
	sips -z 16 16 build/icon-1024.png --out $(ICONSET)/icon_16x16.png >/dev/null
	sips -z 32 32 build/icon-1024.png --out $(ICONSET)/icon_16x16@2x.png >/dev/null
	sips -z 32 32 build/icon-1024.png --out $(ICONSET)/icon_32x32.png >/dev/null
	sips -z 64 64 build/icon-1024.png --out $(ICONSET)/icon_32x32@2x.png >/dev/null
	sips -z 128 128 build/icon-1024.png --out $(ICONSET)/icon_128x128.png >/dev/null
	sips -z 256 256 build/icon-1024.png --out $(ICONSET)/icon_128x128@2x.png >/dev/null
	sips -z 256 256 build/icon-1024.png --out $(ICONSET)/icon_256x256.png >/dev/null
	sips -z 512 512 build/icon-1024.png --out $(ICONSET)/icon_256x256@2x.png >/dev/null
	sips -z 512 512 build/icon-1024.png --out $(ICONSET)/icon_512x512.png >/dev/null
	cp build/icon-1024.png $(ICONSET)/icon_512x512@2x.png
	iconutil -c icns $(ICONSET) -o "$(APP)/Contents/Resources/AppIcon.icns"
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
