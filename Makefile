# Makefile mKey: сборка, тесты, линтеры и вспомогательные команды (AGENTS.md §3).

# Go может быть не в PATH (установлен в /usr/local/go): добавляем стандартные каталоги.
export PATH := $(PATH):/usr/local/go/bin:$(HOME)/go/bin
# Ядро собирается без cgo: один статический бинарник (D2, NFR-5).
export CGO_ENABLED ?= 0

# Сведения о сборке для mkey version.
VERSION ?= $(shell git describe --tags 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/khameleonium/mKey/internal/lib/buildinfo.Version=$(VERSION) \
	-X github.com/khameleonium/mKey/internal/lib/buildinfo.Commit=$(COMMIT) \
	-X github.com/khameleonium/mKey/internal/lib/buildinfo.Date=$(DATE)

# Каталоги.
WEB := web
BIN := mkey
BIN_CLI := mkey-cli

.PHONY: all build build-cli go-build web web-deps test go-test web-test test-integration lint go-lint web-lint fmt run new-module gencodes clean help e2e

## all: то же, что build
all: build

## build: собрать фронтенд, полную версию ./mkey и консольную ./mkey-cli
build: web go-build build-cli

## go-build: собрать только бинарник (фронтенд берётся как есть или заглушка)
go-build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/mkey

## build-cli: собрать консольную версию ./mkey-cli — без окна и значка в трее (ADR-0023, Node не нужен)
build-cli:
	go build -tags nogui -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_CLI) ./cmd/mkey

## web: собрать фронтенд в web/dist
web: web-deps
	cd $(WEB) && npm run build

# Зависимости фронтенда ставятся заново, только если изменился package-lock.json.
web-deps: $(WEB)/node_modules/.installed
$(WEB)/node_modules/.installed: $(WEB)/package-lock.json
	cd $(WEB) && npm ci
	touch $@

## test: unit-тесты Go и фронтенда (без прав и устройств)
test: go-test web-test

go-test:
	go test ./...
	go test -tags nogui ./cmd/... ./internal/app/... ./internal/api/... ./internal/setup/...

web-test: web-deps
	cd $(WEB) && npm test

## test-integration: интеграционные тесты с реальным /dev/uinput
test-integration:
	go test -tags integration ./test/integration/... ./internal/output/...

## e2e: автотесты окна (Playwright) на демоне без настоящих устройств (--fake-backends);
##      браузер: npx playwright install chromium или MKEY_E2E_CHANNEL=chrome (установленный Chrome)
e2e: build
	cd web && npm run e2e

## lint: все линтеры Go и фронтенда (включая архитектурный тест границ модулей)
lint: go-lint web-lint

go-lint:
	go vet ./...
	go vet -tags nogui ./...
	golangci-lint run ./...
	go test ./internal/archtest/

web-lint: web-deps
	cd $(WEB) && npm run check && npm run lint

## fmt: отформатировать Go и фронтенд
fmt: web-deps
	golangci-lint fmt ./...
	cd $(WEB) && npm run format

## run: запустить mkey (демон появится в фазе 2, пока — справка)
run: go-build
	./$(BIN)

## new-module: создать каркас модуля: make new-module NAME=<id>
new-module:
	@if [ -z "$(NAME)" ]; then echo "usage: make new-module NAME=<id>"; exit 1; fi
	go run ./tools/newmodule -name $(NAME)

## gencodes: перегенерировать коды событий из linux/input-event-codes.h
gencodes:
	go run ./tools/gencodes

## clean: удалить бинарник и собранный фронтенд
clean:
	rm -f $(BIN)
	find $(WEB)/dist -mindepth 1 ! -name .gitkeep -delete 2>/dev/null || true

## help: список команд
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
