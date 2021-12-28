FROM golang:1.16

WORKDIR /src

COPY . .

RUN go build -o /bin/smtpauth .

ENTRYPOINT [ "/bin/smtpauth" ]
