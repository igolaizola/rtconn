PACKAGE = github.com/igolaizola/rtconn

test:
	go test -v $(PACKAGE) -race

sanity-check:
	golangci-lint run ./...
