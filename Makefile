VERSION ?= $(shell date +%Y.%m.%d)

.PHONY: all frontend dbviewer backend check test test-go test-dbviewer test-frontend test-e2e clean dev release

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
test: test-go test-dbviewer test-frontend test-e2e

test-go:
	cd backend && go test ./... -race -count=1

## Database-viewer integration tests: bring up MariaDB, run, tear down.
test-dbviewer:
	docker rm -f roost-mariadb-test 2>/dev/null || true
	docker run -d --name roost-mariadb-test -e MARIADB_ROOT_PASSWORD=testpw -p 13306:3306 mariadb:11
	@echo "waiting for MariaDB..."; 	for i in $$(seq 1 60); do docker exec roost-mariadb-test mariadb -uroot -ptestpw -e "SELECT 1" >/dev/null 2>&1 && break; sleep 1; done
	cd backend && ROOST_TEST_MYSQL=127.0.0.1:13306 ROOST_TEST_MYSQL_PW=testpw go test ./internal/dbviewer/... -cover; 	status=$$?; docker rm -f roost-mariadb-test >/dev/null 2>&1; exit $$status

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
