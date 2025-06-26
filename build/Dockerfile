FROM golang:1.24-alpine AS builder

WORKDIR /usr/src/app

RUN apk add --no-cache make git

COPY go.mod go.mod
COPY go.sum go.sum

RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

ARG VERSION
ARG BUILD_DATE
ENV VERSION=${VERSION}
ENV BUILD_DATE=${BUILD_DATE}

COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    make build-dist

FROM uselagoon/commons:latest

RUN mkdir -p /restore && fix-permissions /restore \
  && mkdir -p /archive && fix-permissions /archive

COPY --from=builder /usr/src/app/bin/restore-files-task /usr/local/bin/restore-files-task

CMD ["/usr/local/bin/restore-files-task", "restore"]
