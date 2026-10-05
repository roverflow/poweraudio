BINARY = poweraudio
# VERSION comes from the nearest vX.Y.Z tag. Without tags it is empty and
# the binary uses what Go recorded.
VERSION = $(shell git describe --tags --match 'v[0-9]*' --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS = -ldflags "-X github.com/roverflow/poweraudio/internal/version.stamped=$(VERSION)"
PREFIX ?= $(HOME)/.local

.PHONY: build install uninstall purge clean version

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/poweraudio

install: build
	install -Dm755 $(BINARY) $(PREFIX)/bin/$(BINARY)
	install -Dm644 configs/poweraudio.service $(HOME)/.config/systemd/user/poweraudio.service
	@if sudo -n install -Dm644 configs/70-poweraudio.rules /etc/udev/rules.d/70-poweraudio.rules && \
		sudo -n udevadm control --reload-rules && \
		sudo -n udevadm trigger --subsystem-match=hidraw; then \
		echo "Installed the Barracuda earcup udev rule"; \
	else \
		echo ""; \
		echo "The earcup watcher cannot open the dongle until this rule is installed:"; \
		echo "  sudo install -Dm644 configs/70-poweraudio.rules /etc/udev/rules.d/70-poweraudio.rules"; \
		echo "  sudo udevadm control --reload-rules && sudo udevadm trigger --subsystem-match=hidraw"; \
	fi
	@echo ""
	@echo "Installed. Run:"
	@echo "  systemctl --user daemon-reload"
	@echo "  systemctl --user enable --now poweraudio"

uninstall:
	systemctl --user disable --now poweraudio 2>/dev/null || true
	-pkill -f '^[^ ]*poweraudio( [^ ]+)* (--daemon|daemon)( |$$)' 2>/dev/null || true
	rm -f $(PREFIX)/bin/$(BINARY)
	rm -f $(HOME)/.config/systemd/user/poweraudio.service
	systemctl --user daemon-reload 2>/dev/null || true
	rm -f $${XDG_RUNTIME_DIR:-/run/user/$$(id -u)}/poweraudio.sock
	rm -f $${XDG_RUNTIME_DIR:-/run/user/$$(id -u)}/poweraudio-earcups

purge: uninstall
	rm -rf $${XDG_CONFIG_HOME:-$(HOME)/.config}/poweraudio

clean:
	rm -f $(BINARY)

version:
	@echo $(or $(VERSION),dev)
