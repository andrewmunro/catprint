# catprint — build and deploy on this host (native amd64 Linux).
# Cross-compile for Windows is also provided for the diagnostic scripts.

BIN      := bin/catprint
PKG      := .
GOFLAGS  := -trimpath

.PHONY: build run win scripts install logs status restart clean fmt vet

build: ## Build the server (pure Go, no CGO)
	CGO_ENABLED=0 go build $(GOFLAGS) -o $(BIN) $(PKG)

run: build ## Build and run with .env loaded
	set -a; . ./.env; set +a; ./$(BIN)

win: ## Cross-build the Windows server + scripts
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) -o bin/catprint.exe .
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) -o bin/scan.exe ./scripts/scan
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build $(GOFLAGS) -o bin/test_print.exe ./scripts/test_print

scripts: ## Build diagnostic scripts for this host
	go build -o bin/scan ./scripts/scan
	go build -o bin/test_print ./scripts/test_print
	go build -o bin/test_render ./scripts/test_render

install: build ## Install + start the systemd unit (needs sudo)
	sudo cp systemd/printer-hub.service /etc/systemd/system/printer-hub.service
	sudo systemctl daemon-reload
	sudo systemctl enable --now printer-hub
	@echo "installed. follow logs with: make logs"

restart: build ## Rebuild and restart the running service
	sudo systemctl restart printer-hub

logs: ## Tail the service logs
	journalctl -u printer-hub -f -o cat

status: ## Show service status
	systemctl status printer-hub --no-pager

fmt: ; gofmt -w .
vet: ; go vet ./...

clean: ; rm -rf bin

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-10s %s\n", $$1, $$2}'
