FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download || (sleep 2 && go mod download)
COPY . .
ARG CMD
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /ledger ./cmd/${CMD}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /ledger /ledger
ENTRYPOINT ["/ledger"]
CMD ["serve"]
