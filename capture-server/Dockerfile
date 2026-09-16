FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN go test ./... && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/capture-api .

FROM scratch

COPY --from=build /out/capture-api /capture-api

USER 65532:65532
EXPOSE 8787
ENTRYPOINT ["/capture-api"]

