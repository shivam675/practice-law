# ---------- dev ----------
# Source is bind-mounted and air rebuilds on save, so the image is only
# rebuilt when dependencies change.
FROM golang:1.23-alpine AS dev
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-buildvcs=false
RUN apk add --no-cache git
RUN go install github.com/air-verse/air@v1.61.1
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]

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
