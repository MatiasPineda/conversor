VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X conversor/internal/cli.Version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/arm64 darwin/amd64

.PHONY: build app test install install-app dist dist-app app-windows app-macos clean

# The CLI for this machine, in the repo root.
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o conversor ./cmd/conversor

# The desktop app for this machine. Needs cgo and, on Linux, the OpenGL
# and X11 headers: sudo apt-get install libgl1-mesa-dev xorg-dev libxkbcommon-dev
# Linux builds use GLFW's X11 backend (-tags x11), which also runs on
# Wayland desktops through XWayland and needs no Wayland headers.
APPTAGS := $(if $(filter linux,$(shell go env GOOS)),-tags x11,)
app:
	CGO_ENABLED=1 go build $(APPTAGS) -trimpath -ldflags "$(LDFLAGS)" -o conversor-app ./cmd/conversor-app

# Everything but the app's main package, which needs cgo and OpenGL
# headers; its windows are tested headless in internal/gui.
PKGS = $(shell go list ./... | grep -v /cmd/conversor-app)
test:
	go vet $(PKGS)
	go test $(PKGS)

# Installs to ~/.local/bin (override with PREFIX=/usr/local).
PREFIX ?= $(HOME)/.local
install: build
	install -Dm755 conversor $(PREFIX)/bin/conversor

install-app: build app
	install -Dm755 conversor $(PREFIX)/bin/conversor
	install -Dm755 conversor-app $(PREFIX)/bin/conversor-app

# The CLI for every platform, into dist/<os>-<arch>/. Windows also gets
# conversorw.exe, the same program without a console window, which the
# Explorer menu runs when the app isn't installed.
dist:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "$$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$$os-$$arch/conversor$$ext ./cmd/conversor || exit 1; \
		if [ $$os = windows ]; then \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS) -H=windowsgui" -o dist/$$os-$$arch/conversorw.exe ./cmd/conversor || exit 1; \
		fi; \
		cp LICENSE README.md dist/$$os-$$arch/; \
	done

# The desktop app for the platforms this machine can build: its own, plus
# Windows when mingw-w64 is installed (sudo apt-get install gcc-mingw-w64-x86-64).
# macOS apps must be built on a Mac: make app-macos.
dist-app: dist
	CGO_ENABLED=1 go build $(APPTAGS) -trimpath -ldflags "$(LDFLAGS)" -o dist/$$(go env GOOS)-$$(go env GOARCH)/conversor-app ./cmd/conversor-app
	@if command -v x86_64-w64-mingw32-gcc >/dev/null; then $(MAKE) app-windows; else echo "skipping Windows app: no mingw-w64"; fi

app-windows:
	cd cmd/conversor-app && go run github.com/tc-hib/go-winres@v0.3.3 simply --icon Icon.png --product-name Conversor --file-description "Convert files locally"
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
		go build -trimpath -ldflags "$(LDFLAGS) -H=windowsgui" -o dist/windows-amd64/conversor-app.exe ./cmd/conversor-app
	rm -f cmd/conversor-app/rsrc_windows_*.syso

# On a Mac: Conversor.app with the CLI inside, in dist/darwin-<arch>/.
app-macos:
	go install fyne.io/tools/cmd/fyne@latest
	cd cmd/conversor-app && fyne package -os darwin --release
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o cmd/conversor-app/Conversor.app/Contents/MacOS/conversor ./cmd/conversor
	mkdir -p dist/darwin-$$(go env GOARCH) && rm -rf dist/darwin-$$(go env GOARCH)/Conversor.app
	mv cmd/conversor-app/Conversor.app dist/darwin-$$(go env GOARCH)/

clean:
	rm -rf dist conversor conversor.exe conversorw.exe conversor-app conversor-app.exe
