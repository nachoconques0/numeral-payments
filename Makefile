BANK_FOLDER ?= ./data/bank
SQLITE_DB_FILE_LOCATION ?= ./data/payments.db
ADDR ?= :8080

export BANK_FOLDER
export SQLITE_DB_FILE_LOCATION
export ADDR

.PHONY: run build test fakebank demo clean

## run: start the service with a local ./data folder
run:
	go run ./cmd/server

## build: compile both binaries into ./bin
build:
	go build -o bin/server ./cmd/server
	go build -o bin/fakebank ./cmd/fakebank

## test: run the test suite
test:
	go test ./...

## fakebank: simulate the bank answering PROCESSED
fakebank:
	go run ./cmd/fakebank

## demo: send the provided sample payment
demo:
	curl -i -u $${AUTH_USERNAME:-CALCAGNO}:$${AUTH_PASSWORD:-xxxx} \
		-X POST http://localhost$(ADDR)/payments \
		-H 'Content-Type: application/json' \
		-d @resources/request_sample.json

## clean: remove the local data folder and binaries
clean:
	rm -rf bin data
