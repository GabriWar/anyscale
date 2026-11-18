PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
DATADIR ?= $(PREFIX)/share
DESTDIR ?=
GO ?= go
VERSION ?= $(shell git describe --tags --always 2>/dev/null)
ID := io.github.gabriwar.Anyscale

.PHONY: all build test install uninstall clean

all: build

# anyscale is the GUI, anyscaled the daemon (forked from anyscale in
# daemon/), and anyscale the daemon's CLI.
build:
	$(GO) build -trimpath -ldflags="-s -w -X 'github.com/gabriwar/anyscale/internal/metadata.version=$(VERSION)'" -o anyscale ./cmd/anyscale
	cd daemon && $(GO) build -trimpath -o ../anyscaled ./cmd/anyscaled
	cd daemon && $(GO) build -trimpath -o ../anyscalectl ./cmd/anyscalectl

test:
	$(GO) test ./...
	cd daemon && $(GO) test ./cmd/anyscaled/ ./controlapi/

install: build
	install -Dm755 -t $(DESTDIR)$(BINDIR) anyscale anyscaled anyscalectl
	install -Dm644 data/$(ID).png $(DESTDIR)$(DATADIR)/icons/hicolor/256x256/apps/$(ID).png
	install -Dm644 -t $(DESTDIR)$(DATADIR)/applications data/$(ID).desktop
	install -Dm644 -t $(DESTDIR)$(DATADIR)/metainfo data/$(ID).metainfo.xml
	install -Dm644 -t $(DESTDIR)$(DATADIR)/glib-2.0/schemas data/$(ID).gschema.xml
	glib-compile-schemas $(DESTDIR)$(DATADIR)/glib-2.0/schemas
	install -d $(DESTDIR)/etc/systemd/system $(DESTDIR)/usr/share/polkit-1/actions
	sed 's#@BINDIR@#$(BINDIR)#g' dist/anyscaled.service > $(DESTDIR)/etc/systemd/system/anyscaled.service
	sed 's#@BINDIR@#$(BINDIR)#g' dist/$(ID).policy.in > $(DESTDIR)/usr/share/polkit-1/actions/$(ID).policy

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/anyscale $(DESTDIR)$(BINDIR)/anyscaled $(DESTDIR)$(BINDIR)/anyscalectl
	rm -f $(DESTDIR)$(DATADIR)/icons/hicolor/256x256/apps/$(ID).png $(DESTDIR)$(DATADIR)/applications/$(ID).desktop
	rm -f $(DESTDIR)$(DATADIR)/metainfo/$(ID).metainfo.xml $(DESTDIR)$(DATADIR)/glib-2.0/schemas/$(ID).gschema.xml
	rm -f $(DESTDIR)/etc/systemd/system/anyscaled.service $(DESTDIR)/usr/share/polkit-1/actions/$(ID).policy

clean:
	rm -f anyscale anyscaled anyscalectl
