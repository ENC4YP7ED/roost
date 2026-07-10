VERSION ?= $(shell date +%Y.%m.%d)

.PHONY: all frontend dbviewer backend check test test-go test-frontend test-e2e clean dev release

all: frontend backend

frontend: dbviewer
	cd frontend && npm install --no-audit --no-fund && npm run build
	rm -rf backend/web/dist
	cp -r frontend/dist backend/web/dist

dbviewer:
	cd dbviewer-frontend && npm install --no-audit --no-fund && npm run build
	rm -rf backend/web/dbviewer
	cp -r dbviewer-frontend/dist backend/web/dbviewer

backend:
	cd backend && go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o roost .

check:
	cd backend && go vet ./...
	cd frontend && npx tsc --noEmit
	cd dbviewer-frontend && npx tsc --noEmit

## Run every test suite: Go (race), frontend units, browser end-to-end.
test: test-go test-frontend test-e2e

test-go:
	cd backend && go test ./... -race -count=1

test-frontend:
	cd frontend && npm run test

## Boots the built binary on a throwaway database and drives a real browser.
test-e2e: backend
	cd e2e && npm install --no-audit --no-fund && npx playwright install --with-deps chromium && npx playwright test

## Cross-compiled release binaries.
release: frontend
	cd backend && CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../dist-bin/roost-linux-amd64 .
	cd backend && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../dist-bin/roost-windows-amd64.exe .

dev:
	cd backend && go run . -db dev.db

clean:
	rm -rf frontend/dist dbviewer-frontend/dist backend/roost dist-bin e2e/test-results
