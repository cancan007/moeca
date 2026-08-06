# Orchestra RAG indexer — indexes a read-only knowledge mount into an in-memory
# embedding store and answers similarity queries. Reachable only by the gateway
# (for /rag/search) + a loopback management port; holds no API key (embeddings go
# through the gateway).
#
# Build:  docker build -t orchestra/rag:latest .

FROM golang:1.25 AS build
WORKDIR /src
# Modules first so a source-only edit does not re-download the PDF parser
# (the one dependency: text-layer extraction, pure Go — see internal/index/media.go).
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /ragindex .

FROM gcr.io/distroless/static
COPY --from=build /ragindex /ragindex
ENTRYPOINT ["/ragindex"]
