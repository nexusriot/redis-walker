# Test image for the hermetic end-to-end suite. Built from the repository root.
FROM golang:1.25-alpine

ENV CGO_ENABLED=0 GOFLAGS=-mod=mod
WORKDIR /src

# Dependencies first so that editing the sources does not invalidate the layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build once so that a compile error fails fast, then run the suite.
RUN go build ./... && go vet -tags=e2e ./...

CMD ["go", "test", "-tags=e2e", "-count=1", "-v", "-timeout=10m", "./test/e2e/..."]
