FROM debian:bookworm-slim

ARG GO_VERSION=1.26.0

# confine-ai:managed tool=go kind=sha256 arch=amd64
ARG GO_SHA256_AMD64=aac1b08a0fb0c4e0a7c1555beb7b59180b05dfc5a3d62e40e9de90cd42f88235
