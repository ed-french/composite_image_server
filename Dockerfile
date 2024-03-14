FROM golang:1.19
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY layouts.json ./
COPY templates ./templates
RUN CGO_ENABLED=0 GOOS=linux go build -o /composite_image_server

EXPOSE 8080
CMD ["/composite_image_server"]