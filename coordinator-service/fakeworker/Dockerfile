# syntax=docker/dockerfile:1
# Build context must be the repo root (same reason as ../Dockerfile).

FROM golang:1.22-alpine AS build
WORKDIR /src
COPY shared/ ./shared/
COPY coordinator-service/ ./coordinator-service/
WORKDIR /src/coordinator-service
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -o /out/fakeworker ./fakeworker

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/fakeworker /fakeworker
USER 65532:65532
ENTRYPOINT ["/fakeworker"]
