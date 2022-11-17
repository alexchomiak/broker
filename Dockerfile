FROM golang:latest

WORKDIR /app

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY Makefile .


COPY cmd ./cmd



RUN make build


CMD ["./bin/broker"]
