FROM debian:bookworm-slim AS builder
RUN echo build

FROM debian:bookworm-slim
RUN echo runtime
