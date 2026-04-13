# Hardened dev base: auditable minimal image, no pre-built agent images
# confine-ai:managed tool=base-image kind=image
FROM debian:bookworm-slim

# ---------------------------------------------------------------------------
# Pinned tool versions and checksums
#
# All external tarballs are pinned by SHA-256 so a compromised mirror or
# tampered download fails the build instead of shipping into dev containers.
# The `confine-ai:managed` tags let `confine-ai update` bump these in lockstep.
# ---------------------------------------------------------------------------

# confine-ai:managed tool=go kind=version
ARG GO_VERSION=1.26.2
# confine-ai:managed tool=go kind=sha256 arch=amd64
ARG GO_SHA256_AMD64=990e6b4bbba816dc3ee129eaeaf4b42f17c2800b88a2166c265ac1a200262282
# confine-ai:managed tool=go kind=sha256 arch=arm64
ARG GO_SHA256_ARM64=c958a1fe1b361391db163a485e21f5f228142d6f8b584f6bef89b26f66dc5b23

# confine-ai:managed tool=java kind=version distribution=corretto
ARG CORRETTO_VERSION=25.0.2.10.1
# confine-ai:managed tool=java kind=sha256 arch=amd64 distribution=corretto
ARG CORRETTO_SHA256_AMD64=313e9921e573cf28a4876ab039d56b3a142e7b1b1e847b0dddd170b8dee80387
# confine-ai:managed tool=java kind=sha256 arch=arm64 distribution=corretto
ARG CORRETTO_SHA256_ARM64=6e966b3c3609c25f40e29d6cdb81f83f52a3723c8196a4c38e0d5d03e463c4e5

# System packages (no-install-recommends = minimal attack surface)
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl git make gcc libc6-dev ripgrep jq unzip python3 \
    iptables \
    && rm -rf /var/lib/apt/lists/*

# Go
RUN arch="$(dpkg --print-architecture)" \
    && case "$arch" in \
         amd64) expected_sha="${GO_SHA256_AMD64}" ;; \
         arm64) expected_sha="${GO_SHA256_ARM64}" ;; \
         *) echo "Unsupported architecture: $arch" && exit 1 ;; \
       esac \
    && curl -fsSL -o /tmp/go.tar.gz "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" \
    && echo "${expected_sha}  /tmp/go.tar.gz" | sha256sum -c - \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz
ENV PATH="/usr/local/go/bin:${PATH}"

# Java (Amazon Corretto)
RUN arch="$(dpkg --print-architecture)" \
    && case "$arch" in \
         amd64) corretto_arch="x64"; expected_sha="${CORRETTO_SHA256_AMD64}" ;; \
         arm64) corretto_arch="aarch64"; expected_sha="${CORRETTO_SHA256_ARM64}" ;; \
         *) echo "Unsupported architecture: $arch" && exit 1 ;; \
       esac \
    && curl -fsSL -o /tmp/corretto.tar.gz "https://corretto.aws/downloads/resources/${CORRETTO_VERSION}/amazon-corretto-${CORRETTO_VERSION}-linux-${corretto_arch}.tar.gz" \
    && echo "${expected_sha}  /tmp/corretto.tar.gz" | sha256sum -c - \
    && tar -C /usr/local -xzf /tmp/corretto.tar.gz \
    && rm /tmp/corretto.tar.gz \
    && ln -s /usr/local/amazon-corretto-* /usr/local/java
ENV JAVA_HOME="/usr/local/java"
ENV PATH="${JAVA_HOME}/bin:${PATH}"

# Non-root user
RUN useradd -m -s /bin/bash dev
USER dev
ENV GOPATH="/home/dev/go"
ENV PATH="${GOPATH}/bin:/home/dev/.local/bin:${PATH}"

WORKDIR /workspace
