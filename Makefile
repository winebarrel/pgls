.PHONY: all
all: vet test build

.PHONY: build
build:
	go build .

.PHONY: install
install:
	go install .

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test:
	go test -v -count=1 ./...

.PHONY: bench
bench:
	go test -bench . -run '^$$' -benchtime=2s ./internal/sqlctx/

.PHONY: lint
lint:
	golangci-lint run

.PHONY: clean
clean:
	rm -f pgls
