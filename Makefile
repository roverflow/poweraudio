BINARY = poweraudio
VERSION = $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS = -ldflags "-X main.version=$(VERSION)"
PREFIX ?= $(HOME)/.local

.PHONY: build install uninstall purge clean

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/poweraudio

install: build
	install -Dm755 $(BINARY) $(PREFIX)/bin/$(BINARY)
	install -Dm644 configs/poweraudio.service $(HOME)/.config/systemd/user/poweraudio.service
	@echo ""
	@echo "Installed. Run:"
	@echo "  systemctl --user daemon-reload"
	@echo "  systemctl --user enable --now poweraudio"

uninstall:
	systemctl --user disable --now poweraudio 2>/dev/null || true
	-pkill -f "poweraudio --daemon" 2>/dev/null || true
	rm -f $(PREFIX)/bin/$(BINARY)
	rm -f $(HOME)/.config/systemd/user/poweraudio.service
	systemctl --user daemon-reload 2>/dev/null || true
	rm -f $${XDG_RUNTIME_DIR:-/run/user/$$(id -u)}/poweraudio.sock

purge: uninstall
	rm -rf $${XDG_CONFIG_HOME:-$(HOME)/.config}/poweraudio

clean:
	rm -f $(BINARY)
