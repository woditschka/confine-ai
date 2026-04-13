GO_VERSION     ?= 1.26.0
CORRETTO_VERSION ?= 25.0.2.10.1

.PHONY: base claude github-copilot opencode all clean

base:
	docker build \
		--build-arg GO_VERSION=$(GO_VERSION) \
		--build-arg CORRETTO_VERSION=$(CORRETTO_VERSION) \
		-t localhost/confine-ai-base:latest \
		base/

claude: base
	docker build -t confine-ai-claude:latest claude/.devcontainer/

github-copilot: base
	docker build -t confine-ai-github-copilot:latest github-copilot/.devcontainer/

opencode: base
	docker build -t confine-ai-opencode:latest opencode/.devcontainer/

all: claude github-copilot opencode

clean:
	-docker rmi confine-ai-claude:latest confine-ai-github-copilot:latest \
		confine-ai-opencode:latest localhost/confine-ai-base:latest
