SERVER_BIN := ./bin/virgil-server
CLI_BIN    := ./bin/virgil

.PHONY: build build-server build-cli install-cli restart logs status stop start tail-smee test

build: build-server build-cli

build-server:
	go build -o $(SERVER_BIN) ./cmd/server

build-cli:
	go build -o $(CLI_BIN) ./cmd/virgil

# Install the CLI to ~/.local/bin so `virgil` is on PATH.
install-cli: build-cli
	install -D $(CLI_BIN) $$HOME/.local/bin/virgil
	@echo "installed to $$HOME/.local/bin/virgil"

restart: build-server
	systemctl --user restart virgil-server

start:
	systemctl --user start virgil-server smee-client

stop:
	systemctl --user stop virgil-server smee-client

status:
	systemctl --user status virgil-server smee-client --no-pager

logs:
	journalctl --user -u virgil-server -f -o cat

tail-smee:
	journalctl --user -u smee-client -f -o cat

test:
	go test ./...
