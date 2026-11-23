PKGS := ./cmd/... ./internal/...

check: ## vet + tests con -race
	gofmt -l cmd internal | (! grep .)
	go vet $(PKGS)
	go test -race $(PKGS)

build: ## fabrica (con la página embebida) y fabrica-consola
	go build -o fabrica ./cmd/fabrica
	go build -o fabrica-consola ./cmd/fabrica-consola
