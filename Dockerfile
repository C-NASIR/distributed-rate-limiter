FROM golang:1.21 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN go build -o /out/ratelimit ./cmd/ratelimit

FROM gcr.io/distroless/base-debian12

COPY --from=build /out/ratelimit /ratelimit

EXPOSE 8080
EXPOSE 9090

ENTRYPOINT ["/ratelimit"]
