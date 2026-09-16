FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum *.go ./
RUN go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /notes-api .
FROM scratch
COPY --from=build /notes-api /notes-api
USER 65532:65532
EXPOSE 8788
ENTRYPOINT ["/notes-api"]
