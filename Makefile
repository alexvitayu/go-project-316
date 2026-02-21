.PHONY: run build test lint

BINARY_NAME=hexlet-go-crawler
CMD_DIR=./cmd/hexlet-go-crawler
BUILD_DIR=./bin

# Цвета для вывода
GREEN=\033[0;32m
RED=\033[0;31m
NC=\033[0m # No Color

# Собирает бинарный файл в bin/hexlet-go-crawler
build:
	@echo "${GREEN}Сборка приложения...${NC}"
	go mod download
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)
	@echo "${GREEN}✓ Сборка завершена: $(BUILD_DIR)/$(BINARY_NAME)${NC}"

# Запуск программы
run: build
	@echo "${GREEN}Запуск приложения...${NC}"
	@if [ -z "$(URL)" ]; then \
		echo "${RED}Ошибка: URL не указан!${NC}"; \
		echo "${YELLOW}Использование: make run URL=<адрес>${NC}"; \
		exit 1; \
	fi
	go run ./cmd/hexlet-go-crawler/main.go $(URL)

# Запуск тестов
test:
	@echo "${GREEN}Запуск тестов...${NC}"
	go test ./... -v -race -cover


# Запуск линтера (использует .golangci.yml)
lint:
	@echo "${GREEN}Запуск линтера...${NC}"
	golangci-lint run ./...
