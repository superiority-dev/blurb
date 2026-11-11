# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm@sha256:dc0963f32053e4bc451d24d87e3892d42d01021b61f093e98eb95c0352bb84b0 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS TARGETARCH
ARG VERSION=dev COMMIT=unknown DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
	go build -trimpath \
	-ldflags="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" \
	-o /out/blurb ./cmd/blurb

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY LICENSE /LICENSE
COPY --from=build /out/blurb /blurb

EXPOSE 8080

ENTRYPOINT ["/blurb"]
