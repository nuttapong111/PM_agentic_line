FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/richmenu ./cmd/richmenu

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/ /app/
RUN mkdir -p /app/data && chown app /app/data
USER app
ENV PORT=3000
EXPOSE 3000
CMD ["/app/server"]
