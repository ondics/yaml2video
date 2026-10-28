# Makefile for yaml2video
# Copyright (c) 2026 Ondics GmbH

DOCKER_IMAGE ?= yaml2video:local
DC ?= docker compose
ARGS ?= --help
MOUNT_DIR ?= .

export DOCKER_IMAGE MOUNT_DIR

.PHONY: help up down down-volumes build bash test fonts

.DEFAULT_GOAL := help

help: ## Show this help
	@echo "yaml2video - make helpers"
	@echo "Commands: make ..."
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

up: ## Start the API server (MOUNT_DIR=./ mounted at /data)
	@$(DC) up -d

down: ## Take down the yaml2video API server
	@$(DC) down

cli: ## Run yaml2video with ARGS in the container (ARGS="--help")
	@$(DC) run --rm -v "$(CURDIR):/data" yaml2video $(ARGS)

fonts: ARGS =
fonts: ## List font families (OFFLINE=1 for offline families)
	@$(DC) run --rm -v "$(CURDIR):/data" yaml2video fonts $(if $(filter 1,$(OFFLINE)),--offline-only) $(ARGS)

render: up ## FILE=... DIR=(Default: dir of FILE)
	@test -n "$(FILE)" || { echo "Error: FILE is required" >&2; exit 1; }
	@curl -sSN --get http://localhost:8080/render/local \
		--data-urlencode "path=$(FILE)" \
		$(if $(DIR),--data-urlencode "dir=$(DIR)") \
		| sed -n -e 's/^data: .*"download":"\([^"]*\)".*/\1/p' \
				-e 's/^[[:space:]]*{[[:space:]]*"error"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'

check: ## Checks a yaml schema (FILE=... DIR=(Default: dir of FILE))
	test -n "$(FILE)"
	@$(DC) run --rm --volume "$(CURDIR):/data" yaml2video -n $(FILE) --work-dir $()

logs: ## Show logs (stop with ctrl-c)
	$(DC) logs -f -t --tail=100

ps: ## What's up?
	$(DC) ps

config: ## Show composed docker-compose config
	$(DC) config

build: ## Build the yaml2video Docker image
	$(DC) build yaml2video

bash: ## Open Bash with the current directory mounted at /data
	$(DC) run --rm --entrypoint /bin/bash --volume "$(CURDIR):/data" yaml2video

test: ## Run all Go tests
	@$(DC) run --rm --entrypoint go --workdir /src --volume "$(CURDIR):/src" yaml2video test ./...
