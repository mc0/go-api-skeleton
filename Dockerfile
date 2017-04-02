FROM alpine
ADD go-api-skeleton /go-api-skeleton
ENTRYPOINT ["/go-api-skeleton"]
