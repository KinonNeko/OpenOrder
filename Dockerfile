FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/openorder ./cmd/openorder

FROM alpine:3.22
RUN adduser -D -u 10001 openorder
USER openorder
COPY --from=build /out/openorder /usr/local/bin/openorder
EXPOSE 8080
ENTRYPOINT ["openorder"]
