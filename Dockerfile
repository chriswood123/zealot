# Multi-stage build → distroless static image with a single zealot binary.
#
#   docker build -t zealot:latest .
#   docker run --rm zealot:latest version
#   docker run -p 8080:8080 -v $PWD/examples/node.yaml:/node.yaml:ro \
#     zealot:latest serve --config /node.yaml
FROM golang:1.26 AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/chriswood123/zealot/internal/metrics.Version=${VERSION}" \
      -o /out/zealot ./cmd/zealot

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/zealot /zealot
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/zealot"]
