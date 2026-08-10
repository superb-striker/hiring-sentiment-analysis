SERVICES := shared hiringdb ingestion-service coordinator-service worker-service analysis

.PHONY: tidy build test vet fmt fmt-check local-table

tidy:
	@for d in $(SERVICES); do \
		echo "==> go mod tidy in $$d"; \
		(cd $$d && go mod tidy) || exit 1; \
	done

build:
	@for d in $(SERVICES); do \
		echo "==> go build in $$d"; \
		(cd $$d && go build ./...) || exit 1; \
	done

test:
	@for d in $(SERVICES); do \
		echo "==> go test in $$d"; \
		(cd $$d && go test ./...) || exit 1; \
	done

vet:
	@for d in $(SERVICES); do \
		echo "==> go vet in $$d"; \
		(cd $$d && go vet ./...) || exit 1; \
	done

fmt:
	@gofmt -w $(SERVICES)

fmt-check:
	@unformatted=$$(gofmt -l $(SERVICES)); \
	if [ -n "$$unformatted" ]; then \
		echo "Not gofmt'd:"; echo "$$unformatted"; exit 1; \
	fi

