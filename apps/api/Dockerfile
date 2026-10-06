# ---------- dev ----------
FROM golang:1.23-alpine AS dev
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-buildvcs=false
RUN apk add --no-cache git
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
EXPOSE 8080
CMD ["go", "run", "./cmd/api"]

# ---------- build ----------
FROM golang:1.23-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# ---------- runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/api /api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
