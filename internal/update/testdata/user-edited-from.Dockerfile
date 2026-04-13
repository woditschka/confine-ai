# confine-ai:managed tool=base-image kind=image
FROM ubuntu:24.04

# confine-ai:managed tool=go kind=version
ARG GO_VERSION=1.26.0
# confine-ai:managed tool=go kind=sha256 arch=amd64
ARG GO_SHA256_AMD64=aac1b08a0fb0c4e0a7c1555beb7b59180b05dfc5a3d62e40e9de90cd42f88235
# confine-ai:managed tool=go kind=sha256 arch=arm64
ARG GO_SHA256_ARM64=bd03b743eb6eb4193ea3c3fd3956546bf0e3ca5b7076c8226334afe6b75704cd
