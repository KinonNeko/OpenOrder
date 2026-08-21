FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/opendiscord ./cmd/opendiscord

FROM alpine:3.22
RUN adduser -D -u 10001 opendiscord
USER opendiscord
COPY --from=build /out/opendiscord /usr/local/bin/opendiscord
EXPOSE 8080
ENTRYPOINT ["opendiscord"]
