FROM golang:1.27.0-alpine AS dev

RUN apk add --no-cache ca-certificates git \
    && go install github.com/air-verse/air@v1.67.4

WORKDIR /app

CMD ["air", "--build.cmd", "go build -o /tmp/air/calbot ./cmd/calbot", "--build.entrypoint", "/tmp/air/calbot", "--tmp_dir", "/tmp/air", "--", "api"]


FROM golang:1.27.0-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/calbot \
    ./cmd/calbot


FROM gcr.io/distroless/static-debian12:nonroot AS production

COPY --from=builder --chown=nonroot:nonroot /out/calbot /calbot

USER nonroot:nonroot
ENTRYPOINT ["/calbot"]
