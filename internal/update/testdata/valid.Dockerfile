# confine-ai:managed tool=base-image kind=image
FROM debian:bookworm-slim

# confine-ai:managed tool=go kind=version
ARG GO_VERSION=1.26.0
# confine-ai:managed tool=go kind=sha256 arch=amd64
ARG GO_SHA256_AMD64=aac1b08a0fb0c4e0a7c1555beb7b59180b05dfc5a3d62e40e9de90cd42f88235
# confine-ai:managed tool=go kind=sha256 arch=arm64
ARG GO_SHA256_ARM64=bd03b743eb6eb4193ea3c3fd3956546bf0e3ca5b7076c8226334afe6b75704cd

# confine-ai:managed tool=java kind=version distribution=corretto
ARG CORRETTO_VERSION=25.0.2.10.1
# confine-ai:managed tool=java kind=sha256 arch=amd64 distribution=corretto
ARG CORRETTO_SHA256_AMD64=313e9921e573cf28a4876ab039d56b3a142e7b1b1e847b0dddd170b8dee80387
# confine-ai:managed tool=java kind=sha256 arch=arm64 distribution=corretto
ARG CORRETTO_SHA256_ARM64=6e966b3c3609c25f40e29d6cdb81f83f52a3723c8196a4c38e0d5d03e463c4e5

RUN echo hello
