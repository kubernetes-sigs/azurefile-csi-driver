FROM golang:1.12-alpine3.9@sha256:03b3b1d0fc2ca786fd4342967f3fb3b66215def8a0c3701301a6d0a94d8bfa86 as builder
WORKDIR /go/src/github.com/pelletier/go-toml
COPY . .
ENV CGO_ENABLED=0
ENV GOOS=linux
RUN go install ./...

FROM scratch
COPY --from=builder /go/bin/tomll /usr/bin/tomll
COPY --from=builder /go/bin/tomljson /usr/bin/tomljson
COPY --from=builder /go/bin/jsontoml /usr/bin/jsontoml
