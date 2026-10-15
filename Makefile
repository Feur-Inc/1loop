# The desktop builds two binaries: GLFW chooses X11 or Wayland at compile time.
# bin/1loop starts bin/1loop-wayland by itself in a Wayland session.
BIN  := bin
DIST := dist

# go-gl/glfw passes -D_GNU_SOURCE for Wayland while its wl_window.c defines
# it again, and GCC has no switch for that one warning: the Wayland build
# compiles its C without warnings. The X11 build keeps them all.
WAYLAND_CFLAGS := $(shell go env CGO_CFLAGS) -w

# Release builds: no local paths and no debug symbols in the binaries.
RELEASE_FLAGS := -trimpath -ldflags="-s -w"
VERSION       := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build run dev server release clean

build:
	go build -o $(BIN)/1loop ./cmd/1loop
	CGO_CFLAGS="$(WAYLAND_CFLAGS)" go build -tags wayland -o $(BIN)/1loop-wayland ./cmd/1loop

run: build
	./$(BIN)/1loop -workspace . $(ARGS)

# The developer log: everything 1loop does and how long the interface takes,
# in the terminal and in ~/.cache/1loop/dev.log (see devlog.go).
dev: build
	./$(BIN)/1loop -dev -workspace . $(ARGS)

# The server does not import the desktop (Fyne, GLFW, Computer Use): no
# display or CGO needed.
server:
	CGO_ENABLED=0 go build -o $(BIN)/1loop-server ./cmd/1loop-server

# Everything to hand out, in dist/: the Linux desktop (X11 and Wayland) and
# the headless server in one archive, and the phone app's release APK.
release:
	go build $(RELEASE_FLAGS) -o $(BIN)/1loop ./cmd/1loop
	CGO_CFLAGS="$(WAYLAND_CFLAGS)" go build $(RELEASE_FLAGS) -tags wayland -o $(BIN)/1loop-wayland ./cmd/1loop
	CGO_ENABLED=0 go build $(RELEASE_FLAGS) -o $(BIN)/1loop-server ./cmd/1loop-server
	cd android && ./gradlew -q assembleRelease
	rm -rf $(DIST) && mkdir -p $(DIST)/1loop-$(VERSION)
	cp $(BIN)/1loop $(BIN)/1loop-wayland $(BIN)/1loop-server README.md docs/SERVER.md $(DIST)/1loop-$(VERSION)/
	tar -C $(DIST) -czf $(DIST)/1loop-$(VERSION)-linux-x86_64.tar.gz 1loop-$(VERSION)
	rm -rf $(DIST)/1loop-$(VERSION)
	cp android/app/build/outputs/apk/release/app-release.apk $(DIST)/1loop-remote-$(VERSION).apk

clean:
	rm -rf $(BIN) $(DIST)
