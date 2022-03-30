FROM golang:latest
WORKDIR /go/src/github.com/victor-leee/side-car/
COPY ./ ./
RUN go build -o main cmd/server/main.go
EXPOSE 80
CMD ["./main"]