# 1loop builds two binaries: GLFW chooses X11 or Wayland at compile time.
# bin/1loop starts bin/1loop-wayland by itself in a Wayland session.
BIN := bin

# go-gl/glfw passes -D_GNU_SOURCE for Wayland while its wl_window.c defines
# it again, and GCC has no switch for that one warning: the Wayland build
# compiles its C without warnings. The X11 build keeps them all.
WAYLAND_CFLAGS := $(shell go env CGO_CFLAGS) -w

.PHONY: build run dev clean

build:
	go build -o $(BIN)/1loop .
	CGO_CFLAGS="$(WAYLAND_CFLAGS)" go build -tags wayland -o $(BIN)/1loop-wayland .

run: build
	./$(BIN)/1loop -workspace . $(ARGS)

# The developer log: everything 1loop does and how long the interface takes,
# in the terminal and in ~/.cache/1loop/dev.log (see devlog.go).
dev: build
	./$(BIN)/1loop -dev -workspace . $(ARGS)

clean:
	rm -rf $(BIN)
