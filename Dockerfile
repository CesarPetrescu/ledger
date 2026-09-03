FROM golang:1.25.14-alpine AS build
ARG CMD
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /ledger ./cmd/${CMD}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /ledger /ledger
ENTRYPOINT ["/ledger"]
CMD ["serve"]
