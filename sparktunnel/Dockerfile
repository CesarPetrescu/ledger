FROM alpine:3.22
RUN apk add --no-cache ca-certificates
ADD --checksum=sha256:b9f65746d0886e1a651ff404c40c9ca0a9fa2cfa3cc5e9a4fd307b2aaef921fb --chmod=755 https://webhost.photonspark.com/api/v1/downloads/spark-tunnel/linux/amd64?version=0.3.1 /usr/local/bin/spark-tunnel
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/spark-tunnel"]
CMD ["run", "--server", "https://webhost.photonspark.com", "--target", "http://nginx:8080"]
