FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod ./
COPY main.go .
RUN go mod tidy
RUN CGO_ENABLED=0 go build -o server main.go

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=build /app/server /server
EXPOSE 8080
CMD ["/server"]
