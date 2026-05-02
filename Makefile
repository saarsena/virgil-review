BIN := ./bin/virgil-server
SRC := ./cmd/server

.PHONY: build restart logs status stop start tail-smee

build:
	go build -o $(BIN) $(SRC)

restart: build
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
