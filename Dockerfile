# The purchase component alone: a static binary on Alpine (the health check runs through /bin/sh and wget).
FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /erp-purchase .

FROM alpine:3.24.2
COPY --from=build /erp-purchase /erp-purchase
EXPOSE 8085 9095
ENTRYPOINT ["/erp-purchase"]
