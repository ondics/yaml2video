#
# Makefile für yaml2video
# (C) Ondics GmbH, 2026
#
DOCKER_IMAGE ?= yaml2video:local
DC ?= docker compose
ARGS ?= -t examples/simple/template-simple.yaml -o examples/simple/output.mp4 examples/simple/video-simple.yaml
DOCKER_ARGS ?= $(ARGS)

export DOCKER_IMAGE

.PHONY: help up run build install test docker-build docker-run

help:
	@echo "# yaml2video make helpers"
	@echo Commands: make ...
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help

up: ## Start the serving stack.
	 @$(DC) -f docker-compose.serve.yaml up -d

down: ## Take down the serving stack.
	@$(DC) -f docker-compose.serve.yaml down

down-volumes: ## Take down the serving stack.
	@$(DC) -f docker-compose.serve.yaml down --volumes

logs: ## Show logs (stop with ctrl-c)
	${DC} logs -f -t --tail=100

ps: ## What's up?
	${DC} ps

bash: ## Shell in cogpt container (optional: CMD="...")
	${DC} exec yaml2video bash ${CMD}

config: ## Show composed docker-compose config
	${DC} config

run: ## Run yaml2video with ARGS in the development container.
	 @$(DC) -f docker-compose.dev.yaml run --rm --build development run ./src/cmd/yaml2video $(ARGS)

build: ## Build yaml2video in the development container.
	 @$(DC) -f docker-compose.dev.yaml run --rm --build development build ./src/cmd/yaml2video

install: ## Install yaml2video in the development container.
	 @$(DC) -f docker-compose.dev.yaml run --rm --build development install ./src/cmd/yaml2video

test: ## Run all Go tests in the development container.
	 @$(DC) -f docker-compose.dev.yamlrun --rm --build development test ./...

docker-build: ## Build the yaml2video Docker image.
	 $(DC) build yaml2video

docker-run: ## Run the yaml2video Docker image with DOCKER_ARGS.
	 $(DC) run --rm --build yaml2video $(DOCKER_ARGS)
